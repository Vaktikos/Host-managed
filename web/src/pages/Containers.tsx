import { Suspense, lazy, useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Link, useNavigate, useParams, useSearchParams } from 'react-router-dom'
import { ApiError, api } from '../api/client'
import type { Container, ContainerCreate, Interface, Job, LXCImage, VirtNetwork } from '../api/types'
import { useAuth } from '../auth'
import { DataTable, Column } from '../components/DataTable'
import { Icon } from '../components/icons'
import { ConfirmDialog, Copy, Dialog, ErrorAlert, Field, Menu, MenuItem, StatusDot, Tabs, useErrorText, useToast } from '../components/ui'
import { formatBytes, formatNumber } from '../lib/format'
import { usePoll } from '../lib/hooks'
import { useResources } from '../resources'
import { GuestBackups } from './Backups'
import { JobsTable } from './Tasks'

const Terminal = lazy(() => import('../console/Terminal').then((m) => ({ default: m.Terminal })))

function ctAllowed(state: string, action: string) {
  const on = state === 'running'
  switch (action) {
    case 'start':
      return state === 'stopped'
    case 'shutdown':
    case 'reboot':
    case 'freeze':
      return on
    case 'stop':
      return on || state === 'frozen'
    case 'unfreeze':
      return state === 'frozen'
  }
  return false
}

function useCTActions(onChanged?: () => void) {
  const { t } = useTranslation()
  const { can } = useAuth()
  const toast = useToast()
  const errText = useErrorText()
  const { refreshAll } = useResources()
  const nav = useNavigate()
  const [confirmStop, setConfirmStop] = useState<Container | null>(null)
  const [deleting, setDeleting] = useState<Container | null>(null)

  const run = async (c: Container, action: string) => {
    try {
      await api.post(`/containers/${encodeURIComponent(c.name)}/actions/${action}`)
      toast('ok', t('vm.action_sent', { action: t(`ct.action.${action}`), name: c.name }))
      refreshAll()
      onChanged?.()
    } catch (e) {
      toast('err', `${c.name}: ${errText(e)}`)
    }
  }
  const request = (c: Container, a: string) => (a === 'stop' ? setConfirmStop(c) : void run(c, a))
  const items = (c: Container): MenuItem[] => [
    ...['start', 'shutdown', 'reboot', 'freeze', 'unfreeze', 'stop'].map((a) => ({
      label: t(`ct.action.${a}`),
      onClick: () => request(c, a),
      disabled: !can('ct.power') || !ctAllowed(c.state, a),
      danger: a === 'stop',
    })),
    { label: '', onClick: () => {}, separator: true },
    { label: t('ct.open_console'), onClick: () => nav(`/containers/${encodeURIComponent(c.name)}?tab=console`), disabled: !can('ct.console') || c.state !== 'running' },
    { label: t('ct.delete'), onClick: () => setDeleting(c), disabled: !can('ct.delete') || c.state !== 'stopped', danger: true },
  ]
  const dialogs = (
    <>
      {confirmStop && (
        <ConfirmDialog
          title={t('ct.action.stop')}
          message={t('ct.confirm_stop', { name: confirmStop.name })}
          confirmLabel={t('ct.action.stop')}
          onConfirm={() => run(confirmStop, 'stop')}
          onClose={() => setConfirmStop(null)}
        />
      )}
      {deleting && (
        <ConfirmDialog
          title={t('ct.delete_title', { name: deleting.name })}
          message={t('ct.delete_text')}
          confirmLabel={t('ct.delete')}
          confirmText={deleting.name}
          onConfirm={async () => {
            const res = await api.del<{ job: Job }>(`/containers/${encodeURIComponent(deleting.name)}?confirm=${encodeURIComponent(deleting.name)}`)
            toast('info', t('tasks.started', { id: res.job.id }))
            refreshAll()
            nav('/containers')
          }}
          onClose={() => setDeleting(null)}
        />
      )}
    </>
  )
  return { request, items, dialogs }
}

export function Containers() {
  const { t } = useTranslation()
  const { cts } = useResources()
  const { can } = useAuth()
  const nav = useNavigate()
  const [creating, setCreating] = useState(false)
  const actions = useCTActions()

  const columns: Column<Container>[] = [
    { id: 'state', label: t('common.status'), render: (c) => <StatusDot state={c.state} />, sort: (c) => c.state },
    { id: 'name', label: t('common.name'), sort: (c) => c.name, search: (c) => `${c.name} ${c.ips.join(' ')} ${c.distribution ?? ''}`, render: (c) => <strong style={{ fontWeight: 500 }}>{c.name}</strong> },
    { id: 'os', label: t('ct.os'), sort: (c) => `${c.distribution} ${c.release}`, render: (c) => (c.distribution ? `${c.distribution} ${c.release}` : '–') },
    { id: 'ips', label: t('ct.ips'), sort: (c) => c.ips.join(','), render: (c) => <span className="mono">{c.ips.join(', ') || '–'}</span> },
    { id: 'memory', label: t('ct.memory_use'), num: true, sort: (c) => c.memory_usage ?? 0, render: (c) => (c.memory_usage != null ? formatBytes(c.memory_usage) : '–') },
    { id: 'limit', label: t('ct.memory_limit'), num: true, sort: (c) => c.memory_limit ?? 0, render: (c) => (c.memory_limit ? formatBytes(c.memory_limit) : t('ct.unlimited')) },
    { id: 'cpu', label: t('ct.cpu_limit'), num: true, sort: (c) => c.cpu_limit ?? 0, render: (c) => (c.cpu_limit ? formatNumber(c.cpu_limit) : t('ct.unlimited')), hidden: true },
    { id: 'priv', label: t('ct.mode'), sort: (c) => (c.unprivileged ? 0 : 1), render: (c) => (c.unprivileged ? t('ct.unprivileged') : <span className="badge warn">{t('ct.privileged')}</span>) },
    { id: 'autostart', label: t('vm.autostart'), sort: (c) => (c.autostart ? 1 : 0), render: (c) => (c.autostart ? t('common.yes') : t('common.no')), hidden: true },
  ]
  return (
    <div className="page">
      <div className="page-head">
        <h1>{t('nav.containers')}</h1>
        <div className="actions">
          {can('ct.create') && (
            <button className="btn primary" onClick={() => setCreating(true)}>
              <Icon name="plus" /> {t('ct.create')}
            </button>
          )}
        </div>
      </div>
      <ErrorAlert error={cts.error} />
      <div className="panel">
        <DataTable
          id="containers"
          columns={columns}
          rows={cts.data}
          rowKey={(c) => c.name}
          onRowClick={(c) => nav(`/containers/${encodeURIComponent(c.name)}`)}
          empty={t('ct.none')}
          rowMenu={(c) => <Menu items={actions.items(c)} label={t('common.actions')} />}
        />
      </div>
      {actions.dialogs}
      {creating && <CTCreateDialog onClose={() => setCreating(false)} />}
    </div>
  )
}

const ctNameRe = /^[a-z0-9][a-z0-9-]{0,62}$/
const cidrRe = /^(\d{1,3}\.){3}\d{1,3}\/\d{1,2}$/
const ipRe = /^(\d{1,3}\.){3}\d{1,3}$/

function CTCreateDialog({ onClose }: { onClose: () => void }) {
  const { t } = useTranslation()
  const toast = useToast()
  const nav = useNavigate()
  const { refreshAll, cts } = useResources()
  const [images, setImages] = useState<LXCImage[]>([])
  const [bridges, setBridges] = useState<string[]>([])
  const [loadErr, setLoadErr] = useState<ApiError>()
  const [err, setErr] = useState<ApiError>()
  const [busy, setBusy] = useState(false)
  const [touched, setTouched] = useState(false)
  const [f, setF] = useState<ContainerCreate>({
    name: '', distribution: 'debian', release: '', arch: 'amd64', memory_mib: 1024, swap_mib: 512, cpu_limit: 2,
    bridge: '', ipv4: '', gateway4: '', autostart: false, start: true, description: '',
  })
  const set = <K extends keyof ContainerCreate>(k: K, v: ContainerCreate[K]) => setF((x) => ({ ...x, [k]: v }))

  useEffect(() => {
    api.get<LXCImage[]>('/lxc/images?arch=amd64').then(setImages).catch((e) => setLoadErr(e))
    Promise.all([api.get<Interface[]>('/node/interfaces'), api.get<VirtNetwork[]>('/networks')])
      .then(([ifs, nets]) => {
        const names = Array.from(new Set([...ifs.filter((i) => i.kind === 'bridge').map((i) => i.name), ...nets.filter((n) => n.active && n.bridge).map((n) => n.bridge)]))
        setBridges(names)
        setF((x) => ({ ...x, bridge: x.bridge || names[0] || '' }))
      })
      .catch((e) => setLoadErr(e))
  }, [])

  const dists = useMemo(() => Array.from(new Set(images.map((i) => i.distribution))), [images])
  const releases = images.filter((i) => i.distribution === f.distribution).map((i) => i.release)
  useEffect(() => {
    if (releases.length && !releases.includes(f.release)) {
      const stable = releases.filter((r) => !['edge', 'sid', 'current', 'rolling'].includes(r))
      set('release', stable[stable.length - 1] ?? releases[0])
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [f.distribution, images])

  const errors = useMemo(() => {
    const e: Partial<Record<string, string>> = {}
    if (!ctNameRe.test(f.name)) e.name = t('validation.ct_name')
    else if (cts.data?.some((c) => c.name === f.name)) e.name = t('validation.name_taken')
    if (!f.release) e.release = t('validation.required')
    if (!f.bridge) e.bridge = t('validation.required')
    if (f.memory_mib !== 0 && !(f.memory_mib >= 32)) e.memory_mib = t('validation.min', { min: 32 })
    if (!(f.cpu_limit >= 0 && f.cpu_limit <= 512)) e.cpu_limit = t('validation.range', { min: 0, max: 512 })
    if (f.ipv4 && !cidrRe.test(f.ipv4)) e.ipv4 = t('validation.cidr')
    if (f.gateway4 && !ipRe.test(f.gateway4)) e.gateway4 = t('validation.ip')
    if (f.gateway4 && !f.ipv4) e.gateway4 = t('validation.gateway_needs_ip')
    return e
  }, [f, t, cts.data])

  const submit = async () => {
    setTouched(true)
    if (Object.keys(errors).length) return
    setBusy(true)
    try {
      const body = { ...f, ipv4: f.ipv4 || undefined, gateway4: f.gateway4 || undefined }
      const res = await api.post<{ job: Job }>('/containers', body)
      toast('info', t('tasks.started', { id: res.job.id }))
      refreshAll()
      onClose()
      nav(`/tasks/${res.job.id}`)
    } catch (e) {
      setErr(e as ApiError)
      setBusy(false)
    }
  }
  const fe = (k: string) => (touched ? errors[k] : undefined) ?? (err?.field === k ? err.message : undefined)

  return (
    <Dialog
      title={t('ct.create')}
      onClose={onClose}
      wide
      footer={<><button className="btn" onClick={onClose}>{t('common.cancel')}</button><button className="btn primary" disabled={busy} onClick={submit}>{busy ? t('common.working') : t('ct.create')}</button></>}
    >
      <ErrorAlert error={loadErr} title={t('ct.images_unavailable')} />
      <div className="form-grid">
        <Field label={t('common.name')} error={fe('name')} hint={t('ct.name_hint')} htmlFor="ct-name">
          <input id="ct-name" className="input" value={f.name} onChange={(e) => set('name', e.target.value)} autoComplete="off" />
        </Field>
        <Field label={t('vm.description')} htmlFor="ct-desc">
          <input id="ct-desc" className="input" value={f.description} onChange={(e) => set('description', e.target.value)} />
        </Field>
        <div className="legend">{t('ct.section_image')}</div>
        <Field label={t('ct.distribution')} htmlFor="ct-dist">
          <select id="ct-dist" className="select" value={f.distribution} onChange={(e) => set('distribution', e.target.value)}>
            {dists.length === 0 && <option value={f.distribution}>{f.distribution}</option>}
            {dists.map((d) => <option key={d} value={d}>{d}</option>)}
          </select>
        </Field>
        <Field label={t('ct.release')} error={fe('release')} htmlFor="ct-rel">
          <select id="ct-rel" className="select" value={f.release} onChange={(e) => set('release', e.target.value)}>
            {releases.map((r) => <option key={r} value={r}>{r}</option>)}
          </select>
        </Field>
        <div className="full faint">{t('ct.image_hint')}</div>
        <div className="legend">{t('ct.section_resources')}</div>
        <Field label={t('ct.memory_mib')} error={fe('memory_mib')} hint={t('ct.zero_unlimited')} htmlFor="ct-mem">
          <input id="ct-mem" className="input" type="number" min={0} step={128} value={f.memory_mib} onChange={(e) => set('memory_mib', Number(e.target.value))} />
        </Field>
        <Field label={t('ct.swap_mib')} htmlFor="ct-swap">
          <input id="ct-swap" className="input" type="number" min={0} step={128} value={f.swap_mib} onChange={(e) => set('swap_mib', Number(e.target.value))} />
        </Field>
        <Field label={t('ct.cpu_cores')} error={fe('cpu_limit')} hint={t('ct.zero_unlimited')} htmlFor="ct-cpu">
          <input id="ct-cpu" className="input" type="number" min={0} step={0.5} value={f.cpu_limit} onChange={(e) => set('cpu_limit', Number(e.target.value))} />
        </Field>
        <div />
        <div className="legend">{t('vm.section_network')}</div>
        <Field label={t('ct.bridge')} error={fe('bridge')} htmlFor="ct-br">
          <select id="ct-br" className="select" value={f.bridge} onChange={(e) => set('bridge', e.target.value)}>
            <option value="">{t('common.select')}</option>
            {bridges.map((b) => <option key={b} value={b}>{b}</option>)}
          </select>
        </Field>
        <div />
        <Field label={t('ct.ipv4')} error={fe('ipv4')} hint={t('ct.ipv4_hint')} htmlFor="ct-ip">
          <input id="ct-ip" className="input mono" value={f.ipv4} onChange={(e) => set('ipv4', e.target.value)} placeholder="192.168.122.50/24" />
        </Field>
        <Field label={t('ct.gateway')} error={fe('gateway4')} htmlFor="ct-gw">
          <input id="ct-gw" className="input mono" value={f.gateway4} onChange={(e) => set('gateway4', e.target.value)} placeholder="192.168.122.1" />
        </Field>
        <div className="full row wrap" style={{ gap: 18 }}>
          <label className="check"><input type="checkbox" checked={f.autostart} onChange={(e) => set('autostart', e.target.checked)} />{t('vm.autostart')}</label>
          <label className="check"><input type="checkbox" checked={f.start} onChange={(e) => set('start', e.target.checked)} />{t('vm.start_after_create')}</label>
        </div>
        <div className="full alert info">{t('ct.unprivileged_hint')}</div>
      </div>
      {err && !err.field && <ErrorAlert error={err} />}
    </Dialog>
  )
}

export function ContainerDetail() {
  const { t } = useTranslation()
  const { name = '' } = useParams()
  const [params, setParams] = useSearchParams()
  const tab = params.get('tab') ?? 'summary'
  const { can } = useAuth()
  const ct = usePoll(() => api.get<Container>(`/containers/${encodeURIComponent(name)}`), 4000, [name])
  const actions = useCTActions(ct.reload)
  const c = ct.data
  if (ct.error?.status === 404) {
    return <div className="page"><div className="alert warn">{t('ct.not_found', { name })} <Link to="/containers">{t('nav.containers')}</Link></div></div>
  }
  const tabs = [
    { id: 'summary', label: t('vm.tab.summary') },
    ...(can('ct.console') ? [{ id: 'console', label: t('vm.tab.console') }] : []),
    ...(can('backup.view') ? [{ id: 'backups', label: t('vm.tab.backups') }] : []),
    { id: 'tasks', label: t('vm.tab.tasks') },
  ]
  const power = (a: string, icon: string) =>
    can('ct.power') && (
      <button className="btn" disabled={!c || !ctAllowed(c.state, a)} onClick={() => c && actions.request(c, a)}>
        <Icon name={icon} size={14} /> {t(`ct.action.${a}`)}
      </button>
    )
  return (
    <div className="page">
      <div className="page-head">
        <div>
          <div className="crumbs"><Link to="/containers">{t('nav.containers')}</Link> / {name}</div>
          <div className="row"><h1>{name}</h1>{c && <StatusDot state={c.state} />}</div>
        </div>
        <div className="actions">
          {power('start', 'play')}
          {power('shutdown', 'power')}
          {power('reboot', 'refresh')}
          {c && <Menu items={actions.items(c)} label={t('common.actions')} />}
        </div>
      </div>
      <ErrorAlert error={ct.error} />
      <Tabs tabs={tabs} active={tab} onChange={(id) => setParams({ tab: id })} />
      {c && tab === 'summary' && <CTSummary ct={c} onChanged={ct.reload} />}
      {c && tab === 'console' && (c.state === 'running' ? <Suspense fallback={<div className="faint">{t('common.loading')}</div>}><Terminal path={`/containers/${encodeURIComponent(c.name)}/console`} /></Suspense> : <div className="alert info">{t('ct.console_requires_running')}</div>)}
      {c && tab === 'backups' && can('backup.view') && <GuestBackups kind="ct" name={c.name} running={c.state === 'running'} />}
      {tab === 'tasks' && <div className="panel"><JobsTable target={`ct/${name}`} /></div>}
      {actions.dialogs}
    </div>
  )
}

function CTSummary({ ct, onChanged }: { ct: Container; onChanged: () => void }) {
  const { t } = useTranslation()
  const { can } = useAuth()
  const [edit, setEdit] = useState(false)
  return (
    <div className="grid grid-2">
      <div className="panel">
        <div className="panel-head">
          <h2>{t('vm.resources')}</h2>
          <div className="actions">{can('ct.config') && <button className="btn sm" onClick={() => setEdit(true)}>{t('common.edit')}</button>}</div>
        </div>
        <div className="panel-body">
          <dl className="kv">
            <dt>{t('ct.memory_limit')}</dt><dd>{ct.memory_limit ? formatBytes(ct.memory_limit) : t('ct.unlimited')}</dd>
            <dt>{t('ct.memory_use')}</dt><dd>{ct.memory_usage != null ? formatBytes(ct.memory_usage) : '–'}</dd>
            <dt>{t('ct.cpu_limit')}</dt><dd>{ct.cpu_limit ? t('ct.cores', { n: formatNumber(ct.cpu_limit) }) : t('ct.unlimited')}</dd>
            <dt>{t('vm.autostart')}</dt><dd>{ct.autostart ? t('common.yes') : t('common.no')}</dd>
          </dl>
        </div>
      </div>
      <div className="panel">
        <div className="panel-head"><h2>{t('ct.system')}</h2></div>
        <div className="panel-body">
          <dl className="kv">
            <dt>{t('ct.os')}</dt><dd>{ct.distribution ? `${ct.distribution} ${ct.release}` : '–'}</dd>
            <dt>{t('ct.hostname')}</dt><dd className="mono">{ct.hostname || '–'}</dd>
            <dt>{t('ct.mode')}</dt><dd>{ct.unprivileged ? t('ct.unprivileged') : t('ct.privileged')}</dd>
            <dt>{t('ct.ips')}</dt><dd className="mono">{ct.ips.length ? ct.ips.map((ip) => <div key={ip}>{ip} <Copy value={ip} /></div>) : '–'}</dd>
            <dt>PID</dt><dd className="mono">{ct.pid ?? '–'}</dd>
            {ct.description && (<><dt>{t('vm.description')}</dt><dd>{ct.description}</dd></>)}
          </dl>
        </div>
      </div>
      {edit && <CTConfigDialog ct={ct} onClose={() => setEdit(false)} onSaved={onChanged} />}
    </div>
  )
}

function CTConfigDialog({ ct, onClose, onSaved }: { ct: Container; onClose: () => void; onSaved: () => void }) {
  const { t } = useTranslation()
  const toast = useToast()
  const [mem, setMem] = useState(ct.memory_limit ? Math.round(ct.memory_limit / 1048576) : 0)
  const [swap, setSwap] = useState(0)
  const [cpu, setCpu] = useState(ct.cpu_limit ?? 0)
  const [autostart, setAutostart] = useState(ct.autostart)
  const [desc, setDesc] = useState(ct.description ?? '')
  const [err, setErr] = useState<ApiError>()
  const valid = (mem === 0 || mem >= 32) && cpu >= 0 && cpu <= 512
  const save = async () => {
    try {
      await api.put(`/containers/${encodeURIComponent(ct.name)}/config`, { memory_mib: mem, swap_mib: swap, cpu_limit: cpu, autostart, description: desc })
      toast('ok', ct.state === 'running' ? t('ct.saved_restart') : t('common.saved'))
      onSaved()
      onClose()
    } catch (e) {
      setErr(e as ApiError)
    }
  }
  return (
    <Dialog title={t('ct.edit_resources')} onClose={onClose} footer={<><button className="btn" onClick={onClose}>{t('common.cancel')}</button><button className="btn primary" disabled={!valid} onClick={save}>{t('common.save')}</button></>}>
      <div className="form-grid">
        <Field label={t('ct.memory_mib')} hint={t('ct.zero_unlimited')} htmlFor="cc-mem"><input id="cc-mem" className="input" type="number" min={0} value={mem} onChange={(e) => setMem(Number(e.target.value))} /></Field>
        <Field label={t('ct.swap_mib')} htmlFor="cc-swap"><input id="cc-swap" className="input" type="number" min={0} value={swap} onChange={(e) => setSwap(Number(e.target.value))} /></Field>
        <Field label={t('ct.cpu_cores')} hint={t('ct.zero_unlimited')} htmlFor="cc-cpu"><input id="cc-cpu" className="input" type="number" min={0} step={0.5} value={cpu} onChange={(e) => setCpu(Number(e.target.value))} /></Field>
        <div className="field" style={{ justifyContent: 'flex-end' }}><label className="check"><input type="checkbox" checked={autostart} onChange={(e) => setAutostart(e.target.checked)} />{t('vm.autostart')}</label></div>
        <Field label={t('vm.description')} className="full" htmlFor="cc-desc"><input id="cc-desc" className="input" value={desc} onChange={(e) => setDesc(e.target.value)} /></Field>
      </div>
      <ErrorAlert error={err} />
    </Dialog>
  )
}
