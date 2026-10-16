import { useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Link, useNavigate, useParams } from 'react-router-dom'
import { api } from '../api/client'
import type { Job, LogLine } from '../api/types'
import { useAuth } from '../auth'
import { DataTable, Column } from '../components/DataTable'
import { ErrorAlert, JobStatus, useErrorText, useToast } from '../components/ui'
import { durationBetween, formatDate, formatPercent, formatTime } from '../lib/format'
import { usePoll } from '../lib/hooks'

export function JobsTable({ target }: { target?: string }) {
  const { t } = useTranslation()
  const nav = useNavigate()
  const q = target ? `?limit=200&target=${encodeURIComponent(target)}` : '?limit=500'
  const jobs = usePoll(() => api.get<{ items: Job[]; total: number }>(`/jobs${q}`), 3000, [q])
  const columns: Column<Job>[] = [
    { id: 'created', label: t('tasks.started_at'), sort: (j) => j.created_at, render: (j) => formatDate(j.created_at) },
    { id: 'type', label: t('tasks.type'), sort: (j) => j.type, search: (j) => `${j.type} ${t(`job.type.${j.type.replace('.', '_')}`)}`, render: (j) => t(`job.type.${j.type.replace('.', '_')}`) },
    { id: 'target', label: t('tasks.target'), sort: (j) => j.target, render: (j) => <span className="mono">{j.target}</span> },
    { id: 'user', label: t('tasks.user'), sort: (j) => j.username, render: (j) => j.username },
    { id: 'duration', label: t('tasks.duration'), sort: (j) => j.finished_at ?? '', render: (j) => durationBetween(j.started_at, j.finished_at) },
    { id: 'status', label: t('common.status'), sort: (j) => j.status, render: (j) => <span className="row"><JobStatus status={j.status} />{j.status === 'running' && j.progress != null && <span className="faint">{formatPercent(j.progress, 0)}</span>}</span> },
    { id: 'error', label: t('tasks.error'), sort: (j) => j.error, render: (j) => <span className="faint">{j.error.length > 80 ? j.error.slice(0, 80) + '…' : j.error}</span> },
    { id: 'id', label: 'ID', sort: (j) => j.id, render: (j) => <span className="mono faint">{j.id}</span>, hidden: true },
  ]
  return (
    <>
      <ErrorAlert error={jobs.error} />
      <DataTable id={target ? 'jobs-target' : 'jobs'} columns={columns} rows={jobs.data?.items} rowKey={(j) => j.id} onRowClick={(j) => nav(`/tasks/${j.id}`)} empty={t('tasks.none')} />
    </>
  )
}

export function Tasks() {
  const { t } = useTranslation()
  return (
    <div className="page">
      <div className="page-head"><h1>{t('nav.tasks')}</h1></div>
      <div className="panel"><JobsTable /></div>
    </div>
  )
}

export function TaskDetail() {
  const { t } = useTranslation()
  const { id = '' } = useParams()
  const { me } = useAuth()
  const toast = useToast()
  const errText = useErrorText()
  const job = usePoll(() => api.get<Job>(`/jobs/${encodeURIComponent(id)}`), 2000, [id])
  const [lines, setLines] = useState<LogLine[]>([])
  const last = useRef(0)
  const logBox = useRef<HTMLDivElement>(null)
  const done = job.data && (job.data.status === 'succeeded' || job.data.status === 'failed' || job.data.status === 'cancelled')

  useEffect(() => {
    last.current = 0
    setLines([])
  }, [id])

  useEffect(() => {
    let stop = false
    const tick = async () => {
      try {
        const more = await api.get<LogLine[]>(`/jobs/${encodeURIComponent(id)}/log?after=${last.current}`)
        if (stop || more.length === 0) return
        last.current = more[more.length - 1].id
        setLines((l) => [...l, ...more])
        requestAnimationFrame(() => logBox.current?.scrollTo({ top: logBox.current.scrollHeight }))
      } catch {
        // job status shows the error
      }
    }
    void tick()
    const t = window.setInterval(tick, 1500)
    return () => {
      stop = true
      window.clearInterval(t)
    }
  }, [id, done])

  const j = job.data
  const cancel = async () => {
    try {
      await api.post(`/jobs/${encodeURIComponent(id)}/cancel`)
      toast('info', t('tasks.cancel_requested'))
    } catch (e) {
      toast('err', errText(e))
    }
  }
  const targetLink = j && (j.target.startsWith('vm/') ? `/vms/${encodeURIComponent(j.target.slice(3))}` : j.target.startsWith('ct/') ? `/containers/${encodeURIComponent(j.target.slice(3))}` : undefined)

  return (
    <div className="page">
      <div className="page-head">
        <div>
          <div className="crumbs"><Link to="/tasks">{t('nav.tasks')}</Link> / <span className="mono">{id}</span></div>
          <div className="row">
            <h1>{j ? t(`job.type.${j.type.replace('.', '_')}`) : t('tasks.task')}</h1>
            {j && <JobStatus status={j.status} />}
          </div>
        </div>
        <div className="actions">
          {j && (j.status === 'running' || j.status === 'queued') && (me?.user.username === j.username || me?.user.role === 'admin') && (
            <button className="btn danger" onClick={cancel}>{t('tasks.cancel')}</button>
          )}
        </div>
      </div>
      <ErrorAlert error={job.error} />
      {j && (
        <div className="stack">
          <div className="panel">
            <div className="panel-body">
              <dl className="kv">
                <dt>{t('tasks.target')}</dt><dd className="mono">{targetLink && j.status === 'succeeded' && !j.type.endsWith('.delete') ? <Link to={targetLink}>{j.target}</Link> : j.target}</dd>
                <dt>{t('tasks.user')}</dt><dd>{j.username}</dd>
                <dt>{t('tasks.started_at')}</dt><dd>{formatDate(j.started_at ?? j.created_at)}</dd>
                <dt>{t('tasks.finished_at')}</dt><dd>{formatDate(j.finished_at)}</dd>
                <dt>{t('tasks.duration')}</dt><dd>{durationBetween(j.started_at, j.finished_at)}</dd>
                <dt>{t('tasks.progress')}</dt>
                <dd>
                  {j.progress != null ? (
                    <div className="row">
                      <div className="progress" style={{ width: 240 }}><div style={{ width: `${j.progress}%` }} /></div>
                      <span>{formatPercent(j.progress, 0)}</span>
                    </div>
                  ) : done ? t(`job.status.${j.status}`) : t('tasks.progress_running')}
                </dd>
              </dl>
            </div>
          </div>
          {j.error && <div className="alert err"><div className="title">{t('tasks.error')}</div><div className="mono">{j.error}</div></div>}
          <div className="panel">
            <div className="panel-head"><h2>{t('tasks.log')}</h2></div>
            <div className="panel-body">
              <div className="log" ref={logBox} style={{ maxHeight: '55vh' }}>
                {lines.map((l) => (
                  <div key={l.id}><span className="ts">{formatTime(l.time)}</span>{l.line}</div>
                ))}
                {lines.length === 0 && <span className="faint">{t('tasks.no_log')}</span>}
              </div>
            </div>
          </div>
        </div>
      )}
    </div>
  )
}
