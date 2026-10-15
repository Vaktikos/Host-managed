import { useTranslation } from 'react-i18next'
import { api } from '../api/client'
import type { Interface, VirtNetwork } from '../api/types'
import { DataTable, Column } from '../components/DataTable'
import { Copy, ErrorAlert, StatusDot } from '../components/ui'
import { formatBytes } from '../lib/format'
import { usePoll } from '../lib/hooks'

export function Network() {
  const { t } = useTranslation()
  const ifs = usePoll(() => api.get<Interface[]>('/node/interfaces'), 10000)
  const nets = usePoll(() => api.get<VirtNetwork[]>('/networks'), 20000)

  const ifCols: Column<Interface>[] = [
    { id: 'state', label: t('common.status'), sort: (i) => i.state, render: (i) => <StatusDot state={i.state === 'up' ? 'up' : 'down'} label={t(`net.oper.${i.state}`, { defaultValue: i.state })} /> },
    { id: 'name', label: t('common.name'), sort: (i) => i.name, search: (i) => `${i.name} ${i.addresses.join(' ')} ${i.mac}`, render: (i) => <span className="mono">{i.name}</span> },
    { id: 'kind', label: t('net.kind'), sort: (i) => i.kind, render: (i) => t(`net.kinds.${i.kind}`) },
    { id: 'addr', label: t('net.addresses'), sort: (i) => i.addresses.join(','), render: (i) => <span className="mono">{i.addresses.map((a) => <div key={a}>{a} <Copy value={a.split('/')[0]} /></div>)}</span> },
    { id: 'members', label: t('net.members'), sort: (i) => (i.members ?? []).join(','), render: (i) => <span className="mono faint">{i.master ? t('net.member_of', { name: i.master }) : (i.members ?? []).join(', ')}</span> },
    { id: 'mtu', label: 'MTU', num: true, sort: (i) => i.mtu, render: (i) => i.mtu },
    { id: 'speed', label: t('net.speed'), num: true, sort: (i) => i.speed_mbps ?? 0, render: (i) => (i.speed_mbps ? `${i.speed_mbps} Mbit/s` : '–') },
    { id: 'mac', label: 'MAC', sort: (i) => i.mac, render: (i) => <span className="mono faint">{i.mac}</span>, hidden: true },
    { id: 'rx', label: t('net.rx'), num: true, sort: (i) => i.rx_bytes, render: (i) => formatBytes(i.rx_bytes) },
    { id: 'tx', label: t('net.tx'), num: true, sort: (i) => i.tx_bytes, render: (i) => formatBytes(i.tx_bytes) },
    { id: 'errors', label: t('net.errors_drops'), num: true, sort: (i) => i.rx_errors + i.tx_errors + i.rx_dropped + i.tx_dropped, render: (i) => `${i.rx_errors + i.tx_errors} / ${i.rx_dropped + i.tx_dropped}` },
  ]
  const netCols: Column<VirtNetwork>[] = [
    { id: 'state', label: t('common.status'), sort: (n) => (n.active ? 1 : 0), render: (n) => <StatusDot state={n.active ? 'active' : 'inactive'} label={n.active ? t('common.active') : t('common.inactive')} /> },
    { id: 'name', label: t('common.name'), sort: (n) => n.name, render: (n) => n.name },
    { id: 'bridge', label: t('net.bridge'), sort: (n) => n.bridge, render: (n) => <span className="mono">{n.bridge}</span> },
    { id: 'mode', label: t('net.mode'), sort: (n) => n.forward, render: (n) => n.forward },
    { id: 'ipv4', label: 'IPv4', sort: (n) => n.ipv4 ?? '', render: (n) => <span className="mono">{n.ipv4 ?? '–'}</span> },
    { id: 'autostart', label: t('vm.autostart'), sort: (n) => (n.autostart ? 1 : 0), render: (n) => (n.autostart ? t('common.yes') : t('common.no')) },
  ]
  return (
    <div className="page">
      <div className="page-head"><h1>{t('nav.network')}</h1></div>
      <div className="stack">
        <ErrorAlert error={ifs.error ?? nets.error} />
        <div className="panel">
          <div className="panel-head"><h2>{t('net.interfaces')}</h2></div>
          <DataTable id="interfaces" columns={ifCols} rows={ifs.data?.filter((i) => !i.name.startsWith('veth') && !i.name.startsWith('vnet'))} rowKey={(i) => i.name} />
        </div>
        <div className="panel">
          <div className="panel-head"><h2>{t('net.virtual_networks')}</h2></div>
          <DataTable id="virtnets" columns={netCols} rows={nets.data} rowKey={(n) => n.uuid} empty={t('net.no_networks')} />
        </div>
        <div className="alert info">
          <div className="title">{t('net.readonly_title')}</div>
          <div>{t('net.readonly_text')}</div>
        </div>
      </div>
    </div>
  )
}
