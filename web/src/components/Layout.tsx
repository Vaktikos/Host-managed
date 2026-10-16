import { ReactNode, useEffect, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Link, NavLink, useLocation, useNavigate } from 'react-router-dom'
import { useAuth } from '../auth'
import { useResources } from '../resources'
import { useStoredState } from '../lib/hooks'
import { durationBetween, formatTime } from '../lib/format'
import { Icon, Logo } from './icons'
import { JobStatus, StatusDot } from './ui'

export function Layout({ children }: { children: ReactNode }) {
  const { t } = useTranslation()
  const { me, logout, can } = useAuth()
  const [collapsed, setCollapsed] = useStoredState('hm.sidebar.collapsed', false)
  const [mobileOpen, setMobileOpen] = useState(false)
  const [palette, setPalette] = useState(false)
  const loc = useLocation()

  useEffect(() => setMobileOpen(false), [loc.pathname])
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === 'k') {
        e.preventDefault()
        setPalette(true)
      }
    }
    document.addEventListener('keydown', onKey)
    return () => document.removeEventListener('keydown', onKey)
  }, [])

  const nav = (to: string, icon: string, label: string, perm?: string, count?: number) =>
    (!perm || can(perm)) && (
      <NavLink to={to} end={to === '/'} className={({ isActive }) => `nav-item${isActive ? ' active' : ''}`} title={label}>
        <Icon name={icon} />
        <span className="nav-label">{label}</span>
        {count !== undefined && <span className="nav-count nav-label">{count}</span>}
      </NavLink>
    )

  const { vms, cts } = useResources()

  return (
    <div className={`shell${collapsed ? ' collapsed' : ''}${mobileOpen ? ' mobile-open' : ''}`}>
      <header className="topbar">
        <div className="brand">
          <Logo />
          <span>{me?.product}</span>
        </div>
        <button
          className="btn ghost icon"
          onClick={() => (window.innerWidth <= 860 ? setMobileOpen(!mobileOpen) : setCollapsed(!collapsed))}
          aria-label={t('nav.toggle_sidebar')}
        >
          <Icon name={collapsed ? 'chevronRight' : 'chevronLeft'} />
        </button>
        <button className="search-trigger" onClick={() => setPalette(true)} aria-label={t('search.open')}>
          <Icon name="search" />
          <span>{t('search.placeholder')}</span>
          <kbd>Ctrl K</kbd>
        </button>
        <div className="spacer" />
        <Link to="/account" className="btn ghost" title={t('nav.account')}>
          <Icon name="account" />
          <span className="nav-label">{me?.user.username}</span>
        </Link>
        <button className="btn ghost icon" onClick={() => void logout()} title={t('auth.logout')} aria-label={t('auth.logout')}>
          <Icon name="logout" />
        </button>
      </header>

      <aside className="sidebar" aria-label={t('nav.navigation')}>
        <div className="nav-section">
          <h3>{t('nav.datacenter')}</h3>
          {nav('/', 'overview', t('nav.overview'), 'sys.view')}
          {nav('/vms', 'vm', t('nav.vms'), 'vm.view', vms.data?.length)}
          {nav('/containers', 'container', t('nav.containers'), 'ct.view', cts.data?.length)}
          {nav('/storage', 'storage', t('nav.storage'), 'storage.view')}
          {nav('/network', 'network', t('nav.network'), 'network.view')}
        </div>
        <ResourceTree />
        <div className="nav-section">
          <h3>{t('nav.operations')}</h3>
          {nav('/tasks', 'tasks', t('nav.tasks'), 'jobs.view')}
          {nav('/audit', 'audit', t('nav.audit'), 'sys.audit')}
          {nav('/users', 'users', t('nav.users'), 'sys.users')}
        </div>
        <div className="sidebar-foot">
          {nav('/account', 'account', t('nav.account'))}
          <div className="nav-item faint nav-label" style={{ cursor: 'default', fontSize: 11 }}>
            {me?.product} {me?.version}
          </div>
        </div>
      </aside>

      <main className="main">{children}</main>
      {can('jobs.view') && <TaskBar />}
      {palette && <Palette onClose={() => setPalette(false)} />}
    </div>
  )
}

// ResourceTree: Datacenter → Node → VMs / Containers.
function ResourceTree() {
  const { t } = useTranslation()
  const { vms, cts } = useResources()
  const { can } = useAuth()
  const [open, setOpen] = useStoredState<Record<string, boolean>>('hm.tree', { node: true, vms: true, cts: true })
  const loc = useLocation()
  const toggle = (k: string) => setOpen({ ...open, [k]: !open[k] })
  return (
    <div className="tree" aria-label={t('nav.resources')}>
      <div className="tree-row" onClick={() => toggle('node')}>
        <span className="twisty">{open.node ? '▾' : '▸'}</span>
        <Icon name="server" size={14} />
        <span className="name">{t('nav.local_node')}</span>
      </div>
      {open.node && (
        <>
          {can('vm.view') && (
            <>
              <div className="tree-row" style={{ paddingLeft: 22 }} onClick={() => toggle('vms')}>
                <span className="twisty">{open.vms ? '▾' : '▸'}</span>
                <span className="name faint">{t('nav.vms')}</span>
              </div>
              {open.vms &&
                vms.data?.map((v) => (
                  <Link key={v.uuid} to={`/vms/${encodeURIComponent(v.name)}`} className={`tree-row${loc.pathname === `/vms/${v.name}` ? ' active' : ''}`} style={{ paddingLeft: 40 }}>
                    <StatusDot state={v.state} label="" />
                    <span className="name">{v.name}</span>
                    {v.template && <span className="faint">{t('vm.template_short')}</span>}
                  </Link>
                ))}
            </>
          )}
          {can('ct.view') && (
            <>
              <div className="tree-row" style={{ paddingLeft: 22 }} onClick={() => toggle('cts')}>
                <span className="twisty">{open.cts ? '▾' : '▸'}</span>
                <span className="name faint">{t('nav.containers')}</span>
              </div>
              {open.cts &&
                cts.data?.map((c) => (
                  <Link key={c.name} to={`/containers/${encodeURIComponent(c.name)}`} className={`tree-row${loc.pathname === `/containers/${c.name}` ? ' active' : ''}`} style={{ paddingLeft: 40 }}>
                    <StatusDot state={c.state} label="" />
                    <span className="name">{c.name}</span>
                  </Link>
                ))}
            </>
          )}
        </>
      )}
    </div>
  )
}

function TaskBar() {
  const { t } = useTranslation()
  const { jobs } = useResources()
  const [open, setOpen] = useStoredState('hm.taskbar.open', false)
  const nav = useNavigate()
  const items = jobs.data?.items ?? []
  const running = items.filter((j) => j.status === 'running' || j.status === 'queued').length
  const failed = items.filter((j) => j.status === 'failed').length
  return (
    <div className="taskbar">
      <div className="taskbar-head" onClick={() => setOpen(!open)} role="button" aria-expanded={open}>
        <Icon name={open ? 'chevronDown' : 'chevronRight'} size={12} />
        <span>{t('tasks.recent')}</span>
        {running > 0 && <span className="badge">{t('tasks.running_n', { n: running })}</span>}
        {failed > 0 && <span className="badge err">{t('tasks.failed_n', { n: failed })}</span>}
      </div>
      {open && (
        <div className="taskbar-body">
          <table className="data">
            <tbody>
              {items.slice(0, 10).map((j) => (
                <tr key={j.id} className="clickable" onClick={() => nav(`/tasks/${j.id}`)}>
                  <td style={{ width: 90 }}>{formatTime(j.created_at)}</td>
                  <td>{t(`job.type.${j.type.replace('.', '_')}`)}</td>
                  <td className="mono">{j.target}</td>
                  <td>{j.username}</td>
                  <td>{durationBetween(j.started_at, j.finished_at)}</td>
                  <td><JobStatus status={j.status} />{j.status === 'running' && j.progress != null && <span className="faint"> {Math.round(j.progress)} %</span>}</td>
                  <td className="truncate faint">{j.error}</td>
                </tr>
              ))}
              {items.length === 0 && (
                <tr>
                  <td className="faint">{t('tasks.none')}</td>
                </tr>
              )}
            </tbody>
          </table>
        </div>
      )}
    </div>
  )
}

interface PaletteItem {
  label: string
  kind: string
  to: string
}

function Palette({ onClose }: { onClose: () => void }) {
  const { t } = useTranslation()
  const { vms, cts } = useResources()
  const { can } = useAuth()
  const nav = useNavigate()
  const [q, setQ] = useState('')
  const [idx, setIdx] = useState(0)
  const input = useRef<HTMLInputElement>(null)
  useEffect(() => input.current?.focus(), [])

  const all = useMemo<PaletteItem[]>(() => {
    const pages: PaletteItem[] = [
      { label: t('nav.overview'), kind: t('search.page'), to: '/' },
      { label: t('nav.vms'), kind: t('search.page'), to: '/vms' },
      { label: t('nav.containers'), kind: t('search.page'), to: '/containers' },
      { label: t('nav.storage'), kind: t('search.page'), to: '/storage' },
      { label: t('nav.network'), kind: t('search.page'), to: '/network' },
      { label: t('nav.tasks'), kind: t('search.page'), to: '/tasks' },
      { label: t('nav.account'), kind: t('search.page'), to: '/account' },
    ]
    if (can('sys.audit')) pages.push({ label: t('nav.audit'), kind: t('search.page'), to: '/audit' })
    if (can('sys.users')) pages.push({ label: t('nav.users'), kind: t('search.page'), to: '/users' })
    const res: PaletteItem[] = [
      ...(vms.data ?? []).map((v) => ({ label: v.name, kind: t('search.vm'), to: `/vms/${encodeURIComponent(v.name)}` })),
      ...(cts.data ?? []).map((c) => ({ label: c.name, kind: t('search.container'), to: `/containers/${encodeURIComponent(c.name)}` })),
    ]
    return [...res, ...pages]
  }, [vms.data, cts.data, t, can])

  const items = all.filter((i) => i.label.toLowerCase().includes(q.trim().toLowerCase())).slice(0, 30)
  const go = (i: PaletteItem | undefined) => {
    if (!i) return
    nav(i.to)
    onClose()
  }
  return (
    <div className="overlay" onMouseDown={(e) => e.target === e.currentTarget && onClose()}>
      <div className="palette" role="dialog" aria-label={t('search.open')}>
        <input
          ref={input}
          className="input"
          placeholder={t('search.placeholder')}
          value={q}
          onChange={(e) => {
            setQ(e.target.value)
            setIdx(0)
          }}
          onKeyDown={(e) => {
            if (e.key === 'Escape') onClose()
            if (e.key === 'ArrowDown') setIdx(Math.min(idx + 1, items.length - 1))
            if (e.key === 'ArrowUp') setIdx(Math.max(idx - 1, 0))
            if (e.key === 'Enter') go(items[idx])
          }}
        />
        <div className="palette-list">
          {items.map((i, n) => (
            <div key={i.to} className={`palette-item${n === idx ? ' active' : ''}`} onMouseEnter={() => setIdx(n)} onClick={() => go(i)}>
              {i.label}
              <span className="kind">{i.kind}</span>
            </div>
          ))}
          {items.length === 0 && <div className="empty">{t('search.no_results')}</div>}
        </div>
      </div>
    </div>
  )
}
