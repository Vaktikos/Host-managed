import { useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { useNavigate } from 'react-router-dom'
import { ApiError, api } from '../api/client'
import type { Interface, Job, StoragePool, StorageVolume, VMCreate, VirtNetwork } from '../api/types'
import { Dialog, ErrorAlert, Field, useToast } from '../components/ui'
import { formatBytes } from '../lib/format'
import { CloudInitForm, cloudInitErrors, draftFrom, toCloudInit } from './CloudInitForm'
import { useResources } from '../resources'

const nameRe = /^[a-zA-Z0-9][a-zA-Z0-9._-]{0,62}$/
const tagRe = /^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,31}$/

export function VMCreateDialog({ onClose }: { onClose: () => void }) {
  const { t } = useTranslation()
  const toast = useToast()
  const nav = useNavigate()
  const { refreshAll, vms } = useResources()
  const [pools, setPools] = useState<StoragePool[]>([])
  const [nets, setNets] = useState<VirtNetwork[]>([])
  const [bridges, setBridges] = useState<Interface[]>([])
  const [isos, setIsos] = useState<{ pool: string; vol: StorageVolume }[]>([])
  const [loadErr, setLoadErr] = useState<ApiError>()
  const [err, setErr] = useState<ApiError>()
  const [busy, setBusy] = useState(false)
  const [touched, setTouched] = useState(false)
  const [tags, setTags] = useState('')
  const [iso, setIso] = useState('')
  const [source, setSource] = useState<'iso' | 'image'>('iso')
  const [images, setImages] = useState<{ pool: string; vol: StorageVolume }[]>([])
  const [image, setImage] = useState('')
  const [useCI, setUseCI] = useState(true)
  const [ci, setCI] = useState(draftFrom(undefined, ''))
  const [f, setF] = useState<VMCreate>({
    name: '', description: '', tags: [], vcpus: 2, memory_mib: 2048, cpu_mode: 'host-passthrough',
    firmware: 'uefi', secure_boot: false, tpm: false, disk_pool: '', disk_size_gib: 32, disk_bus: 'virtio',
    network: '', nic_model: 'virtio', guest_agent: true, autostart: false, start: true,
  })
  const set = <K extends keyof VMCreate>(k: K, v: VMCreate[K]) => setF((x) => ({ ...x, [k]: v }))

  useEffect(() => {
    Promise.all([api.get<StoragePool[]>('/storage'), api.get<VirtNetwork[]>('/networks'), api.get<Interface[]>('/node/interfaces')])
      .then(async ([p, n, ifs]) => {
        setPools(p)
        setNets(n)
        const br = ifs.filter((i) => i.kind === 'bridge' && !n.some((x) => x.bridge === i.name))
        setBridges(br)
        const imagePool = p.find((x) => x.active && x.content === 'images')
        const defNet = n.find((x) => x.active)
        setF((x) => ({
          ...x,
          disk_pool: x.disk_pool || imagePool?.name || '',
          network: x.network || (br[0] ? `bridge:${br[0].name}` : defNet ? `network:${defNet.name}` : ''),
        }))
        const isoPools = p.filter((x) => x.active && x.content === 'iso')
        const lists = await Promise.all(isoPools.map((ip) => api.get<StorageVolume[]>(`/storage/${encodeURIComponent(ip.name)}/volumes`).then((vs) => vs.map((vol) => ({ pool: ip.name, vol })))))
        setIsos(lists.flat().filter((x) => x.vol.name.toLowerCase().endsWith('.iso')))
        // Disk images that are not disks of existing VMs: cloud images.
        const imgPools = p.filter((x) => x.active && x.content === 'images')
        const imgLists = await Promise.all(imgPools.map((ip) => api.get<StorageVolume[]>(`/storage/${encodeURIComponent(ip.name)}/volumes`).then((vs) => vs.map((vol) => ({ pool: ip.name, vol })))))
        setImages(imgLists.flat().filter((x) => /\.(qcow2|img|raw)$/i.test(x.vol.name) && !/-disk\d+(\.qcow2)?$/.test(x.vol.name) && !x.vol.name.endsWith('-cloudinit.iso')))
      })
      .catch((e) => setLoadErr(e))
  }, [])

  const errors = useMemo(() => {
    const e: Partial<Record<string, string>> = {}
    if (!nameRe.test(f.name)) e.name = t('validation.vm_name')
    else if (vms.data?.some((v) => v.name === f.name)) e.name = t('validation.name_taken')
    if (!(f.vcpus >= 1 && f.vcpus <= 512)) e.vcpus = t('validation.range', { min: 1, max: 512 })
    if (!(f.memory_mib >= 64)) e.memory_mib = t('validation.min', { min: 64 })
    if (!(f.disk_size_gib >= 1 && f.disk_size_gib <= 65536)) e.disk_size_gib = t('validation.range', { min: 1, max: 65536 })
    if (!f.disk_pool) e.disk_pool = t('validation.required')
    if (!f.network) e.network = t('validation.required')
    const tl = tags.split(',').map((s) => s.trim()).filter(Boolean)
    if (tl.some((x) => !tagRe.test(x)) || tl.length > 16) e.tags = t('validation.tags')
    if (f.secure_boot && f.firmware !== 'uefi') e.secure_boot = t('validation.secure_boot_uefi')
    if (source === 'image') {
      if (!image) e.image = t('validation.required')
      if (useCI) for (const [k, v] of Object.entries(cloudInitErrors(ci, t))) e['ci.' + k] = v
    }
    return e
  }, [f, tags, t, vms.data, source, image, useCI, ci])
  const serverField = err?.field

  const submit = async () => {
    setTouched(true)
    if (Object.keys(errors).length > 0) return
    setBusy(true)
    setErr(undefined)
    const [isoPool, isoVol] = iso ? iso.split('\u0000') : ['', '']
    const [imgPool, imgVol] = image ? image.split('\u0000') : ['', '']
    const body: VMCreate = {
      ...f,
      tags: tags.split(',').map((s) => s.trim()).filter(Boolean),
      ...(source === 'iso' && iso ? { iso_pool: isoPool, iso_volume: isoVol } : {}),
      ...(source === 'image' ? { image_pool: imgPool, image_volume: imgVol } : {}),
      ...(source === 'image' && useCI ? { cloud_init: toCloudInit({ ...ci, hostname: ci.hostname || f.name }) } : {}),
    }
    try {
      const res = await api.post<{ job: Job }>('/vms', body)
      toast('info', t('tasks.started', { id: res.job.id }))
      refreshAll()
      onClose()
      nav(`/tasks/${res.job.id}`)
    } catch (e) {
      setErr(e as ApiError)
      setBusy(false)
    }
  }

  const fe = (k: string) => (touched ? errors[k] : undefined) ?? (serverField === k ? err?.message : undefined)
  const num = (v: string) => (v === '' ? NaN : Number(v))

  return (
    <Dialog
      title={t('vm.create')}
      onClose={onClose}
      wide
      footer={
        <>
          <button className="btn" onClick={onClose}>{t('common.cancel')}</button>
          <button className="btn primary" onClick={submit} disabled={busy}>{busy ? t('common.working') : t('vm.create')}</button>
        </>
      }
    >
      <ErrorAlert error={loadErr} />
      <div className="form-grid">
        <Field label={t('common.name')} error={fe('name')} htmlFor="vm-name">
          <input id="vm-name" className={`input${fe('name') ? ' invalid' : ''}`} value={f.name} onChange={(e) => set('name', e.target.value)} placeholder="web-01" autoComplete="off" />
        </Field>
        <Field label={t('vm.tags')} error={fe('tags')} hint={t('vm.tags_hint')} htmlFor="vm-tags">
          <input id="vm-tags" className="input" value={tags} onChange={(e) => setTags(e.target.value)} placeholder="prod, web" />
        </Field>
        <Field label={t('vm.description')} className="full" htmlFor="vm-desc">
          <input id="vm-desc" className="input" value={f.description} onChange={(e) => set('description', e.target.value)} />
        </Field>

        <div className="legend">{t('vm.section_cpu_memory')}</div>
        <Field label={t('vm.vcpus')} error={fe('vcpus')} htmlFor="vm-cpus">
          <input id="vm-cpus" className="input" type="number" min={1} max={512} value={f.vcpus} onChange={(e) => set('vcpus', num(e.target.value))} />
        </Field>
        <Field label={t('vm.memory_mib')} error={fe('memory_mib')} hint={formatBytes(f.memory_mib * 1048576)} htmlFor="vm-mem">
          <input id="vm-mem" className="input" type="number" min={64} step={256} value={f.memory_mib} onChange={(e) => set('memory_mib', num(e.target.value))} />
        </Field>
        <Field label={t('vm.cpu_mode')} hint={t(`vm.cpu_mode_hint.${f.cpu_mode.replace('-', '_')}`)} htmlFor="vm-cpumode">
          <select id="vm-cpumode" className="select" value={f.cpu_mode} onChange={(e) => set('cpu_mode', e.target.value)}>
            <option value="host-passthrough">host-passthrough</option>
            <option value="host-model">host-model</option>
            <option value="qemu64">qemu64</option>
          </select>
        </Field>
        <Field label={t('vm.firmware')} htmlFor="vm-fw">
          <select id="vm-fw" className="select" value={f.firmware} onChange={(e) => setF((x) => ({ ...x, firmware: e.target.value, secure_boot: e.target.value === 'uefi' && x.secure_boot }))}>
            <option value="uefi">UEFI (OVMF)</option>
            <option value="bios">BIOS (SeaBIOS)</option>
          </select>
        </Field>
        <div className="full row wrap" style={{ gap: 18 }}>
          <label className="check">
            <input type="checkbox" checked={f.secure_boot} disabled={f.firmware !== 'uefi'} onChange={(e) => set('secure_boot', e.target.checked)} />
            {t('vm.secure_boot')}
          </label>
          <label className="check">
            <input type="checkbox" checked={f.tpm} onChange={(e) => set('tpm', e.target.checked)} />
            {t('vm.tpm')}
          </label>
          <span className="faint">{t('vm.secure_boot_hint')}</span>
        </div>

        <div className="legend">{t('vm.section_disk')}</div>
        <Field label={t('vm.storage_pool')} error={fe('disk_pool')} htmlFor="vm-pool">
          <select id="vm-pool" className="select" value={f.disk_pool} onChange={(e) => set('disk_pool', e.target.value)}>
            <option value="">{t('common.select')}</option>
            {pools.filter((p) => p.content === 'images').map((p) => (
              <option key={p.name} value={p.name} disabled={!p.active}>
                {p.name} ({p.type}) · {t('storage.free_of', { free: formatBytes(p.available), total: formatBytes(p.capacity) })}
              </option>
            ))}
          </select>
        </Field>
        <Field label={t('vm.disk_size_gib')} error={fe('disk_size_gib')} hint={t('vm.thin_hint')} htmlFor="vm-disk">
          <input id="vm-disk" className="input" type="number" min={1} value={f.disk_size_gib} onChange={(e) => set('disk_size_gib', num(e.target.value))} />
        </Field>
        <Field label={t('vm.disk_bus')} htmlFor="vm-bus">
          <select id="vm-bus" className="select" value={f.disk_bus} onChange={(e) => set('disk_bus', e.target.value)}>
            <option value="virtio">VirtIO Block</option>
            <option value="scsi">VirtIO SCSI</option>
            <option value="sata">SATA</option>
          </select>
        </Field>
        <Field label={t('vm.source')} htmlFor="vm-src">
          <select id="vm-src" className="select" value={source} onChange={(e) => setSource(e.target.value as 'iso' | 'image')}>
            <option value="iso">{t('vm.source_iso')}</option>
            <option value="image">{t('vm.source_image')}</option>
          </select>
        </Field>
        {source === 'iso' ? (
        <Field label={t('vm.iso')} hint={isos.length === 0 ? t('vm.no_iso_hint') : undefined} htmlFor="vm-iso">
          <select id="vm-iso" className="select" value={iso} onChange={(e) => setIso(e.target.value)}>
            <option value="">{t('vm.no_iso')}</option>
            {isos.map((i) => (
              <option key={i.pool + i.vol.name} value={`${i.pool}\u0000${i.vol.name}`}>
                {i.pool}: {i.vol.name} ({formatBytes(i.vol.capacity)})
              </option>
            ))}
          </select>
        </Field>
        ) : (
          <Field label={t('vm.image')} error={fe('image')} hint={images.length === 0 ? t('vm.no_image_hint') : t('vm.image_hint')} htmlFor="vm-img">
            <select id="vm-img" className="select" value={image} onChange={(e) => setImage(e.target.value)}>
              <option value="">{t('common.select')}</option>
              {images.map((i) => (
                <option key={i.pool + i.vol.name} value={`${i.pool}\u0000${i.vol.name}`}>
                  {i.pool}: {i.vol.name} ({formatBytes(i.vol.capacity)})
                </option>
              ))}
            </select>
          </Field>
        )}
        {source === 'image' && (
          <>
            <div className="legend">Cloud-Init</div>
            <label className="check full">
              <input type="checkbox" checked={useCI} onChange={(e) => setUseCI(e.target.checked)} />
              {t('ci.enable')}
            </label>
            {useCI && (
              <div className="full">
                <CloudInitForm d={{ ...ci, hostname: ci.hostname || f.name }} set={setCI} errors={touched ? Object.fromEntries(Object.entries(errors).filter(([k]) => k.startsWith('ci.')).map(([k, v]) => [k.slice(3), v as string])) : {}} />
              </div>
            )}
          </>
        )}

        <div className="legend">{t('vm.section_network')}</div>
        <Field label={t('vm.network')} error={fe('network')} htmlFor="vm-net">
          <select id="vm-net" className="select" value={f.network} onChange={(e) => set('network', e.target.value)}>
            <option value="">{t('common.select')}</option>
            {bridges.map((b) => (
              <option key={b.name} value={`bridge:${b.name}`}>{t('vm.bridge_option', { name: b.name })}</option>
            ))}
            {nets.map((n) => (
              <option key={n.name} value={`network:${n.name}`} disabled={!n.active}>
                {t('vm.network_option', { name: n.name, mode: n.forward })}
              </option>
            ))}
          </select>
        </Field>
        <Field label={t('vm.nic_model')} htmlFor="vm-nic">
          <select id="vm-nic" className="select" value={f.nic_model} onChange={(e) => set('nic_model', e.target.value)}>
            <option value="virtio">VirtIO</option>
            <option value="e1000">Intel E1000</option>
            <option value="rtl8139">Realtek RTL8139</option>
          </select>
        </Field>

        <div className="legend">{t('vm.section_options')}</div>
        <div className="full row wrap" style={{ gap: 18 }}>
          <label className="check">
            <input type="checkbox" checked={f.guest_agent} onChange={(e) => set('guest_agent', e.target.checked)} />
            {t('vm.guest_agent_channel')}
          </label>
          <label className="check">
            <input type="checkbox" checked={f.autostart} onChange={(e) => set('autostart', e.target.checked)} />
            {t('vm.autostart')}
          </label>
          <label className="check">
            <input type="checkbox" checked={f.start} onChange={(e) => set('start', e.target.checked)} />
            {t('vm.start_after_create')}
          </label>
        </div>
      </div>
      {err && !err.field && <ErrorAlert error={err} />}
    </Dialog>
  )
}
