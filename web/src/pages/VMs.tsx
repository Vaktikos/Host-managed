import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { useNavigate } from 'react-router-dom'
import type { VM } from '../api/types'
import { useAuth } from '../auth'
import { DataTable, Column } from '../components/DataTable'
import { Icon } from '../components/icons'
import { ErrorAlert, Menu, StatusDot } from '../components/ui'
import { formatMiB, formatPercent, formatRate } from '../lib/format'
import { useResources } from '../resources'
import { VMCreateDialog } from './VMCreate'
import { useVMActions, vmActionAllowed } from './vmActions'

export function VMs() {
  const { t } = useTranslation()
  const { vms } = useResources()
  const { can } = useAuth()
  const nav = useNavigate()
  const [creating, setCreating] = useState(false)
  const actions = useVMActions()

  const columns: Column<VM>[] = [
    { id: 'state', label: t('common.status'), render: (v) => <StatusDot state={v.state} />, sort: (v) => v.state },
    {
      id: 'name', label: t('common.name'), sort: (v) => v.name, search: (v) => `${v.name} ${v.tags.join(' ')} ${v.description}`,
      render: (v) => (
        <span className="row">
          <strong style={{ fontWeight: 500 }}>{v.name}</strong>
          {v.template && <span className="badge">{t('vm.template')}</span>}
        </span>
      ),
    },
    { id: 'cpu', label: t('vm.cpu_usage'), num: true, sort: (v) => v.stats?.cpu_usage ?? -1, render: (v) => formatPercent(v.stats?.cpu_usage) },
    { id: 'vcpus', label: t('vm.vcpus'), num: true, sort: (v) => v.vcpus, render: (v) => v.vcpus },
    { id: 'memory', label: t('vm.memory'), num: true, sort: (v) => v.memory_mib, render: (v) => formatMiB(v.memory_mib) },
    { id: 'net', label: t('vm.network_io'), num: true, sort: (v) => (v.stats?.net_rx_bps ?? 0) + (v.stats?.net_tx_bps ?? 0), render: (v) => (v.stats ? `${formatRate(v.stats.net_rx_bps)} / ${formatRate(v.stats.net_tx_bps)}` : '–'), hidden: false },
    { id: 'disk', label: t('vm.disk_io'), num: true, sort: (v) => (v.stats?.disk_read_bps ?? 0) + (v.stats?.disk_write_bps ?? 0), render: (v) => (v.stats ? `${formatRate(v.stats.disk_read_bps)} / ${formatRate(v.stats.disk_write_bps)}` : '–'), hidden: true },
    { id: 'tags', label: t('vm.tags'), sort: (v) => v.tags.join(','), render: (v) => v.tags.map((x) => <span key={x} className="tag">{x}</span>) },
    { id: 'autostart', label: t('vm.autostart'), sort: (v) => (v.autostart ? 1 : 0), render: (v) => (v.autostart ? t('common.yes') : t('common.no')), hidden: true },
    { id: 'description', label: t('vm.description'), sort: (v) => v.description, render: (v) => <span className="faint">{v.description}</span>, hidden: true },
    { id: 'uuid', label: 'UUID', sort: (v) => v.uuid, render: (v) => <span className="mono faint">{v.uuid}</span>, hidden: true },
  ]

  return (
    <div className="page">
      <div className="page-head">
        <h1>{t('nav.vms')}</h1>
        <div className="actions">
          {can('vm.create') && (
            <button className="btn primary" onClick={() => setCreating(true)}>
              <Icon name="plus" /> {t('vm.create')}
            </button>
          )}
        </div>
      </div>
      <ErrorAlert error={vms.error} />
      <div className="panel">
        <DataTable
          id="vms"
          columns={columns}
          rows={vms.data}
          rowKey={(v) => v.uuid}
          onRowClick={(v) => nav(`/vms/${encodeURIComponent(v.name)}`)}
          empty={t('vm.none')}
          rowMenu={(v) => (
            <span className="row" style={{ gap: 4 }}>
              {can('vm.power') && vmActionAllowed(v.state, 'start', v.template) && (
                <button className="btn sm icon" title={t('vm.action.start')} aria-label={t('vm.action.start')} onClick={() => actions.request(v, 'start')}>
                  <Icon name="play" size={12} />
                </button>
              )}
              {can('vm.power') && vmActionAllowed(v.state, 'shutdown') && (
                <button className="btn sm icon" title={t('vm.action.shutdown')} aria-label={t('vm.action.shutdown')} onClick={() => actions.request(v, 'shutdown')}>
                  <Icon name="power" size={12} />
                </button>
              )}
              <Menu items={actions.items(v)} label={t('common.actions')} />
            </span>
          )}
        />
      </div>
      {actions.dialogs}
      {creating && <VMCreateDialog onClose={() => setCreating(false)} />}
    </div>
  )
}
