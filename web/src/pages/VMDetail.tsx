import { Suspense, lazy, useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Link, useParams, useSearchParams } from 'react-router-dom'
import { ApiError, api } from '../api/client'
import type { GuestInfo, VMDetail as Detail, VMStats } from '../api/types'
import { useAuth } from '../auth'
import { Icon } from '../components/icons'
import { Copy, Dialog, ErrorAlert, Field, Menu, Sparkline, StatusDot, Tabs, useErrorText, useToast } from '../components/ui'
import { formatBytes, formatMiB, formatPercent, formatRate } from '../lib/format'
import { usePoll } from '../lib/hooks'
import { useResources } from '../resources'
import { GuestBackups } from './Backups'
import { JobsTable } from './Tasks'
import { Hardware } from './VMHardware'
import { Snapshots } from './VMSnapshots'
import { VMCloudInit } from './VMCloudInit'
import { useVMActions, vmActionAllowed } from './vmActions'

// Consoles are loaded on demand to keep the initial bundle small.
const VNCConsole = lazy(() => import('../console/VNCConsole').then((m) => ({ default: m.VNCConsole })))

export function VMDetail() {
  const { t } = useTranslation()
  const { name = '' } = useParams()
  const [params, setParams] = useSearchParams()
  const tab = params.get('tab') ?? 'summary'
  const { can } = useAuth()
  const vm = usePoll(() => api.get<Detail>(`/vms/${encodeURIComponent(name)}`), 4000, [name])
  const actions = useVMActions(vm.reload)
  const v = vm.data

  if (vm.error?.status === 404) {
    return (
      <div className="page">
        <div className="alert warn">{t('vm.not_found', { name })} <Link to="/vms">{t('nav.vms')}</Link></div>
      </div>
    )
  }

  const tabs = [
    { id: 'summary', label: t('vm.tab.summary') },
    { id: 'hardware', label: t('vm.tab.hardware') },
    ...(can('vm.console') ? [{ id: 'console', label: t('vm.tab.console') }] : []),
    { id: 'snapshots', label: t('vm.tab.snapshots') },
    ...(can('backup.view') ? [{ id: 'backups', label: t('vm.tab.backups') }] : []),
    ...(v?.cloud_init ? [{ id: 'cloudinit', label: 'Cloud-Init' }] : []),
    { id: 'notes', label: t('vm.tab.notes') },
    { id: 'tasks', label: t('vm.tab.tasks') },
  ]

  const power = (a: string, icon: string) =>
    can('vm.power') && (
      <button className="btn" disabled={!v || !vmActionAllowed(v.state, a, v.template)} onClick={() => v && actions.request(v, a)}>
        <Icon name={icon} size={14} /> {t(`vm.action.${a}`)}
      </button>
    )

  return (
    <div className="page">
      <div className="page-head">
        <div>
          <div className="crumbs"><Link to="/vms">{t('nav.vms')}</Link> / {name}</div>
          <div className="row">
            <h1>{name}</h1>
            {v && <StatusDot state={v.state} />}
            {v?.template && <span className="badge">{t('vm.template')}</span>}
            {v?.domain_type === 'qemu' && <span className="badge warn">{t('vm.software_emulation')}</span>}
          </div>
        </div>
        <div className="actions">
          {power('start', 'play')}
          {power('shutdown', 'power')}
          {power('reboot', 'refresh')}
          {can('vm.console') && (
            <button className="btn" disabled={v?.state !== 'running'} onClick={() => setParams({ tab: 'console' })}>
              <Icon name="terminal" size={14} /> {t('vm.tab.console')}
            </button>
          )}
          {v && <Menu items={actions.items(v)} label={t('common.actions')} />}
        </div>
      </div>
      <ErrorAlert error={vm.error} />
      <Tabs tabs={tabs} active={tab} onChange={(id) => setParams({ tab: id })} />
      {v && tab === 'summary' && <Summary vm={v} onChanged={vm.reload} />}
      {v && tab === 'hardware' && <Hardware vm={v} onChanged={vm.reload} />}
      {v && tab === 'snapshots' && <Snapshots vm={v} />}
      {v && tab === 'backups' && can('backup.view') && <GuestBackups kind="vm" name={v.name} running={v.state !== 'shutoff'} />}
      {v && tab === 'cloudinit' && v.cloud_init && <VMCloudInit vm={v} onChanged={vm.reload} />}
      {v && tab === 'console' && can('vm.console') && (
        v.state === 'running' || v.state === 'paused' ? <Suspense fallback={<div className="faint">{t('common.loading')}</div>}><VNCConsole name={v.name} /></Suspense> : <div className="alert info">{t('vm.console_requires_running')}</div>
      )}
      {v && tab === 'notes' && <Notes vm={v} onChanged={vm.reload} />}
      {tab === 'tasks' && <div className="panel"><JobsTable target={`vm/${name}`} /></div>}
      {actions.dialogs}
    </div>
  )
}

function Summary({ vm, onChanged }: { vm: Detail; onChanged: () => void }) {
  const { t } = useTranslation()
  const { can } = useAuth()
  const stats = usePoll(() => api.get<VMStats[]>(`/vms/${encodeURIComponent(vm.name)}/stats`), vm.state === 'running' ? 5000 : null, [vm.name, vm.state])
  const guest = usePoll(() => api.get<GuestInfo>(`/vms/${encodeURIComponent(vm.name)}/guest`), vm.state === 'running' ? 15000 : null, [vm.name, vm.state])
  const [editRes, setEditRes] = useState(false)
  const s = stats.data ?? []
  const last = s[s.length - 1]

  return (
    <div className="stack">
      <div className="grid grid-4">
        <div className="panel metric">
          <div className="label">{t('vm.cpu_usage')}</div>
          <div className="value">{vm.state === 'running' ? formatPercent(last?.cpu_usage) : '–'}</div>
          <div className="detail">{t('vm.vcpus_n', { n: vm.vcpus })}</div>
          <Sparkline values={s.map((x) => x.cpu_usage)} max={100} />
        </div>
        <div className="panel metric">
          <div className="label">{t('vm.memory')}</div>
          <div className="value">{last?.memory_used_kib != null ? formatBytes(last.memory_used_kib * 1024) : formatMiB(vm.memory_mib)}</div>
          <div className="detail">{last?.memory_used_kib != null ? t('vm.memory_guest_used', { total: formatMiB(vm.memory_mib) }) : t('vm.memory_assigned')}</div>
          <Sparkline values={s.map((x) => (x.memory_used_kib != null ? x.memory_used_kib : null))} max={vm.memory_mib * 1024} />
        </div>
        <div className="panel metric">
          <div className="label">{t('vm.network_io')}</div>
          <div className="value">{formatRate(last?.net_rx_bps)}</div>
          <div className="detail">{t('vm.rx_tx', { tx: formatRate(last?.net_tx_bps) })}</div>
          <Sparkline values={s.map((x) => (x.net_rx_bps ?? 0) + (x.net_tx_bps ?? 0))} />
        </div>
        <div className="panel metric">
          <div className="label">{t('vm.disk_io')}</div>
          <div className="value">{formatRate(last?.disk_read_bps)}</div>
          <div className="detail">{t('vm.read_write', { w: formatRate(last?.disk_write_bps) })}</div>
          <Sparkline values={s.map((x) => (x.disk_read_bps ?? 0) + (x.disk_write_bps ?? 0))} />
        </div>
      </div>
      <div className="grid grid-2">
        <div className="panel">
          <div className="panel-head">
            <h2>{t('vm.resources')}</h2>
            <div className="actions">
              {can('vm.config') && <button className="btn sm" onClick={() => setEditRes(true)}>{t('common.edit')}</button>}
            </div>
          </div>
          <div className="panel-body">
            <dl className="kv">
              <dt>{t('vm.vcpus')}</dt><dd>{vm.vcpus}</dd>
              <dt>{t('vm.memory')}</dt><dd>{formatMiB(vm.memory_mib)}</dd>
              <dt>{t('vm.cpu_mode')}</dt><dd>{vm.cpu_mode || t('vm.cpu_default')}</dd>
              <dt>{t('vm.autostart')}</dt><dd>{vm.autostart ? t('common.yes') : t('common.no')}</dd>
              <dt>UUID</dt><dd className="mono">{vm.uuid} <Copy value={vm.uuid} /></dd>
            </dl>
          </div>
        </div>
        <div className="panel">
          <div className="panel-head"><h2>{t('vm.guest')}</h2></div>
          <div className="panel-body">
            {vm.state !== 'running' ? (
              <span className="faint">{t('vm.guest_not_running')}</span>
            ) : !vm.guest_agent_channel ? (
              <span className="faint">{t('vm.guest_no_channel')}</span>
            ) : !guest.data ? (
              <span className="faint">{t('common.loading')}</span>
            ) : !guest.data.available ? (
              <span className="faint">{t('vm.guest_unavailable')}</span>
            ) : (
              <table className="data">
                <thead><tr><th>{t('vm.interface')}</th><th>MAC</th><th>{t('vm.addresses')}</th></tr></thead>
                <tbody>
                  {guest.data.interfaces?.map((i) => (
                    <tr key={i.name}>
                      <td>{i.name}</td>
                      <td className="mono">{i.mac}</td>
                      <td className="mono">{i.addresses.map((a) => <div key={a}>{a} <Copy value={a.split('/')[0]} /></div>)}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            )}
          </div>
        </div>
      </div>
      {vm.description && <div className="panel"><div className="panel-body muted">{vm.description}</div></div>}
      {editRes && <ResourcesDialog vm={vm} onClose={() => setEditRes(false)} onSaved={onChanged} />}
    </div>
  )
}

function ResourcesDialog({ vm, onClose, onSaved }: { vm: Detail; onClose: () => void; onSaved: () => void }) {
  const { t } = useTranslation()
  const toast = useToast()
  const [vcpus, setVcpus] = useState(vm.vcpus)
  const [mem, setMem] = useState(vm.memory_mib)
  const [err, setErr] = useState<ApiError>()
  const [busy, setBusy] = useState(false)
  const valid = vcpus >= 1 && vcpus <= 512 && mem >= 64
  const save = async () => {
    setBusy(true)
    try {
      const r = await api.put<{ pending_restart: boolean }>(`/vms/${encodeURIComponent(vm.name)}/resources`, { vcpus, memory_mib: mem })
      toast('ok', r.pending_restart ? t('vm.saved_restart') : t('common.saved'))
      onSaved()
      onClose()
    } catch (e) {
      setErr(e as ApiError)
      setBusy(false)
    }
  }
  return (
    <Dialog title={t('vm.edit_resources')} onClose={onClose} footer={<><button className="btn" onClick={onClose}>{t('common.cancel')}</button><button className="btn primary" disabled={!valid || busy} onClick={save}>{t('common.save')}</button></>}>
      {vm.state !== 'shutoff' && <div className="alert info">{t('vm.resources_next_start')}</div>}
      <div className="form-grid">
        <Field label={t('vm.vcpus')} htmlFor="r-cpu" error={vcpus >= 1 && vcpus <= 512 ? undefined : t('validation.range', { min: 1, max: 512 })}>
          <input id="r-cpu" className="input" type="number" min={1} max={512} value={vcpus} onChange={(e) => setVcpus(Number(e.target.value))} />
        </Field>
        <Field label={t('vm.memory_mib')} htmlFor="r-mem" hint={formatMiB(mem)} error={mem >= 64 ? undefined : t('validation.min', { min: 64 })}>
          <input id="r-mem" className="input" type="number" min={64} step={256} value={mem} onChange={(e) => setMem(Number(e.target.value))} />
        </Field>
      </div>
      <ErrorAlert error={err} />
    </Dialog>
  )
}

function Notes({ vm, onChanged }: { vm: Detail; onChanged: () => void }) {
  const { t } = useTranslation()
  const { can } = useAuth()
  const toast = useToast()
  const errText = useErrorText()
  const { refreshAll } = useResources()
  const [desc, setDesc] = useState(vm.description)
  const [notes, setNotes] = useState(vm.notes)
  const [tags, setTags] = useState(vm.tags.join(', '))
  const [autostart, setAutostart] = useState(vm.autostart)
  useEffect(() => {
    setDesc(vm.description)
    setNotes(vm.notes)
    setTags(vm.tags.join(', '))
    setAutostart(vm.autostart)
    // Only reset when another VM is shown.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [vm.uuid])
  const editable = can('vm.config')
  const save = async () => {
    try {
      await api.put(`/vms/${encodeURIComponent(vm.name)}/meta`, {
        description: desc, notes, autostart,
        tags: tags.split(',').map((s) => s.trim()).filter(Boolean),
      })
      toast('ok', t('common.saved'))
      onChanged()
      refreshAll()
    } catch (e) {
      toast('err', errText(e))
    }
  }
  const setTemplate = async (tpl: boolean) => {
    try {
      await api.put(`/vms/${encodeURIComponent(vm.name)}/template`, { template: tpl })
      toast('ok', t('common.saved'))
      onChanged()
      refreshAll()
    } catch (e) {
      toast('err', errText(e))
    }
  }
  return (
    <div className="stack">
      <div className="panel">
        <div className="panel-body">
          <div className="form-grid">
            <Field label={t('vm.description')} className="full" htmlFor="n-desc">
              <input id="n-desc" className="input" value={desc} onChange={(e) => setDesc(e.target.value)} disabled={!editable} />
            </Field>
            <Field label={t('vm.tags')} hint={t('vm.tags_hint')} htmlFor="n-tags">
              <input id="n-tags" className="input" value={tags} onChange={(e) => setTags(e.target.value)} disabled={!editable} />
            </Field>
            <div className="field" style={{ justifyContent: 'flex-end' }}>
              <label className="check">
                <input type="checkbox" checked={autostart} onChange={(e) => setAutostart(e.target.checked)} disabled={!editable || vm.template} />
                {t('vm.autostart')}
              </label>
            </div>
            <Field label={t('vm.notes')} className="full" htmlFor="n-notes">
              <textarea id="n-notes" className="textarea" rows={10} value={notes} onChange={(e) => setNotes(e.target.value)} disabled={!editable} />
            </Field>
          </div>
        </div>
        {editable && (
          <div className="dialog-foot">
            <button className="btn primary" onClick={save}>{t('common.save')}</button>
          </div>
        )}
      </div>
      {editable && (
        <div className="panel">
          <div className="panel-head"><h2>{t('vm.template')}</h2></div>
          <div className="panel-body row wrap">
            <span className="muted grow">{vm.template ? t('vm.template_is') : t('vm.template_hint')}</span>
            <button className="btn" disabled={!vm.template && vm.state !== 'shutoff'} onClick={() => setTemplate(!vm.template)}>
              {vm.template ? t('vm.template_unmark') : t('vm.template_mark')}
            </button>
          </div>
        </div>
      )}
    </div>
  )
}
