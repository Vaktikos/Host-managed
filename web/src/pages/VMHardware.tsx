import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { ApiError, api } from '../api/client'
import type { Interface, StoragePool, StorageVolume, VMDetail, VirtNetwork } from '../api/types'
import { useAuth } from '../auth'
import { Icon } from '../components/icons'
import { ConfirmDialog, Copy, Dialog, ErrorAlert, Field, useErrorText, useToast } from '../components/ui'
import { formatBytes } from '../lib/format'

interface ChangeResult {
  pending_restart: boolean
  target?: string
}

// useChange runs a hardware change and reports whether it waits for a restart.
function useChange(onChanged: () => void) {
  const { t } = useTranslation()
  const toast = useToast()
  return async (fn: () => Promise<ChangeResult | undefined>) => {
    const r = await fn()
    toast('ok', r?.pending_restart ? t('vm.saved_restart') : t('common.saved'))
    onChanged()
  }
}

export function Hardware({ vm, onChanged }: { vm: VMDetail; onChanged: () => void }) {
  const { t } = useTranslation()
  const { can } = useAuth()
  const toast = useToast()
  const errText = useErrorText()
  const change = useChange(onChanged)
  const editable = can('vm.config')
  const [addDisk, setAddDisk] = useState(false)
  const [resize, setResize] = useState<string | null>(null)
  const [media, setMedia] = useState<string | null>(null)
  const [removeDisk, setRemoveDisk] = useState<string | null>(null)
  const [deleteData, setDeleteData] = useState(false)
  const [addNIC, setAddNIC] = useState(false)
  const [removeNIC, setRemoveNIC] = useState<string | null>(null)
  const [boot, setBoot] = useState(false)
  const base = `/vms/${encodeURIComponent(vm.name)}`
  const running = vm.state !== 'shutoff'

  const run = async (fn: () => Promise<ChangeResult | undefined>) => {
    try {
      await change(fn)
    } catch (e) {
      toast('err', errText(e))
    }
  }
  const done: DoneFn = (r) => {
    toast('ok', r?.pending_restart ? t('vm.saved_restart') : t('common.saved'))
    onChanged()
  }

  return (
    <div className="stack">
      <div className="panel">
        <div className="panel-head">
          <h2>{t('vm.machine')}</h2>
          <div className="actions">{editable && <button className="btn sm" onClick={() => setBoot(true)}>{t('vm.edit_boot_order')}</button>}</div>
        </div>
        <div className="panel-body">
          <dl className="kv">
            <dt>{t('vm.hypervisor')}</dt><dd>{vm.domain_type === 'kvm' ? 'KVM' : t('vm.software_emulation')}</dd>
            <dt>{t('vm.machine_type')}</dt><dd className="mono">{vm.machine}</dd>
            <dt>{t('vm.firmware')}</dt><dd>{vm.firmware === 'uefi' ? 'UEFI' : 'BIOS'}{vm.secure_boot ? ` · ${t('vm.secure_boot')}` : ''}</dd>
            <dt>{t('vm.tpm')}</dt><dd>{vm.tpm ? 'TPM 2.0 (swtpm)' : t('common.none')}</dd>
            <dt>{t('vm.boot_order')}</dt><dd className="mono">{vm.boot_order?.join(' → ') ?? '–'}</dd>
            <dt>{t('vm.console')}</dt><dd>{vm.console.vnc ? 'VNC' : t('common.none')}</dd>
            <dt>{t('vm.guest_agent_channel')}</dt><dd>{vm.guest_agent_channel ? t('common.yes') : t('common.no')}</dd>
          </dl>
        </div>
      </div>

      <div className="panel">
        <div className="panel-head">
          <h2>{t('vm.disks')}</h2>
          <div className="actions">{editable && <button className="btn sm" onClick={() => setAddDisk(true)}><Icon name="plus" size={12} /> {t('vm.add_disk')}</button>}</div>
        </div>
        <table className="data">
          <thead><tr><th>{t('vm.device')}</th><th>{t('vm.type')}</th><th>{t('vm.bus')}</th><th>{t('vm.format')}</th><th className="num">{t('storage.size')}</th><th>{t('vm.source')}</th>{editable && <th />}</tr></thead>
          <tbody>
            {vm.disks.map((d) => (
              <tr key={d.target}>
                <td className="mono">{d.target}</td>
                <td>{d.device === 'cdrom' ? t('vm.cdrom') : t('vm.disk')}</td>
                <td>{d.bus}</td>
                <td>{d.format}</td>
                <td className="num">{d.capacity ? formatBytes(d.capacity) : '–'}</td>
                <td className="mono truncate" title={d.source}>{d.source || <span className="faint">{t('vm.empty_drive')}</span>}</td>
                {editable && (
                  <td className="right nowrap">
                    {d.device === 'cdrom' ? (
                      <>
                        <button className="btn sm" onClick={() => setMedia(d.target)}>{t('vm.insert_iso')}</button>{' '}
                        <button className="btn sm" disabled={!d.source} onClick={() => run(() => api.put<ChangeResult>(`${base}/cdrom/${d.target}`, {}))}>{t('vm.eject')}</button>
                      </>
                    ) : (
                      <>
                        <button className="btn sm" onClick={() => setResize(d.target)}>{t('vm.resize')}</button>{' '}
                        <button className="btn sm danger" onClick={() => { setDeleteData(false); setRemoveDisk(d.target) }}>{t('vm.remove')}</button>
                      </>
                    )}
                  </td>
                )}
              </tr>
            ))}
          </tbody>
        </table>
      </div>

      <div className="panel">
        <div className="panel-head">
          <h2>{t('vm.nics')}</h2>
          <div className="actions">{editable && <button className="btn sm" onClick={() => setAddNIC(true)}><Icon name="plus" size={12} /> {t('vm.add_nic')}</button>}</div>
        </div>
        <table className="data">
          <thead><tr><th>MAC</th><th>{t('vm.model')}</th><th>{t('vm.attached_to')}</th><th>{t('vm.host_device')}</th>{editable && <th />}</tr></thead>
          <tbody>
            {vm.nics.map((n) => (
              <tr key={n.mac}>
                <td className="mono">{n.mac} <Copy value={n.mac} /></td>
                <td>{n.model}</td>
                <td>{n.type === 'bridge' ? t('vm.bridge_option', { name: n.source }) : t('vm.network_name', { name: n.source })}</td>
                <td className="mono faint">{n.target ?? '–'}</td>
                {editable && <td className="right"><button className="btn sm danger" onClick={() => setRemoveNIC(n.mac)}>{t('vm.remove')}</button></td>}
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      <div className="alert info">{running ? t('vm.hotplug_hint') : t('vm.hardware_offline_hint')}</div>

      {addDisk && <AddDiskDialog vm={vm} onClose={() => setAddDisk(false)} onDone={done} />}
      {resize && <ResizeDialog vm={vm} target={resize} onClose={() => setResize(null)} onDone={done} />}
      {media && <MediaDialog vm={vm} target={media} onClose={() => setMedia(null)} onDone={done} />}
      {addNIC && <AddNICDialog vm={vm} onClose={() => setAddNIC(false)} onDone={done} />}
      {boot && <BootDialog vm={vm} onClose={() => setBoot(false)} onDone={done} />}
      {removeDisk && (
        <ConfirmDialog
          title={t('vm.remove_disk_title', { target: removeDisk })}
          message={t('vm.remove_disk_text')}
          confirmLabel={t('vm.remove')}
          confirmText={deleteData ? removeDisk : undefined}
          onConfirm={() =>
            change(() => api.del<ChangeResult>(`${base}/disks/${removeDisk}${deleteData ? `?delete_volume=true&confirm=${removeDisk}` : ''}`))
          }
          onClose={() => setRemoveDisk(null)}
        >
          <label className="check">
            <input type="checkbox" checked={deleteData} disabled={running || !can('vm.delete')} onChange={(e) => setDeleteData(e.target.checked)} />
            {t('vm.remove_disk_delete_data')}
          </label>
          {running && <div className="faint">{t('vm.remove_disk_running')}</div>}
        </ConfirmDialog>
      )}
      {removeNIC && (
        <ConfirmDialog
          title={t('vm.remove_nic_title')}
          message={t('vm.remove_nic_text', { mac: removeNIC })}
          confirmLabel={t('vm.remove')}
          onConfirm={() => change(() => api.del<ChangeResult>(`${base}/nics/${removeNIC}`))}
          onClose={() => setRemoveNIC(null)}
        />
      )}
    </div>
  )
}

// Dialogs perform the request themselves (errors stay in the dialog) and
// report the result through done.
type DoneFn = (r: ChangeResult | undefined) => void

function SubmitDialog({ title, onClose, onSubmit, valid, children }: { title: string; onClose: () => void; onSubmit: () => Promise<void>; valid: boolean; children: React.ReactNode }) {
  const { t } = useTranslation()
  const [err, setErr] = useState<ApiError>()
  const [busy, setBusy] = useState(false)
  const submit = async () => {
    setBusy(true)
    setErr(undefined)
    try {
      await onSubmit()
      onClose()
    } catch (e) {
      setErr(e as ApiError)
      setBusy(false)
    }
  }
  return (
    <Dialog title={title} onClose={onClose} footer={<><button className="btn" onClick={onClose}>{t('common.cancel')}</button><button className="btn primary" disabled={!valid || busy} onClick={submit}>{t('common.save')}</button></>}>
      {children}
      <ErrorAlert error={err} />
    </Dialog>
  )
}

function usePools() {
  const [pools, setPools] = useState<StoragePool[]>([])
  useEffect(() => {
    api.get<StoragePool[]>('/storage').then(setPools).catch(() => setPools([]))
  }, [])
  return pools
}

function AddDiskDialog({ vm, onClose, onDone }: { vm: VMDetail; onClose: () => void; onDone: DoneFn }) {
  const { t } = useTranslation()
  const pools = usePools().filter((p) => p.content === 'images' && p.active)
  const [pool, setPool] = useState('')
  const [size, setSize] = useState(16)
  const [bus, setBus] = useState('virtio')
  useEffect(() => {
    if (!pool && pools[0]) setPool(pools[0].name)
  }, [pools, pool])
  return (
    <SubmitDialog
      title={t('vm.add_disk')}
      onClose={onClose}
      valid={!!pool && size >= 1}
      onSubmit={async () => onDone(await api.post<ChangeResult>(`/vms/${encodeURIComponent(vm.name)}/disks`, { pool, size_gib: size, bus }))}
    >
      <div className="form-grid">
        <Field label={t('vm.storage_pool')} htmlFor="ad-pool">
          <select id="ad-pool" className="select" value={pool} onChange={(e) => setPool(e.target.value)}>
            {pools.map((p) => <option key={p.name} value={p.name}>{p.name} · {t('storage.free_of', { free: formatBytes(p.available), total: formatBytes(p.capacity) })}</option>)}
          </select>
        </Field>
        <Field label={t('vm.disk_size_gib')} htmlFor="ad-size">
          <input id="ad-size" className="input" type="number" min={1} value={size} onChange={(e) => setSize(Number(e.target.value))} />
        </Field>
        <Field label={t('vm.disk_bus')} hint={bus === 'sata' && vm.state !== 'shutoff' ? t('vm.sata_no_hotplug') : undefined} htmlFor="ad-bus">
          <select id="ad-bus" className="select" value={bus} onChange={(e) => setBus(e.target.value)}>
            <option value="virtio">VirtIO Block</option>
            <option value="scsi">VirtIO SCSI</option>
            <option value="sata">SATA</option>
          </select>
        </Field>
      </div>
    </SubmitDialog>
  )
}

function ResizeDialog({ vm, target, onClose, onDone }: { vm: VMDetail; target: string; onClose: () => void; onDone: DoneFn }) {
  const { t } = useTranslation()
  const cur = Math.ceil((vm.disks.find((d) => d.target === target)?.capacity ?? 0) / 1073741824)
  const [size, setSize] = useState(cur + 8)
  return (
    <SubmitDialog
      title={t('vm.resize_title', { target })}
      onClose={onClose}
      valid={size > cur}
      onSubmit={async () => onDone(await api.put<ChangeResult>(`/vms/${encodeURIComponent(vm.name)}/disks/${target}/size`, { size_gib: size }))}
    >
      <Field label={t('vm.new_size_gib')} hint={t('vm.resize_hint', { cur })} error={size > cur ? undefined : t('validation.min', { min: cur + 1 })} htmlFor="rs-size">
        <input id="rs-size" className="input" type="number" min={cur + 1} value={size} onChange={(e) => setSize(Number(e.target.value))} />
      </Field>
    </SubmitDialog>
  )
}

function MediaDialog({ vm, target, onClose, onDone }: { vm: VMDetail; target: string; onClose: () => void; onDone: DoneFn }) {
  const { t } = useTranslation()
  const pools = usePools().filter((p) => p.content === 'iso' && p.active)
  const [isos, setIsos] = useState<{ pool: string; vol: StorageVolume }[]>([])
  const [sel, setSel] = useState('')
  useEffect(() => {
    Promise.all(pools.map((p) => api.get<StorageVolume[]>(`/storage/${encodeURIComponent(p.name)}/volumes`).then((vs) => vs.map((vol) => ({ pool: p.name, vol })))))
      .then((l) => setIsos(l.flat()))
      .catch(() => setIsos([]))
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [pools.length])
  const [pool, volume] = sel.split('\u0000')
  return (
    <SubmitDialog
      title={t('vm.insert_iso_title', { target })}
      onClose={onClose}
      valid={!!sel}
      onSubmit={async () => onDone(await api.put(`/vms/${encodeURIComponent(vm.name)}/cdrom/${target}`, { pool, volume }).then(() => undefined))}
    >
      <Field label={t('vm.iso')} hint={isos.length === 0 ? t('vm.no_iso_hint') : undefined} htmlFor="md-iso">
        <select id="md-iso" className="select" value={sel} onChange={(e) => setSel(e.target.value)}>
          <option value="">{t('common.select')}</option>
          {isos.map((i) => <option key={i.pool + i.vol.name} value={`${i.pool}\u0000${i.vol.name}`}>{i.pool}: {i.vol.name}</option>)}
        </select>
      </Field>
    </SubmitDialog>
  )
}

function AddNICDialog({ vm, onClose, onDone }: { vm: VMDetail; onClose: () => void; onDone: DoneFn }) {
  const { t } = useTranslation()
  const [nets, setNets] = useState<VirtNetwork[]>([])
  const [bridges, setBridges] = useState<Interface[]>([])
  const [network, setNetwork] = useState('')
  const [model, setModel] = useState('virtio')
  useEffect(() => {
    Promise.all([api.get<VirtNetwork[]>('/networks'), api.get<Interface[]>('/node/interfaces')]).then(([n, ifs]) => {
      setNets(n)
      setBridges(ifs.filter((i) => i.kind === 'bridge' && !n.some((x) => x.bridge === i.name)))
    })
  }, [])
  return (
    <SubmitDialog
      title={t('vm.add_nic')}
      onClose={onClose}
      valid={!!network}
      onSubmit={async () => onDone(await api.post<ChangeResult>(`/vms/${encodeURIComponent(vm.name)}/nics`, { network, model }))}
    >
      <div className="form-grid">
        <Field label={t('vm.network')} htmlFor="an-net">
          <select id="an-net" className="select" value={network} onChange={(e) => setNetwork(e.target.value)}>
            <option value="">{t('common.select')}</option>
            {bridges.map((b) => <option key={b.name} value={`bridge:${b.name}`}>{t('vm.bridge_option', { name: b.name })}</option>)}
            {nets.map((n) => <option key={n.name} value={`network:${n.name}`} disabled={!n.active}>{t('vm.network_option', { name: n.name, mode: n.forward })}</option>)}
          </select>
        </Field>
        <Field label={t('vm.nic_model')} htmlFor="an-model">
          <select id="an-model" className="select" value={model} onChange={(e) => setModel(e.target.value)}>
            <option value="virtio">VirtIO</option>
            <option value="e1000">Intel E1000</option>
            <option value="rtl8139">Realtek RTL8139</option>
          </select>
        </Field>
      </div>
    </SubmitDialog>
  )
}

function BootDialog({ vm, onClose, onDone }: { vm: VMDetail; onClose: () => void; onDone: DoneFn }) {
  const { t } = useTranslation()
  const all = [...vm.disks.map((d) => d.target), ...vm.nics.map((n) => `net:${n.mac}`)]
  const initial = vm.boot_order?.filter((e) => all.includes(e)) ?? []
  const [order, setOrder] = useState<string[]>(initial.length ? initial : all.slice(0, 1))
  const label = (e: string) => {
    if (e.startsWith('net:')) return t('vm.boot_nic', { mac: e.slice(4) })
    const d = vm.disks.find((x) => x.target === e)
    return `${e} (${d?.device === 'cdrom' ? t('vm.cdrom') : t('vm.disk')})`
  }
  const move = (i: number, dir: -1 | 1) => {
    const n = [...order]
    ;[n[i], n[i + dir]] = [n[i + dir], n[i]]
    setOrder(n)
  }
  return (
    <SubmitDialog
      title={t('vm.edit_boot_order')}
      onClose={onClose}
      valid={order.length > 0}
      onSubmit={async () => onDone(await api.put<ChangeResult>(`/vms/${encodeURIComponent(vm.name)}/boot`, { order }))}
    >
      <table className="data">
        <tbody>
          {order.map((e, i) => (
            <tr key={e}>
              <td style={{ width: 30 }}>{i + 1}.</td>
              <td className="mono">{label(e)}</td>
              <td className="right nowrap">
                <button className="btn sm" disabled={i === 0} onClick={() => move(i, -1)} aria-label={t('vm.move_up')}>↑</button>{' '}
                <button className="btn sm" disabled={i === order.length - 1} onClick={() => move(i, 1)} aria-label={t('vm.move_down')}>↓</button>{' '}
                <button className="btn sm" onClick={() => setOrder(order.filter((x) => x !== e))}>{t('vm.remove')}</button>
              </td>
            </tr>
          ))}
          {all.filter((e) => !order.includes(e)).map((e) => (
            <tr key={e}>
              <td />
              <td className="mono faint">{label(e)}</td>
              <td className="right"><button className="btn sm" onClick={() => setOrder([...order, e])}>{t('vm.add_to_boot')}</button></td>
            </tr>
          ))}
        </tbody>
      </table>
      <div className="faint">{t('vm.boot_hint')}</div>
    </SubmitDialog>
  )
}
