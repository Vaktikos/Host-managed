import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Link } from 'react-router-dom'
import { api } from '../api/client'
import type { NodeInfo } from '../api/types'
import { ErrorAlert, Meter, Sparkline, StatusDot } from '../components/ui'
import { formatBytes, formatDuration, formatNumber, formatPercent } from '../lib/format'
import { usePoll } from '../lib/hooks'
import { useResources } from '../resources'

export function Overview() {
  const { t } = useTranslation()
  const node = usePoll(() => api.get<NodeInfo>('/node'), 5000)
  const { vms, cts } = useResources()
  // Short client-side history of host CPU samples for the sparkline.
  const [cpuHist, setCpuHist] = useState<(number | null)[]>([])
  const h = node.data?.host
  useEffect(() => {
    if (node.data) setCpuHist((hist) => [...hist, node.data!.host.cpu_usage].slice(-60))
  }, [node.data])

  const memUsed = h ? h.mem_total - h.mem_available : 0
  const running = vms.data?.filter((v) => v.state === 'running').length ?? 0
  const ctRunning = cts.data?.filter((c) => c.state === 'running').length ?? 0

  return (
    <div className="page">
      <div className="page-head">
        <div>
          <div className="crumbs">{t('nav.datacenter')} / {t('nav.local_node')}</div>
          <h1>{h?.hostname ?? t('nav.overview')}</h1>
        </div>
        {h && <span className="sub">{h.os} · {t('node.kernel')} {h.kernel}</span>}
      </div>
      <ErrorAlert error={node.error} />
      {h && !h.kvm_available && (
        <div className="alert warn" style={{ marginBottom: 12 }}>
          <div className="title">{t('node.no_kvm_title')}</div>
          <div>{node.data?.control_plane.allow_software_emulation ? t('node.no_kvm_tcg') : t('node.no_kvm_text')}</div>
        </div>
      )}
      {h && (
        <div className="stack">
          <div className="grid grid-4">
            <div className="panel metric">
              <div className="label">{t('node.cpu')}</div>
              <div className="value">{formatPercent(h.cpu_usage)}</div>
              <div className="detail">{t('node.threads', { n: h.cpu_threads })} · {t('node.load')} {h.load.map((l) => formatNumber(l)).join(' / ')}</div>
              <Sparkline values={cpuHist} max={100} />
            </div>
            <div className="panel metric">
              <div className="label">{t('node.memory')}</div>
              <div className="value">{formatBytes(memUsed)}</div>
              <div className="detail">{t('common.of')} {formatBytes(h.mem_total)}</div>
              <Meter value={memUsed} max={h.mem_total} />
            </div>
            <div className="panel metric">
              <div className="label">{t('nav.vms')}</div>
              <div className="value">{running} / {vms.data?.length ?? '–'}</div>
              <div className="detail">{t('node.running')}</div>
              <div style={{ marginTop: 8 }}><Link to="/vms">{t('node.show_vms')}</Link></div>
            </div>
            <div className="panel metric">
              <div className="label">{t('nav.containers')}</div>
              <div className="value">{ctRunning} / {cts.data?.length ?? '–'}</div>
              <div className="detail">{t('node.running')}</div>
              <div style={{ marginTop: 8 }}><Link to="/containers">{t('node.show_cts')}</Link></div>
            </div>
          </div>

          <div className="grid grid-2">
            <div className="panel">
              <div className="panel-head"><h2>{t('node.system')}</h2></div>
              <div className="panel-body">
                <dl className="kv">
                  <dt>{t('node.cpu_model')}</dt><dd>{h.cpu_model} ({t('node.sockets', { n: h.cpu_sockets })})</dd>
                  <dt>{t('node.virtualization')}</dt>
                  <dd>
                    <StatusDot state={h.kvm_available ? 'ok' : 'error'} label={h.kvm_available ? t('node.kvm_ok', { ext: h.virt_extensions || '–' }) : t('node.kvm_missing')} />
                  </dd>
                  <dt>{t('node.iommu')}</dt><dd>{h.iommu_enabled ? t('common.enabled') : t('common.not_detected')}</dd>
                  <dt>{t('node.boot_mode')}</dt><dd>{h.boot_mode.toUpperCase()}</dd>
                  <dt>{t('node.uptime')}</dt><dd>{formatDuration(h.uptime_seconds)}</dd>
                  <dt>{t('node.swap')}</dt><dd>{h.swap_total ? `${formatBytes(h.swap_total - h.swap_free)} / ${formatBytes(h.swap_total)}` : t('common.none')}</dd>
                </dl>
              </div>
            </div>
            <div className="panel">
              <div className="panel-head"><h2>{t('node.versions')}</h2></div>
              <div className="panel-body">
                <dl className="kv">
                  <dt>{t('node.control_plane')}</dt><dd className="mono">{node.data?.control_plane.version}</dd>
                  <dt>{t('node.agent')}</dt><dd className="mono">{h.agent_version}</dd>
                  <dt>libvirt</dt><dd className="mono">{h.libvirt_version || t('common.unavailable')}</dd>
                  <dt>QEMU</dt><dd className="mono">{h.qemu_version || t('common.unavailable')}</dd>
                  <dt>LXC</dt><dd className="mono">{h.lxc_version || t('common.unavailable')}</dd>
                  <dt>{t('node.schema')}</dt><dd className="mono">{node.data?.control_plane.schema_version}</dd>
                </dl>
              </div>
            </div>
          </div>

          <div className="panel">
            <div className="panel-head"><h2>{t('node.filesystems')}</h2></div>
            <table className="data">
              <thead>
                <tr>
                  <th>{t('node.mountpoint')}</th><th>{t('node.device')}</th><th>{t('node.fstype')}</th>
                  <th className="num">{t('storage.used')}</th><th className="num">{t('storage.size')}</th><th style={{ width: 200 }} />
                </tr>
              </thead>
              <tbody>
                {h.filesystems.map((f) => (
                  <tr key={f.mountpoint}>
                    <td className="mono">{f.mountpoint}</td>
                    <td className="mono faint">{f.device}</td>
                    <td>{f.fstype}</td>
                    <td className="num">{formatBytes(f.total - f.free)}</td>
                    <td className="num">{formatBytes(f.total)}</td>
                    <td><Meter value={f.total - f.free} max={f.total} /></td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </div>
      )}
    </div>
  )
}
