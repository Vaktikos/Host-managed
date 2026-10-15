import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { useNavigate } from 'react-router-dom'
import { api } from '../api/client'
import type { Job, VM } from '../api/types'
import { useAuth } from '../auth'
import { ConfirmDialog, MenuItem, useErrorText, useToast } from '../components/ui'
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
    list.push({ label: t('vm.delete'), onClick: () => setDeleting(vm), disabled: !can('vm.delete') || vm.state !== 'shutoff', danger: true })
    return list
  }

  const dialogs = (
    <>
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
