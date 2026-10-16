import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { useNavigate } from 'react-router-dom'
import { api } from '../api/client'
import type { Job, VM } from '../api/types'
import { useAuth } from '../auth'
import { ApiError } from '../api/client'
import { ConfirmDialog, Dialog, ErrorAlert, Field, MenuItem, useErrorText, useToast } from '../components/ui'
import { useResources } from '../resources'

// Which power actions make sense in which state.
export function vmActionAllowed(state: string, action: string, template = false): boolean {
  const on = state === 'running' || state === 'blocked'
  switch (action) {
    case 'start':
      return !template && (state === 'shutoff' || state === 'crashed')
    case 'shutdown':
    case 'reboot':
    case 'reset':
    case 'pause':
      return on
    case 'stop':
      return state !== 'shutoff'
    case 'resume':
      return state === 'paused'
  }
  return false
}

// useVMActions bundles power actions, confirmations and deletion.
export function useVMActions(onChanged?: () => void) {
  const { t } = useTranslation()
  const { can } = useAuth()
  const toast = useToast()
  const errText = useErrorText()
  const { refreshAll } = useResources()
  const nav = useNavigate()
  const [confirm, setConfirm] = useState<{ vm: VM; action: string } | null>(null)
  const [deleting, setDeleting] = useState<VM | null>(null)
  const [deleteDisks, setDeleteDisks] = useState(true)
  const [cloning, setCloning] = useState<VM | null>(null)

  const run = async (vm: VM, action: string) => {
    try {
      await api.post(`/vms/${encodeURIComponent(vm.name)}/actions/${action}`)
      toast('ok', t('vm.action_sent', { action: t(`vm.action.${action}`), name: vm.name }))
      refreshAll()
      onChanged?.()
    } catch (e) {
      toast('err', `${vm.name}: ${errText(e)}`)
    }
  }

  const request = (vm: VM, action: string) => {
    // Actions that can lose guest data are confirmed.
    if (action === 'stop' || action === 'reset') setConfirm({ vm, action })
    else void run(vm, action)
  }

  const items = (vm: VM): MenuItem[] => {
    const pw = can('vm.power')
    const list: MenuItem[] = ['start', 'shutdown', 'reboot', 'pause', 'resume', 'stop', 'reset'].map((a) => ({
      label: t(`vm.action.${a}`),
      onClick: () => request(vm, a),
      disabled: !pw || !vmActionAllowed(vm.state, a, vm.template),
      danger: a === 'stop' || a === 'reset',
    }))
    list.push({ label: '', onClick: () => {}, separator: true })
    list.push({ label: t('vm.open_console'), onClick: () => nav(`/vms/${encodeURIComponent(vm.name)}?tab=console`), disabled: !can('vm.console') || vm.state !== 'running' })
    list.push({ label: t('vm.clone'), onClick: () => setCloning(vm), disabled: !can('vm.create') || vm.state !== 'shutoff' })
    list.push({ label: t('vm.delete'), onClick: () => setDeleting(vm), disabled: !can('vm.delete') || vm.state !== 'shutoff', danger: true })
    return list
  }

  const dialogs = (
    <>
      {cloning && <CloneDialog vm={cloning} onClose={() => setCloning(null)} />}
      {confirm && (
        <ConfirmDialog
          title={t(`vm.action.${confirm.action}`)}
          message={t(`vm.confirm_${confirm.action}`, { name: confirm.vm.name })}
          confirmLabel={t(`vm.action.${confirm.action}`)}
          onConfirm={() => run(confirm.vm, confirm.action)}
          onClose={() => setConfirm(null)}
        />
      )}
      {deleting && (
        <ConfirmDialog
          title={t('vm.delete_title', { name: deleting.name })}
          message={t('vm.delete_text')}
          confirmLabel={t('vm.delete')}
          confirmText={deleting.name}
          onConfirm={async () => {
            const res = await api.del<{ job: Job }>(`/vms/${encodeURIComponent(deleting.name)}?confirm=${encodeURIComponent(deleting.name)}&delete_disks=${deleteDisks}`)
            toast('info', t('tasks.started', { id: res.job.id }))
            refreshAll()
            nav('/vms')
          }}
          onClose={() => setDeleting(null)}
        >
          <label className="check">
            <input type="checkbox" checked={deleteDisks} onChange={(e) => setDeleteDisks(e.target.checked)} />
            {t('vm.delete_disks')}
          </label>
        </ConfirmDialog>
      )}
    </>
  )
  return { request, items, dialogs }
}

const nameRe = /^[a-zA-Z0-9][a-zA-Z0-9._-]{0,62}$/

function CloneDialog({ vm, onClose }: { vm: VM; onClose: () => void }) {
  const { t } = useTranslation()
  const toast = useToast()
  const nav = useNavigate()
  const { vms, refreshAll } = useResources()
  const [name, setName] = useState(`${vm.name}-clone`)
  const [mode, setMode] = useState(vm.template ? 'linked' : 'full')
  const [start, setStart] = useState(false)
  const [err, setErr] = useState<ApiError>()
  const taken = vms.data?.some((v) => v.name === name)
  const valid = nameRe.test(name) && !taken
  const submit = async () => {
    try {
      const r = await api.post<{ job: Job }>(`/vms/${encodeURIComponent(vm.name)}/clone`, { name, mode, start })
      toast('info', t('tasks.started', { id: r.job.id }))
      refreshAll()
      onClose()
      nav(`/tasks/${r.job.id}`)
    } catch (e) {
      setErr(e as ApiError)
    }
  }
  return (
    <Dialog title={t('vm.clone_title', { name: vm.name })} onClose={onClose} footer={<><button className="btn" onClick={onClose}>{t('common.cancel')}</button><button className="btn primary" disabled={!valid} onClick={submit}>{t('vm.clone')}</button></>}>
      <Field label={t('vm.clone_name')} error={!nameRe.test(name) ? t('validation.vm_name') : taken ? t('validation.name_taken') : err?.field === 'name' ? err.message : undefined} htmlFor="cl-name">
        <input id="cl-name" className="input" value={name} onChange={(e) => setName(e.target.value)} />
      </Field>
      <Field label={t('vm.clone_mode')} hint={mode === 'linked' ? t('vm.clone_linked_hint') : t('vm.clone_full_hint')} htmlFor="cl-mode">
        <select id="cl-mode" className="select" value={mode} onChange={(e) => setMode(e.target.value)}>
          <option value="full">{t('vm.clone_full')}</option>
          <option value="linked" disabled={!vm.template}>{t('vm.clone_linked')}</option>
        </select>
      </Field>
      <label className="check"><input type="checkbox" checked={start} onChange={(e) => setStart(e.target.checked)} />{t('vm.start_after_create')}</label>
      {err && !err.field && <ErrorAlert error={err} />}
    </Dialog>
  )
}
