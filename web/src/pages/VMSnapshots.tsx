import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { useNavigate } from 'react-router-dom'
import { ApiError, api } from '../api/client'
import type { Job, VMDetail } from '../api/types'
import { useAuth } from '../auth'
import { Icon } from '../components/icons'
import { ConfirmDialog, Dialog, ErrorAlert, Field, StatusDot, useToast } from '../components/ui'
import { formatDate } from '../lib/format'
import { usePoll } from '../lib/hooks'

export interface Snapshot {
  name: string
  description: string
  created_at: number
  state: string
  memory: boolean
  parent?: string
  current: boolean
}

const snapRe = /^[a-zA-Z0-9][a-zA-Z0-9._-]{0,62}$/

// Snapshots are point-in-time states inside the VM's qcow2 disks. They are
// not backups: they live on the same storage as the VM.
export function Snapshots({ vm }: { vm: VMDetail }) {
  const { t } = useTranslation()
  const { can } = useAuth()
  const toast = useToast()
  const nav = useNavigate()
  const base = `/vms/${encodeURIComponent(vm.name)}/snapshots`
  const list = usePoll(() => api.get<Snapshot[]>(base), 5000, [vm.name])
  const [create, setCreate] = useState(false)
  const [revert, setRevert] = useState<string | null>(null)
  const [del, setDel] = useState<string | null>(null)
  const uefiRunning = vm.firmware === 'uefi' && vm.state !== 'shutoff'
  const nonQcow = vm.disks.some((d) => d.device === 'disk' && d.format !== 'qcow2')
  const job = async (p: Promise<{ job: Job }>) => {
    const r = await p
    toast('info', t('tasks.started', { id: r.job.id }))
    nav(`/tasks/${r.job.id}`)
  }
  const editable = can('vm.snapshot')
  return (
    <div className="stack">
      <div className="panel">
        <div className="panel-head">
          <h2>{t('snap.title')}</h2>
          <div className="actions">
            {editable && (
              <button className="btn sm" disabled={uefiRunning || nonQcow} onClick={() => setCreate(true)}>
                <Icon name="plus" size={12} /> {t('snap.create')}
              </button>
            )}
          </div>
        </div>
        <ErrorAlert error={list.error} />
        <table className="data">
          <thead><tr><th>{t('common.name')}</th><th>{t('snap.created')}</th><th>{t('snap.vm_state')}</th><th>{t('snap.memory')}</th><th>{t('vm.description')}</th>{editable && <th />}</tr></thead>
          <tbody>
            {list.data?.map((s) => (
              <tr key={s.name}>
                <td className="mono">{s.name} {s.current && <span className="badge">{t('snap.current')}</span>}</td>
                <td>{formatDate(new Date(s.created_at * 1000).toISOString())}</td>
                <td><StatusDot state={s.state} /></td>
                <td>{s.memory ? t('common.yes') : t('common.no')}</td>
                <td className="faint truncate">{s.description}</td>
                {editable && (
                  <td className="right nowrap">
                    <button className="btn sm" onClick={() => setRevert(s.name)}>{t('snap.revert')}</button>{' '}
                    <button className="btn sm danger" onClick={() => setDel(s.name)}>{t('common.delete')}</button>
                  </td>
                )}
              </tr>
            ))}
          </tbody>
        </table>
        {list.data?.length === 0 && <div className="empty">{t('snap.none')}</div>}
      </div>
      {uefiRunning && <div className="alert info">{t('snap.uefi_running')}</div>}
      {nonQcow && <div className="alert info">{t('snap.needs_qcow2')}</div>}
      <div className="alert info">{t('snap.not_backup')}</div>
      {create && <CreateSnapshot base={base} running={vm.state !== 'shutoff'} onClose={() => setCreate(false)} onStarted={job} />}
      {revert && (
        <ConfirmDialog
          title={t('snap.revert_title', { name: revert })}
          message={t('snap.revert_text')}
          confirmLabel={t('snap.revert')}
          onConfirm={() => job(api.post<{ job: Job }>(`${base}/${encodeURIComponent(revert)}/revert`))}
          onClose={() => setRevert(null)}
        />
      )}
      {del && (
        <ConfirmDialog
          title={t('snap.delete_title', { name: del })}
          message={t('snap.delete_text')}
          confirmLabel={t('common.delete')}
          onConfirm={() => job(api.del<{ job: Job }>(`${base}/${encodeURIComponent(del)}`))}
          onClose={() => setDel(null)}
        />
      )}
    </div>
  )
}

function CreateSnapshot({ base, running, onClose, onStarted }: { base: string; running: boolean; onClose: () => void; onStarted: (p: Promise<{ job: Job }>) => Promise<void> }) {
  const { t } = useTranslation()
  const [name, setName] = useState(`snap-${new Date().toISOString().slice(0, 16).replace(/[-:T]/g, '')}`)
  const [desc, setDesc] = useState('')
  const [err, setErr] = useState<ApiError>()
  const submit = async () => {
    try {
      await onStarted(api.post<{ job: Job }>(base, { name, description: desc }))
      onClose()
    } catch (e) {
      setErr(e as ApiError)
    }
  }
  return (
    <Dialog title={t('snap.create')} onClose={onClose} footer={<><button className="btn" onClick={onClose}>{t('common.cancel')}</button><button className="btn primary" disabled={!snapRe.test(name)} onClick={submit}>{t('common.create')}</button></>}>
      <Field label={t('common.name')} error={snapRe.test(name) ? undefined : t('validation.vm_name')} htmlFor="sn-name">
        <input id="sn-name" className="input mono" value={name} onChange={(e) => setName(e.target.value)} />
      </Field>
      <Field label={t('vm.description')} htmlFor="sn-desc">
        <input id="sn-desc" className="input" value={desc} onChange={(e) => setDesc(e.target.value)} />
      </Field>
      <div className="faint">{running ? t('snap.with_memory') : t('snap.disk_only')}</div>
      <ErrorAlert error={err} />
    </Dialog>
  )
}
