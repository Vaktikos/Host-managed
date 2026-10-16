import { useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { ApiError, api, upload } from '../api/client'
import type { Job, StoragePool, StorageVolume } from '../api/types'
import { useAuth } from '../auth'
import { DataTable, Column } from '../components/DataTable'
import { Icon } from '../components/icons'
import { ConfirmDialog, Copy, Dialog, ErrorAlert, Field, Meter, StatusDot, useErrorText, useToast } from '../components/ui'
import { useNavigate } from 'react-router-dom'
import { cloudImages } from './CloudInitForm'
import { formatBytes } from '../lib/format'
import { usePoll } from '../lib/hooks'

export function Storage() {
  const { t } = useTranslation()
  const pools = usePoll(() => api.get<StoragePool[]>('/storage'), 15000)
  const [selected, setSelected] = useState<string>()
  const sel = pools.data?.find((p) => p.name === selected) ?? pools.data?.[0]

  const columns: Column<StoragePool>[] = [
    { id: 'state', label: t('common.status'), sort: (p) => p.state, render: (p) => <StatusDot state={p.active ? 'active' : 'inactive'} label={t(`storage.state.${p.state}`)} /> },
    { id: 'name', label: t('common.name'), sort: (p) => p.name, render: (p) => <strong style={{ fontWeight: 500 }}>{p.name}</strong> },
    { id: 'type', label: t('storage.type'), sort: (p) => p.type, render: (p) => p.type },
    { id: 'content', label: t('storage.content'), sort: (p) => p.content, render: (p) => t(`storage.content_${p.content}`) },
    { id: 'path', label: t('storage.path'), sort: (p) => p.path, render: (p) => <span className="mono faint">{p.path}</span> },
    { id: 'used', label: t('storage.used'), num: true, sort: (p) => p.allocation, render: (p) => formatBytes(p.allocation) },
    { id: 'size', label: t('storage.size'), num: true, sort: (p) => p.capacity, render: (p) => formatBytes(p.capacity) },
    { id: 'usage', label: t('storage.usage'), render: (p) => <div style={{ width: 140 }}><Meter value={p.allocation} max={p.capacity} /></div> },
  ]

  return (
    <div className="page">
      <div className="page-head"><h1>{t('nav.storage')}</h1></div>
      <ErrorAlert error={pools.error} />
      <div className="stack">
        <div className="panel">
          <DataTable id="pools" columns={columns} rows={pools.data} rowKey={(p) => p.name} onRowClick={(p) => setSelected(p.name)} selected={sel?.name} empty={t('storage.no_pools')} />
        </div>
        {sel && <Volumes pool={sel} onChanged={pools.reload} />}
        <div className="alert info">{t('storage.backends_hint')}</div>
      </div>
    </div>
  )
}

function Volumes({ pool, onChanged }: { pool: StoragePool; onChanged: () => void }) {
  const { t } = useTranslation()
  const { can } = useAuth()
  const toast = useToast()
  const errText = useErrorText()
  const vols = usePoll(() => api.get<StorageVolume[]>(`/storage/${encodeURIComponent(pool.name)}/volumes`), 20000, [pool.name])
  const [deleting, setDeleting] = useState<StorageVolume | null>(null)
  const [progress, setProgress] = useState<{ name: string; loaded: number; total: number; abort: () => void } | null>(null)
  const fileRef = useRef<HTMLInputElement>(null)
  const [fromURL, setFromURL] = useState(false)

  const startUpload = async (file: File) => {
    const u = upload(`/storage/${encodeURIComponent(pool.name)}/upload/${encodeURIComponent(file.name)}`, file, (loaded, total) =>
      setProgress((p) => (p ? { ...p, loaded, total } : p)),
    )
    setProgress({ name: file.name, loaded: 0, total: file.size, abort: u.abort })
    try {
      await u.promise
      toast('ok', t('storage.upload_done', { name: file.name }))
      vols.reload()
      onChanged()
    } catch (e) {
      if (!(e instanceof ApiError && e.code === 'aborted')) toast('err', `${file.name}: ${errText(e)}`)
    } finally {
      setProgress(null)
    }
  }

  const columns: Column<StorageVolume>[] = [
    { id: 'name', label: t('common.name'), sort: (v) => v.name, render: (v) => <span className="mono">{v.name}</span> },
    { id: 'format', label: t('vm.format'), sort: (v) => v.format, render: (v) => v.format || v.type },
    { id: 'capacity', label: t('storage.size'), num: true, sort: (v) => v.capacity, render: (v) => formatBytes(v.capacity) },
    { id: 'allocation', label: t('storage.allocated'), num: true, sort: (v) => v.allocation, render: (v) => formatBytes(v.allocation) },
    { id: 'path', label: t('storage.path'), sort: (v) => v.path, render: (v) => <span className="mono faint">{v.path} <Copy value={v.path} /></span>, hidden: true },
  ]
  const canUpload = can('storage.upload') && pool.type === 'dir' && pool.active

  return (
    <div className="panel">
      <div className="panel-head">
        <h2>{t('storage.volumes_in', { name: pool.name })}</h2>
        <span className="faint">{t('storage.free_of', { free: formatBytes(pool.available), total: formatBytes(pool.capacity) })}</span>
        <div className="actions">
          {canUpload && (
            <>
              <input ref={fileRef} type="file" hidden accept={pool.content === 'iso' ? '.iso,.img' : undefined} onChange={(e) => e.target.files?.[0] && void startUpload(e.target.files[0])} />
              <button className="btn sm" disabled={!!progress} onClick={() => fileRef.current?.click()}>
                <Icon name="upload" size={12} /> {t('storage.upload')}
              </button>
              <button className="btn sm" onClick={() => setFromURL(true)}>
                {t('storage.download_url')}
              </button>
            </>
          )}
        </div>
      </div>
      {progress && (
        <div className="panel-body row">
          <span className="mono">{progress.name}</span>
          <div className="grow progress"><div style={{ width: `${progress.total ? (progress.loaded / progress.total) * 100 : 0}%` }} /></div>
          <span className="faint nowrap">{formatBytes(progress.loaded)} / {formatBytes(progress.total)}</span>
          <button className="btn sm" onClick={progress.abort}>{t('common.cancel')}</button>
        </div>
      )}
      <ErrorAlert error={vols.error} />
      <DataTable
        id="volumes"
        columns={columns}
        rows={vols.data}
        rowKey={(v) => v.name}
        empty={t('storage.no_volumes')}
        rowMenu={can('storage.delete') ? (v) => (
          <button className="btn sm icon" title={t('common.delete')} aria-label={t('common.delete')} onClick={() => setDeleting(v)}>
            <Icon name="trash" size={12} />
          </button>
        ) : undefined}
      />
      {fromURL && <DownloadDialog pool={pool} onClose={() => setFromURL(false)} />}
      {deleting && (
        <ConfirmDialog
          title={t('storage.delete_volume')}
          message={t('storage.delete_volume_text', { name: deleting.name, pool: pool.name })}
          confirmLabel={t('common.delete')}
          confirmText={deleting.name}
          onConfirm={async () => {
            await api.del(`/storage/${encodeURIComponent(pool.name)}/volumes/${encodeURIComponent(deleting.name)}`)
            toast('ok', t('storage.deleted', { name: deleting.name }))
            vols.reload()
            onChanged()
          }}
          onClose={() => setDeleting(null)}
        />
      )}
    </div>
  )
}

const fileRe = /^[a-zA-Z0-9][a-zA-Z0-9._+-]{0,200}$/
const sumRe = /^((sha256:)?[0-9a-fA-F]{64}|(sha512:)?[0-9a-fA-F]{128})$/

function nameFromURL(u: string): string {
  try {
    const p = new URL(u).pathname
    return decodeURIComponent(p.slice(p.lastIndexOf('/') + 1))
  } catch {
    return ''
  }
}

// DownloadDialog lets the node fetch a file (typically an ISO) from a URL.
function DownloadDialog({ pool, onClose }: { pool: StoragePool; onClose: () => void }) {
  const { t } = useTranslation()
  const toast = useToast()
  const nav = useNavigate()
  const [url, setURL] = useState('')
  const [name, setName] = useState('')
  const [nameTouched, setNameTouched] = useState(false)
  const [sum, setSum] = useState('')
  const [err, setErr] = useState<ApiError>()
  const [busy, setBusy] = useState(false)
  const effName = nameTouched ? name : nameFromURL(url)
  const errors = useMemo(() => {
    const e: Record<string, string> = {}
    if (!/^https?:\/\/[^\s/]+/i.test(url.trim())) e.url = t('validation.url')
    if (!fileRe.test(effName) || effName.includes('..')) e.name = t('validation.file_name')
    if (sum.trim() && !sumRe.test(sum.trim())) e.checksum = t('validation.checksum')
    return e
  }, [url, effName, sum, t])
  const submit = async () => {
    setBusy(true)
    setErr(undefined)
    try {
      const r = await api.post<{ job: Job }>(`/storage/${encodeURIComponent(pool.name)}/download`, { url: url.trim(), name: effName, checksum: sum.trim() || undefined })
      toast('info', t('tasks.started', { id: r.job.id }))
      onClose()
      nav(`/tasks/${r.job.id}`)
    } catch (e) {
      setErr(e as ApiError)
      setBusy(false)
    }
  }
  const fe = (k: string) => (url || k !== 'url' ? errors[k] : undefined) ?? (err?.field === k ? err.message : undefined)
  return (
    <Dialog
      title={t('storage.download_title', { pool: pool.name })}
      onClose={onClose}
      footer={<><button className="btn" onClick={onClose}>{t('common.cancel')}</button><button className="btn primary" disabled={busy || Object.keys(errors).length > 0} onClick={submit}>{t('storage.download_start')}</button></>}
    >
      {pool.content === 'images' && (
        <Field label={t('storage.cloud_images')} hint={t('storage.cloud_images_hint')} htmlFor="dl-pick">
          <select id="dl-pick" className="select" value="" onChange={(e) => { setURL(e.target.value); setNameTouched(false) }}>
            <option value="">{t('common.select')}</option>
            {cloudImages.map((c) => <option key={c.url} value={c.url}>{c.label}</option>)}
          </select>
        </Field>
      )}
      <Field label="URL" error={fe('url')} hint={t('storage.download_url_hint')} htmlFor="dl-url">
        <input id="dl-url" className="input mono" value={url} onChange={(e) => setURL(e.target.value)} placeholder="https://cdimage.debian.org/…/debian-13-amd64-netinst.iso" autoComplete="off" />
      </Field>
      <Field label={t('storage.file_name')} error={effName ? fe('name') : undefined} htmlFor="dl-name">
        <input id="dl-name" className="input mono" value={effName} onChange={(e) => { setNameTouched(true); setName(e.target.value) }} />
      </Field>
      <Field label={t('storage.checksum')} error={fe('checksum')} hint={<>{t('storage.checksum_hint')} {cloudImages.find((c) => c.url === url.trim()) && <a href={cloudImages.find((c) => c.url === url.trim())!.sums} target="_blank" rel="noreferrer noopener">{t('storage.checksum_list')}</a>}</>} htmlFor="dl-sum">
        <input id="dl-sum" className="input mono" value={sum} onChange={(e) => setSum(e.target.value)} placeholder="sha256:…" autoComplete="off" />
      </Field>
      {err && !err.field && <ErrorAlert error={err} />}
    </Dialog>
  )
}
