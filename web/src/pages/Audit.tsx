import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { api } from '../api/client'
import type { AuditEntry } from '../api/types'
import { DataTable, Column } from '../components/DataTable'
import { ErrorAlert, StatusDot } from '../components/ui'
import { formatDate } from '../lib/format'
import { usePoll } from '../lib/hooks'

export function Audit() {
  const { t } = useTranslation()
  const log = usePoll(() => api.get<{ items: AuditEntry[]; total: number }>('/audit?limit=500'), 10000)
  const [verify, setVerify] = useState<{ checked: number; intact: boolean; broken_at: number } | null>(null)
  const [busy, setBusy] = useState(false)
  const runVerify = async () => {
    setBusy(true)
    try {
      setVerify(await api.get('/audit/verify'))
    } finally {
      setBusy(false)
    }
  }
  const columns: Column<AuditEntry>[] = [
    { id: 'time', label: t('audit.time'), sort: (a) => a.id, render: (a) => formatDate(a.time) },
    { id: 'user', label: t('tasks.user'), sort: (a) => a.username, render: (a) => a.username },
    { id: 'ip', label: t('audit.ip'), sort: (a) => a.ip, render: (a) => <span className="mono faint">{a.ip || '–'}</span> },
    { id: 'action', label: t('audit.action'), sort: (a) => a.action, render: (a) => <span className="mono">{a.action}</span> },
    { id: 'resource', label: t('audit.resource'), sort: (a) => a.resource, render: (a) => <span className="mono">{a.resource}</span> },
    { id: 'result', label: t('audit.result'), sort: (a) => a.result, render: (a) => <StatusDot state={a.result === 'ok' ? 'ok' : 'error'} label={a.result === 'ok' ? t('audit.ok') : t(`job.status.${a.result}`, { defaultValue: t('audit.error') })} /> },
    { id: 'detail', label: t('audit.detail'), sort: (a) => a.detail, render: (a) => <span className="faint">{a.detail.length > 100 ? a.detail.slice(0, 100) + '…' : a.detail}</span> },
    { id: 'corr', label: t('audit.correlation'), sort: (a) => a.correlation_id, render: (a) => <span className="mono faint">{a.correlation_id}</span>, hidden: true },
  ]
  return (
    <div className="page">
      <div className="page-head">
        <h1>{t('nav.audit')}</h1>
        <span className="sub">{log.data ? t('audit.total', { n: log.data.total }) : ''}</span>
        <div className="actions">
          <button className="btn" disabled={busy} onClick={runVerify}>{t('audit.verify')}</button>
        </div>
      </div>
      {verify && (
        <div className={`alert ${verify.intact ? 'info' : 'err'}`} style={{ marginBottom: 12 }}>
          {verify.intact ? t('audit.intact', { n: verify.checked }) : t('audit.broken', { id: verify.broken_at })}
        </div>
      )}
      <ErrorAlert error={log.error} />
      <div className="panel">
        <DataTable id="audit" columns={columns} rows={log.data?.items} rowKey={(a) => String(a.id)} empty={t('audit.empty')} />
      </div>
    </div>
  )
}
