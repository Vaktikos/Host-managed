import { Fragment, ReactNode, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { useNavigate, useSearchParams } from 'react-router-dom'
import { ApiError, api } from '../api/client'
import type { Job, StoragePool } from '../api/types'
import { useAuth } from '../auth'
import { DataTable, Column } from '../components/DataTable'
import { Icon } from '../components/icons'
import { ConfirmDialog, Dialog, ErrorAlert, Field, Menu, StatusDot, Tabs, useErrorText, useToast } from '../components/ui'
import { formatBytes, formatDate, formatDuration } from '../lib/format'
import { usePoll } from '../lib/hooks'

export interface Backup {
  id: string
  kind: 'vm' | 'ct'
  name: string
  created: string
  host: string
  software: string
  mode: string
  consistency: string
  size: number
  notes: string
  protected: boolean
  duration_seconds: number
  store: string
  disks?: { target: string; capacity: number }[]
}

export interface Keep {
  keep_last: number
  keep_hourly: number
  keep_daily: number
  keep_weekly: number
  keep_monthly: number
  keep_yearly: number
}

export interface Schedule {
  id: number
  name: string
  enabled: boolean
  store: string
  days: number
  at: string
  selection: 'all' | 'vms' | 'containers' | 'list'
  targets: string[]
  mode: string
  keep: Keep
  notes: string
  last_run: string | null
  last_job: string
  next_run: string | null
  running: boolean
}

const keepFields: (keyof Keep)[] = ['keep_last', 'keep_hourly', 'keep_daily', 'keep_weekly', 'keep_monthly', 'keep_yearly']
const emptyKeep: Keep = { keep_last: 0, keep_hourly: 0, keep_daily: 0, keep_weekly: 0, keep_monthly: 0, keep_yearly: 0 }
const nameRe = /^[a-z0-9][a-z0-9-]{0,62}$/
const vmNameRe = /^[a-zA-Z0-9][a-zA-Z0-9._-]{0,62}$/

export const backupStoresPath = '/backup-stores'

function useStartJob() {
  const { t } = useTranslation()
  const toast = useToast()
  const nav = useNavigate()
  return async (p: Promise<{ job: Job }>) => {
    const r = await p
    toast('info', t('tasks.started', { id: r.job.id }))
    nav(`/tasks/${r.job.id}`)
  }
}

export function ConsistencyBadge({ b }: { b: Pick<Backup, 'consistency'> }) {
  const { t } = useTranslation()
  const cls = b.consistency === 'none' ? 'badge warn' : 'badge'
  return <span className={cls} title={t(`backup.consistency_hint.${b.consistency}`)}>{t(`backup.consistency.${b.consistency}`)}</span>
}

export function Backups() {
  const { t } = useTranslation()
  const { can } = useAuth()
  const [params, setParams] = useSearchParams()
  const tab = params.get('tab') ?? 'backups'
  const stores = usePoll(() => api.get<StoragePool[]>(backupStoresPath), 30000)
  const tabs = [
    { id: 'backups', label: t('backup.tab.backups') },
    { id: 'schedules', label: t('backup.tab.schedules') },
    { id: 'stores', label: t('backup.tab.stores') },
  ]
  return (
    <div className="page">
      <div className="page-head">
        <h1>{t('nav.backups')}</h1>
        <span className="sub">{t('backup.subtitle')}</span>
      </div>
      <ErrorAlert error={stores.error} />
      {stores.data?.length === 0 && <div className="alert warn" style={{ marginBottom: 12 }}>{t('backup.no_store')}</div>}
      <Tabs tabs={tabs} active={tab} onChange={(id) => setParams({ tab: id })} />
      {tab === 'backups' && <BackupList stores={stores.data ?? []} />}
      {tab === 'schedules' && <Schedules stores={stores.data ?? []} editable={can('backup.schedule')} />}
      {tab === 'stores' && <Stores stores={stores.data} />}
    </div>
  )
}

function Stores({ stores }: { stores: StoragePool[] | undefined }) {
  const { t } = useTranslation()
  return (
    <div className="panel">
      <table className="data">
        <thead><tr><th>{t('common.name')}</th><th>{t('common.status')}</th><th>{t('storage.type')}</th><th>{t('storage.path')}</th><th className="num">{t('storage.used')}</th><th className="num">{t('backup.available')}</th></tr></thead>
        <tbody>
          {stores?.map((s) => (
            <tr key={s.name}>
              <td className="mono">{s.name}</td>
              <td><StatusDot state={s.active ? 'active' : 'inactive'} label={s.active ? t('common.active') : t('common.inactive')} /></td>
              <td>{s.type}</td>
              <td className="mono faint">{s.path}</td>
              <td className="num">{formatBytes(s.allocation)}</td>
              <td className="num">{formatBytes(s.available)}</td>
            </tr>
          ))}
        </tbody>
      </table>
      <div className="panel-body faint">{t('backup.store_hint')}</div>
    </div>
  )
}

// BackupList shows backups of all stores, or of one guest when kind/name are set.
export function BackupList({ stores, kind, name }: { stores: StoragePool[]; kind?: 'vm' | 'ct'; name?: string }) {
  const { t } = useTranslation()
  const { can } = useAuth()
  const toast = useToast()
  const errText = useErrorText()
  const [store, setStore] = useState('')
  const q = new URLSearchParams()
  if (store) q.set('store', store)
  if (kind && name) {
    q.set('kind', kind)
    q.set('name', name)
  }
  const list = usePoll(() => api.get<Backup[]>(`/backups?${q}`), 15000, [store, kind, name])
  const [restore, setRestore] = useState<Backup | null>(null)
  const [del, setDel] = useState<Backup | null>(null)
  const [info, setInfo] = useState<Backup | null>(null)
  const protect = async (b: Backup) => {
    try {
      await api.put('/backups/protected', { store: b.store, id: b.id, protected: !b.protected })
      list.reload()
    } catch (e) {
      toast('err', errText(e))
    }
  }
  const columns: Column<Backup>[] = [
    ...(kind ? [] : [{ id: 'guest', label: t('backup.guest'), sort: (b: Backup) => `${b.kind}/${b.name}`, search: (b: Backup) => b.name, render: (b: Backup) => <span className="row" style={{ gap: 6 }}><Icon name={b.kind === 'vm' ? 'vm' : 'container'} size={13} /> {b.name}</span> }]),
    { id: 'created', label: t('backup.created'), sort: (b) => b.created, render: (b) => formatDate(b.created) },
    { id: 'mode', label: t('backup.mode'), sort: (b) => b.mode, render: (b) => t(`backup.modes.${b.mode}`) },
    { id: 'consistency', label: t('backup.consistency_label'), sort: (b) => b.consistency, render: (b) => <ConsistencyBadge b={b} /> },
    { id: 'size', label: t('backup.size'), num: true, sort: (b) => b.size, render: (b) => formatBytes(b.size) },
    { id: 'store', label: t('backup.store'), sort: (b) => b.store, render: (b) => <span className="mono">{b.store}</span>, hidden: !!kind },
    { id: 'protected', label: t('backup.protected'), sort: (b) => (b.protected ? 1 : 0), render: (b) => (b.protected ? <span className="badge">{t('backup.protected')}</span> : '') },
    { id: 'notes', label: t('backup.notes'), search: (b) => b.notes, render: (b) => <span className="faint truncate">{b.notes}</span> },
  ]
  return (
    <div className="panel">
      <ErrorAlert error={list.error} />
      <DataTable
        id={kind ? 'guest-backups' : 'backups'}
        columns={columns}
        rows={list.data}
        rowKey={(b) => b.store + ':' + b.id}
        onRowClick={(b) => setInfo(b)}
        empty={t('backup.none')}
        tools={stores.length > 1 && (
          <select className="select" style={{ width: 160 }} value={store} onChange={(e) => setStore(e.target.value)} aria-label={t('backup.store')}>
            <option value="">{t('backup.all_stores')}</option>
            {stores.map((s) => <option key={s.name} value={s.name}>{s.name}</option>)}
          </select>
        )}
        rowMenu={(b) => (
          <Menu
            label={t('common.actions')}
            items={[
              { label: t('backup.details'), onClick: () => setInfo(b) },
              { label: t('backup.restore'), onClick: () => setRestore(b), disabled: !can('backup.restore') },
              { label: b.protected ? t('backup.unprotect') : t('backup.protect'), onClick: () => protect(b), disabled: !can('backup.delete') },
              { label: t('common.delete'), onClick: () => setDel(b), danger: true, disabled: !can('backup.delete') || b.protected },
            ]}
          />
        )}
      />
      {restore && <RestoreDialog backup={restore} onClose={() => setRestore(null)} />}
      {info && <BackupInfo backup={info} onClose={() => setInfo(null)} />}
      {del && (
        <ConfirmDialog
          title={t('backup.delete_title')}
          message={t('backup.delete_text', { id: del.id })}
          confirmLabel={t('common.delete')}
          onConfirm={async () => {
            await api.del(`/backups?${new URLSearchParams({ store: del.store, id: del.id })}`)
            list.reload()
          }}
          onClose={() => setDel(null)}
        />
      )}
    </div>
  )
}

function BackupInfo({ backup: b, onClose }: { backup: Backup; onClose: () => void }) {
  const { t } = useTranslation()
  const rows: [string, ReactNode][] = [
    [t('backup.id'), <span className="mono">{b.id}</span>],
    [t('backup.store'), b.store],
    [t('backup.created'), formatDate(b.created)],
    [t('backup.mode'), t(`backup.modes.${b.mode}`)],
    [t('backup.consistency_label'), <><ConsistencyBadge b={b} /> <span className="faint">{t(`backup.consistency_hint.${b.consistency}`)}</span></>],
    [t('backup.size'), formatBytes(b.size)],
    [t('backup.duration'), formatDuration(b.duration_seconds)],
    [t('backup.host'), b.host],
    [t('backup.software'), b.software],
    [t('backup.notes'), b.notes || '—'],
  ]
  if (b.disks?.length) rows.splice(6, 0, [t('backup.disks'), b.disks.map((d) => `${d.target} (${formatBytes(d.capacity)})`).join(', ')])
  return (
    <Dialog title={`${b.kind === 'vm' ? 'VM' : 'CT'} ${b.name}`} onClose={onClose} footer={<button className="btn" onClick={onClose}>{t('common.close')}</button>}>
      <dl className="kv">{rows.map(([k, v]) => <Fragment key={k}><dt>{k}</dt><dd>{v}</dd></Fragment>)}</dl>
    </Dialog>
  )
}

function RestoreDialog({ backup: b, onClose }: { backup: Backup; onClose: () => void }) {
  const { t } = useTranslation()
  const start = useStartJob()
  const pools = usePoll(() => api.get<StoragePool[]>('/storage'), null)
  const [name, setName] = useState(b.name)
  const [pool, setPool] = useState('local')
  const [run, setRun] = useState(false)
  const [err, setErr] = useState<ApiError>()
  const targetPools = (pools.data ?? []).filter((p) => p.content === 'images' && p.active)
  const valid = b.kind === 'vm' ? vmNameRe.test(name) && !!pool : nameRe.test(name)
  const submit = async () => {
    try {
      await start(api.post<{ job: Job }>('/backups/restore', { store: b.store, id: b.id, name, pool: b.kind === 'vm' ? pool : '', start: run }))
      onClose()
    } catch (e) {
      setErr(e as ApiError)
    }
  }
  return (
    <Dialog title={t('backup.restore_title', { name: b.name })} onClose={onClose} footer={<><button className="btn" onClick={onClose}>{t('common.cancel')}</button><button className="btn primary" disabled={!valid} onClick={submit}>{t('backup.restore')}</button></>}>
      <div className="faint">{formatDate(b.created)} · {t(`backup.modes.${b.mode}`)} · {formatBytes(b.size)}</div>
      <Field label={t('backup.restore_name')} hint={t('backup.restore_name_hint')} error={err?.field === 'name' ? err.message : !valid && name ? t(b.kind === 'vm' ? 'validation.vm_name' : 'validation.ct_name') : undefined} htmlFor="rs-name">
        <input id="rs-name" className="input mono" value={name} onChange={(e) => setName(e.target.value)} />
      </Field>
      {b.kind === 'vm' && (
        <Field label={t('backup.restore_pool')} error={err?.field === 'pool' ? err.message : undefined} htmlFor="rs-pool">
          <select id="rs-pool" className="select" value={pool} onChange={(e) => setPool(e.target.value)}>
            {targetPools.map((p) => <option key={p.name} value={p.name}>{p.name} ({formatBytes(p.available)} {t('backup.free')})</option>)}
          </select>
        </Field>
      )}
      <label className="check"><input type="checkbox" checked={run} onChange={(e) => setRun(e.target.checked)} /> {t('backup.start_after')}</label>
      <ErrorAlert error={err && !err.field ? err : undefined} />
    </Dialog>
  )
}

// BackupNow starts a backup of one guest.
export function BackupNowDialog({ kind, name, running, onClose }: { kind: 'vm' | 'ct'; name: string; running: boolean; onClose: () => void }) {
  const { t } = useTranslation()
  const start = useStartJob()
  const stores = usePoll(() => api.get<StoragePool[]>(backupStoresPath), null)
  const active = (stores.data ?? []).filter((s) => s.active)
  const [store, setStore] = useState('')
  const [notes, setNotes] = useState('')
  const [mode, setMode] = useState('stop')
  const [err, setErr] = useState<ApiError>()
  const chosen = store || active[0]?.name || ''
  const submit = async () => {
    try {
      await start(api.post<{ job: Job }>(`/${kind === 'vm' ? 'vms' : 'containers'}/${encodeURIComponent(name)}/backup`, { store: chosen, notes, mode: kind === 'ct' ? mode : '' }))
      onClose()
    } catch (e) {
      setErr(e as ApiError)
    }
  }
  return (
    <Dialog title={t('backup.now_title', { name })} onClose={onClose} footer={<><button className="btn" onClick={onClose}>{t('common.cancel')}</button><button className="btn primary" disabled={!chosen} onClick={submit}>{t('backup.start')}</button></>}>
      {stores.data && active.length === 0 && <div className="alert warn">{t('backup.no_store')}</div>}
      <Field label={t('backup.store')} htmlFor="bn-store">
        <select id="bn-store" className="select" value={chosen} onChange={(e) => setStore(e.target.value)}>
          {active.map((s) => <option key={s.name} value={s.name}>{s.name} ({formatBytes(s.available)} {t('backup.free')})</option>)}
        </select>
      </Field>
      {kind === 'ct' && (
        <Field label={t('backup.mode')} hint={t(`backup.mode_hint.${mode}`)} htmlFor="bn-mode">
          <select id="bn-mode" className="select" value={mode} onChange={(e) => setMode(e.target.value)}>
            {['stop', 'suspend', 'live'].map((m) => <option key={m} value={m}>{t(`backup.modes.${m}`)}</option>)}
          </select>
        </Field>
      )}
      {kind === 'vm' && <div className="faint">{running ? t('backup.vm_running_hint') : t('backup.vm_offline_hint')}</div>}
      <Field label={t('backup.notes')} htmlFor="bn-notes">
        <input id="bn-notes" className="input" value={notes} maxLength={1024} onChange={(e) => setNotes(e.target.value)} />
      </Field>
      <ErrorAlert error={err} />
    </Dialog>
  )
}

// GuestBackups is the backup tab of a VM or container.
export function GuestBackups({ kind, name, running }: { kind: 'vm' | 'ct'; name: string; running: boolean }) {
  const { t } = useTranslation()
  const { can } = useAuth()
  const stores = usePoll(() => api.get<StoragePool[]>(backupStoresPath), null)
  const [now, setNow] = useState(false)
  const [prune, setPrune] = useState(false)
  return (
    <div className="stack">
      <div className="row">
        <div className="grow" />
        {can('backup.delete') && <button className="btn" onClick={() => setPrune(true)} disabled={!stores.data?.length}>{t('backup.prune')}</button>}
        {can('backup.create') && <button className="btn primary" onClick={() => setNow(true)}><Icon name="plus" size={12} /> {t('backup.now')}</button>}
      </div>
      <BackupList stores={stores.data ?? []} kind={kind} name={name} />
      {now && <BackupNowDialog kind={kind} name={name} running={running} onClose={() => setNow(false)} />}
      {prune && <PruneDialog kind={kind} name={name} stores={stores.data ?? []} onClose={() => setPrune(false)} />}
    </div>
  )
}

function KeepFields({ keep, onChange }: { keep: Keep; onChange: (k: Keep) => void }) {
  const { t } = useTranslation()
  return (
    <div className="form-grid three">
      {keepFields.map((f) => (
        <Field key={f} label={t(`backup.keep.${f}`)} htmlFor={`k-${f}`}>
          <input id={`k-${f}`} className="input" type="number" min={0} max={10000} value={keep[f]} onChange={(e) => onChange({ ...keep, [f]: Math.max(0, Number(e.target.value) || 0) })} />
        </Field>
      ))}
    </div>
  )
}

function PruneDialog({ kind, name, stores, onClose }: { kind: 'vm' | 'ct'; name: string; stores: StoragePool[]; onClose: () => void }) {
  const { t } = useTranslation()
  const toast = useToast()
  const [store, setStore] = useState(stores[0]?.name ?? '')
  const [keep, setKeep] = useState<Keep>({ ...emptyKeep, keep_last: 3 })
  const [preview, setPreview] = useState<{ kept: string[]; removed: string[] } | null>(null)
  const [err, setErr] = useState<ApiError>()
  const call = async (dry: boolean) => {
    setErr(undefined)
    try {
      const r = await api.post<{ kept: string[]; removed: string[] }>('/backups/prune', { store, kind, name, keep, dry_run: dry })
      if (dry) setPreview(r)
      else {
        toast('ok', t('backup.pruned', { count: r.removed.length }))
        onClose()
      }
    } catch (e) {
      setErr(e as ApiError)
    }
  }
  const empty = keepFields.every((f) => keep[f] === 0)
  return (
    <Dialog
      title={t('backup.prune_title', { name })}
      onClose={onClose}
      wide
      footer={<><button className="btn" onClick={onClose}>{t('common.cancel')}</button><button className="btn" disabled={empty} onClick={() => call(true)}>{t('backup.preview')}</button><button className="btn danger" disabled={empty || !preview} onClick={() => call(false)}>{t('backup.prune')}</button></>}
    >
      <Field label={t('backup.store')} htmlFor="pr-store">
        <select id="pr-store" className="select" value={store} onChange={(e) => { setStore(e.target.value); setPreview(null) }}>
          {stores.map((s) => <option key={s.name} value={s.name}>{s.name}</option>)}
        </select>
      </Field>
      <KeepFields keep={keep} onChange={(k) => { setKeep(k); setPreview(null) }} />
      <div className="faint">{t('backup.keep_hint')}</div>
      {preview && (
        <div className="log mono">
          {preview.kept.map((id) => <div key={id}>{t('backup.keep_line')} {id}</div>)}
          {preview.removed.map((id) => <div key={id} style={{ color: 'var(--err)' }}>{t('backup.remove_line')} {id}</div>)}
          {preview.removed.length === 0 && <div>{t('backup.nothing_to_prune')}</div>}
        </div>
      )}
      <ErrorAlert error={err} />
    </Dialog>
  )
}

// --- schedules ---

const weekdays = [1, 2, 3, 4, 5, 6, 0] // Monday first

function DaysLabel({ days }: { days: number }) {
  const { t } = useTranslation()
  if (days === 127) return <>{t('backup.days_all')}</>
  if (days === 62) return <>{t('backup.days_workdays')}</>
  return <>{weekdays.filter((d) => days & (1 << d)).map((d) => t(`time.weekday_short.${d}`)).join(', ')}</>
}

function keepSummary(k: Keep, t: (k: string, o?: Record<string, unknown>) => string) {
  const parts = keepFields.filter((f) => k[f] > 0).map((f) => `${t(`backup.keep_short.${f}`)} ${k[f]}`)
  return parts.length ? parts.join(' · ') : t('backup.keep_all')
}

function Schedules({ stores, editable }: { stores: StoragePool[]; editable: boolean }) {
  const { t } = useTranslation()
  const toast = useToast()
  const errText = useErrorText()
  const start = useStartJob()
  const list = usePoll(() => api.get<Schedule[]>('/backup-schedules'), 15000)
  const [edit, setEdit] = useState<Schedule | 'new' | null>(null)
  const [del, setDel] = useState<Schedule | null>(null)
  const runNow = async (s: Schedule) => {
    try {
      await start(api.post<{ job: Job }>(`/backup-schedules/${s.id}/run`))
    } catch (e) {
      toast('err', errText(e))
    }
  }
  const columns: Column<Schedule>[] = [
    { id: 'state', label: t('common.status'), sort: (s) => (s.enabled ? 0 : 1), render: (s) => <StatusDot state={s.running ? 'running' : s.enabled ? 'active' : 'inactive'} label={s.running ? t('state.running') : s.enabled ? t('common.enabled') : t('backup.disabled')} /> },
    { id: 'name', label: t('common.name'), sort: (s) => s.name, render: (s) => <strong style={{ fontWeight: 500 }}>{s.name}</strong> },
    { id: 'when', label: t('backup.when'), render: (s) => <><DaysLabel days={s.days} /> {s.at}</> },
    { id: 'what', label: t('backup.selection'), render: (s) => (s.selection === 'list' ? t('backup.selection_count', { count: s.targets.length }) : t(`backup.selections.${s.selection}`)) },
    { id: 'store', label: t('backup.store'), sort: (s) => s.store, render: (s) => <span className="mono">{s.store}</span> },
    { id: 'keep', label: t('backup.retention'), render: (s) => <span className="faint">{keepSummary(s.keep, t)}</span> },
    { id: 'next', label: t('backup.next_run'), sort: (s) => s.next_run ?? '', render: (s) => formatDate(s.next_run) },
    { id: 'last', label: t('backup.last_run'), sort: (s) => s.last_run ?? '', render: (s) => (s.last_job ? <a href={`/tasks/${s.last_job}`} onClick={(e) => e.stopPropagation()}>{formatDate(s.last_run)}</a> : formatDate(s.last_run)) },
  ]
  return (
    <div className="stack">
      {editable && (
        <div className="row">
          <div className="grow faint">{t('backup.schedule_hint')}</div>
          <button className="btn primary" onClick={() => setEdit('new')} disabled={stores.length === 0}><Icon name="plus" size={12} /> {t('backup.schedule_create')}</button>
        </div>
      )}
      <div className="panel">
        <ErrorAlert error={list.error} />
        <DataTable
          id="schedules"
          columns={columns}
          rows={list.data}
          rowKey={(s) => String(s.id)}
          empty={t('backup.no_schedules')}
          onRowClick={editable ? (s) => setEdit(s) : undefined}
          rowMenu={editable ? (s) => (
            <Menu
              label={t('common.actions')}
              items={[
                { label: t('backup.run_now'), onClick: () => runNow(s), disabled: s.running },
                { label: t('common.edit'), onClick: () => setEdit(s) },
                { label: t('common.delete'), onClick: () => setDel(s), danger: true },
              ]}
            />
          ) : undefined}
        />
      </div>
      {edit && <ScheduleDialog schedule={edit === 'new' ? null : edit} stores={stores} onClose={() => setEdit(null)} onSaved={list.reload} />}
      {del && (
        <ConfirmDialog
          title={t('backup.schedule_delete')}
          message={t('backup.schedule_delete_text', { name: del.name })}
          confirmLabel={t('common.delete')}
          onConfirm={async () => {
            await api.del(`/backup-schedules/${del.id}`)
            list.reload()
          }}
          onClose={() => setDel(null)}
        />
      )}
    </div>
  )
}

function ScheduleDialog({ schedule, stores, onClose, onSaved }: { schedule: Schedule | null; stores: StoragePool[]; onClose: () => void; onSaved: () => void }) {
  const { t } = useTranslation()
  const vms = usePoll(() => api.get<{ name: string }[]>('/vms'), null)
  const cts = usePoll(() => api.get<{ name: string }[]>('/containers'), null)
  const [s, setS] = useState({
    name: schedule?.name ?? 'nightly',
    enabled: schedule?.enabled ?? true,
    store: schedule?.store ?? stores[0]?.name ?? '',
    days: schedule?.days ?? 127,
    at: schedule?.at ?? '02:00',
    selection: schedule?.selection ?? 'all',
    targets: schedule?.targets ?? [],
    mode: schedule?.mode || 'stop',
    keep: schedule?.keep ?? { ...emptyKeep, keep_daily: 7, keep_weekly: 4 },
    notes: schedule?.notes ?? '',
  })
  const [err, setErr] = useState<ApiError>()
  const set = <K extends keyof typeof s>(k: K, v: (typeof s)[K]) => setS({ ...s, [k]: v })
  const toggleTarget = (id: string) => set('targets', s.targets.includes(id) ? s.targets.filter((x) => x !== id) : [...s.targets, id])
  const submit = async () => {
    try {
      if (schedule) await api.put(`/backup-schedules/${schedule.id}`, s)
      else await api.post('/backup-schedules', s)
      onSaved()
      onClose()
    } catch (e) {
      setErr(e as ApiError)
    }
  }
  const fe = (f: string) => (err?.field === f ? err.message : undefined)
  const guests = [...(vms.data ?? []).map((v) => `vm/${v.name}`), ...(cts.data ?? []).map((c) => `ct/${c.name}`)]
  const valid = s.name.trim() && s.store && s.days > 0 && /^([01]\d|2[0-3]):[0-5]\d$/.test(s.at) && (s.selection !== 'list' || s.targets.length > 0)
  return (
    <Dialog
      title={schedule ? t('backup.schedule_edit') : t('backup.schedule_create')}
      onClose={onClose}
      wide
      footer={<><button className="btn" onClick={onClose}>{t('common.cancel')}</button><button className="btn primary" disabled={!valid} onClick={submit}>{t('common.save')}</button></>}
    >
      <div className="form-grid">
        <Field label={t('common.name')} error={fe('name')} htmlFor="sc-name">
          <input id="sc-name" className="input" value={s.name} onChange={(e) => set('name', e.target.value)} />
        </Field>
        <Field label={t('backup.store')} error={fe('store')} htmlFor="sc-store">
          <select id="sc-store" className="select" value={s.store} onChange={(e) => set('store', e.target.value)}>
            {stores.map((p) => <option key={p.name} value={p.name}>{p.name}</option>)}
          </select>
        </Field>
        <Field label={t('backup.days')} error={fe('days')} className="full">
          <div className="row" style={{ flexWrap: 'wrap', gap: 4 }}>
            {weekdays.map((d) => (
              <button key={d} type="button" className={`btn sm ${s.days & (1 << d) ? 'primary' : ''}`} aria-pressed={!!(s.days & (1 << d))} onClick={() => set('days', s.days ^ (1 << d))}>
                {t(`time.weekday_short.${d}`)}
              </button>
            ))}
            <button type="button" className="btn sm ghost" onClick={() => set('days', 127)}>{t('backup.days_all')}</button>
            <button type="button" className="btn sm ghost" onClick={() => set('days', 62)}>{t('backup.days_workdays')}</button>
          </div>
        </Field>
        <Field label={t('backup.at')} hint={t('backup.at_hint')} error={fe('at')} htmlFor="sc-at">
          <input id="sc-at" className="input" type="time" value={s.at} onChange={(e) => set('at', e.target.value)} />
        </Field>
        <Field label={t('backup.ct_mode')} hint={t(`backup.mode_hint.${s.mode}`)} htmlFor="sc-mode">
          <select id="sc-mode" className="select" value={s.mode} onChange={(e) => set('mode', e.target.value)}>
            {['stop', 'suspend', 'live'].map((m) => <option key={m} value={m}>{t(`backup.modes.${m}`)}</option>)}
          </select>
        </Field>
        <Field label={t('backup.selection')} error={fe('selection') ?? fe('targets')} className="full" htmlFor="sc-sel">
          <select id="sc-sel" className="select" value={s.selection} onChange={(e) => set('selection', e.target.value as Schedule['selection'])}>
            {(['all', 'vms', 'containers', 'list'] as const).map((x) => <option key={x} value={x}>{t(`backup.selections.${x}`)}</option>)}
          </select>
        </Field>
        {s.selection === 'list' && (
          <div className="full checklist">
            {guests.length === 0 && <div className="faint">{t('backup.no_guests')}</div>}
            {guests.map((g) => (
              <label key={g} className="check"><input type="checkbox" checked={s.targets.includes(g)} onChange={() => toggleTarget(g)} /> <Icon name={g.startsWith('vm/') ? 'vm' : 'container'} size={12} /> {g.slice(3)}</label>
            ))}
            {s.targets.filter((x) => !guests.includes(x)).map((g) => (
              <label key={g} className="check faint"><input type="checkbox" checked onChange={() => toggleTarget(g)} /> {g} ({t('backup.missing_guest')})</label>
            ))}
          </div>
        )}
      </div>
      <h3 className="section">{t('backup.retention')}</h3>
      <KeepFields keep={s.keep} onChange={(k) => set('keep', k)} />
      <div className="faint">{t('backup.keep_hint')}</div>
      <Field label={t('backup.notes')} htmlFor="sc-notes">
        <input id="sc-notes" className="input" value={s.notes} maxLength={1024} onChange={(e) => set('notes', e.target.value)} />
      </Field>
      <label className="check"><input type="checkbox" checked={s.enabled} onChange={(e) => set('enabled', e.target.checked)} /> {t('common.enabled')}</label>
      <ErrorAlert error={err && !err.field ? err : undefined} />
    </Dialog>
  )
}
