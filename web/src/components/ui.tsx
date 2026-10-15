import { ReactNode, createContext, useCallback, useContext, useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { ApiError } from '../api/client'
import { Icon } from './icons'

// Status mapping: one place decides which states carry which colour.
const stateClass: Record<string, string> = {
  running: 'ok', paused: 'warn', frozen: 'warn', suspended: 'warn', blocked: 'warn',
  'shutting-down': 'info', stopping: 'info', starting: 'info',
  shutoff: '', stopped: '', crashed: 'err', unknown: '',
  queued: 'info', succeeded: 'ok', failed: 'err', cancelled: 'warn', ok: 'ok', error: 'err',
  up: 'ok', down: '', active: 'ok', inactive: '',
}

export function StatusDot({ state, label }: { state: string; label?: string }) {
  const { t } = useTranslation()
  const cls = stateClass[state] ?? ''
  const pulse = state === 'queued' || state === 'shutting-down' ? ' pulse' : ''
  return (
    <span className="status">
      <span className={`dot ${cls}${pulse}`} />
      {label ?? t(`state.${state.replace('-', '_')}`)}
    </span>
  )
}

export function JobStatus({ status }: { status: string }) {
  const { t } = useTranslation()
  return (
    <span className="status">
      <span className={`dot ${stateClass[status] ?? ''}${status === 'running' ? ' info pulse' : ''}`} />
      {t(`job.status.${status}`)}
    </span>
  )
}

export function ErrorAlert({ error, title }: { error: ApiError | Error | undefined; title?: string }) {
  const { t } = useTranslation()
  if (!error) return null
  const ae = error instanceof ApiError ? error : undefined
  const known = ae && t(`errors.${ae.code}`, { defaultValue: '' })
  return (
    <div className="alert err" role="alert">
      <div className="title">{title ?? t('errors.title')}</div>
      <div>{known || error.message}</div>
      {ae?.requestId && <div className="meta">{t('errors.request_id')}: <span className="mono">{ae.requestId}</span></div>}
    </div>
  )
}

export function Dialog({ title, onClose, children, footer, wide }: { title: string; onClose: () => void; children: ReactNode; footer?: ReactNode; wide?: boolean }) {
  const { t } = useTranslation()
  const ref = useRef<HTMLDivElement>(null)
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => e.key === 'Escape' && onClose()
    document.addEventListener('keydown', onKey)
    const first = ref.current?.querySelector<HTMLElement>('input, select, textarea, button.primary')
    first?.focus()
    return () => document.removeEventListener('keydown', onKey)
  }, [onClose])
  return (
    <div className="overlay" onMouseDown={(e) => e.target === e.currentTarget && onClose()}>
      <div className={`dialog${wide ? ' wide' : ''}`} role="dialog" aria-modal="true" aria-label={title} ref={ref}>
        <div className="dialog-head">
          <h2>{title}</h2>
          <button className="btn ghost icon" onClick={onClose} aria-label={t('common.close')}>
            <Icon name="close" />
          </button>
        </div>
        <div className="dialog-body">{children}</div>
        {footer && <div className="dialog-foot">{footer}</div>}
      </div>
    </div>
  )
}

// ConfirmDialog for destructive actions. With confirmText the user has to
// type the resource name.
export function ConfirmDialog({
  title, message, confirmLabel, confirmText, danger = true, onConfirm, onClose, children,
}: {
  title: string; message: ReactNode; confirmLabel: string; confirmText?: string; danger?: boolean
  onConfirm: () => Promise<void> | void; onClose: () => void; children?: ReactNode
}) {
  const { t } = useTranslation()
  const [typed, setTyped] = useState('')
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState<Error>()
  const ok = !confirmText || typed === confirmText
  const run = async () => {
    setBusy(true)
    setErr(undefined)
    try {
      await onConfirm()
      onClose()
    } catch (e) {
      setErr(e as Error)
      setBusy(false)
    }
  }
  return (
    <Dialog
      title={title}
      onClose={onClose}
      footer={
        <>
          <button className="btn" onClick={onClose}>{t('common.cancel')}</button>
          <button className={`btn ${danger ? 'danger' : 'primary'}`} disabled={!ok || busy} onClick={run}>{confirmLabel}</button>
        </>
      }
    >
      <div>{message}</div>
      {children}
      {confirmText && (
        <div className="field">
          <label htmlFor="confirm-input">{t('common.type_to_confirm', { name: confirmText })}</label>
          <input id="confirm-input" className="input mono" value={typed} onChange={(e) => setTyped(e.target.value)} autoComplete="off" />
        </div>
      )}
      <ErrorAlert error={err} />
    </Dialog>
  )
}

export function Field({ label, error, hint, children, className, htmlFor }: { label: string; error?: string; hint?: ReactNode; children: ReactNode; className?: string; htmlFor?: string }) {
  return (
    <div className={`field ${className ?? ''}`}>
      <label htmlFor={htmlFor}>{label}</label>
      {children}
      {error ? <div className="error">{error}</div> : hint ? <div className="hint">{hint}</div> : null}
    </div>
  )
}

export interface MenuItem {
  label: string
  onClick: () => void
  disabled?: boolean
  danger?: boolean
  separator?: boolean
}

export function Menu({ items, label, children }: { items: MenuItem[]; label: string; children?: ReactNode }) {
  const [open, setOpen] = useState(false)
  const ref = useRef<HTMLDivElement>(null)
  useEffect(() => {
    if (!open) return
    const close = (e: MouseEvent) => !ref.current?.contains(e.target as Node) && setOpen(false)
    const esc = (e: KeyboardEvent) => e.key === 'Escape' && setOpen(false)
    document.addEventListener('mousedown', close)
    document.addEventListener('keydown', esc)
    return () => {
      document.removeEventListener('mousedown', close)
      document.removeEventListener('keydown', esc)
    }
  }, [open])
  return (
    <div className="menu-wrap" ref={ref}>
      <button className="btn" aria-haspopup="menu" aria-expanded={open} onClick={(e) => { e.stopPropagation(); setOpen(!open) }} aria-label={label}>
        {children ?? <Icon name="more" />}
      </button>
      {open && (
        <div className="menu" role="menu">
          {items.map((it, i) =>
            it.separator ? (
              <hr key={i} />
            ) : (
              <button key={i} role="menuitem" className={it.danger ? 'danger' : ''} disabled={it.disabled} onClick={(e) => { e.stopPropagation(); setOpen(false); it.onClick() }}>
                {it.label}
              </button>
            ),
          )}
        </div>
      )}
    </div>
  )
}

export function Tabs({ tabs, active, onChange }: { tabs: { id: string; label: string }[]; active: string; onChange: (id: string) => void }) {
  return (
    <div className="tabs" role="tablist">
      {tabs.map((t) => (
        <button key={t.id} role="tab" aria-selected={active === t.id} className={`tab${active === t.id ? ' active' : ''}`} onClick={() => onChange(t.id)}>
          {t.label}
        </button>
      ))}
    </div>
  )
}

export function Copy({ value }: { value: string }) {
  const { t } = useTranslation()
  const [done, setDone] = useState(false)
  const copy = async (e: React.MouseEvent) => {
    e.stopPropagation()
    try {
      await navigator.clipboard.writeText(value)
      setDone(true)
      setTimeout(() => setDone(false), 1200)
    } catch {
      // clipboard requires a secure context
    }
  }
  return (
    <button className="copy" onClick={copy} title={t('common.copy')} aria-label={t('common.copy')}>
      {done ? t('common.copied') : <Icon name="copy" size={12} />}
    </button>
  )
}

export function Meter({ value, max }: { value: number; max: number }) {
  const pct = max > 0 ? Math.min(100, (value / max) * 100) : 0
  return (
    <div className={`meter${pct > 90 ? ' err' : pct > 75 ? ' warn' : ''}`} role="meter" aria-valuenow={Math.round(pct)} aria-valuemin={0} aria-valuemax={100}>
      <div style={{ width: `${pct}%` }} />
    </div>
  )
}

// Sparkline draws real samples only; missing points are gaps.
export function Sparkline({ values, max }: { values: (number | null)[]; max?: number }) {
  if (values.length < 2) return <svg className="spark" />
  const top = max ?? Math.max(1, ...values.map((v) => v ?? 0))
  const w = 200
  const h = 36
  const step = w / (values.length - 1)
  let d = ''
  values.forEach((v, i) => {
    if (v === null) return
    const x = i * step
    const y = h - 2 - (Math.min(v, top) / top) * (h - 4)
    d += (d === '' || values[i - 1] === null ? 'M' : 'L') + x.toFixed(1) + ' ' + y.toFixed(1)
  })
  return (
    <svg className="spark" viewBox={`0 0 ${w} ${h}`} preserveAspectRatio="none" aria-hidden="true">
      <path d={d} fill="none" stroke="var(--accent)" strokeWidth="1.2" vectorEffect="non-scaling-stroke" />
    </svg>
  )
}

interface Toast {
  id: number
  kind: 'ok' | 'err' | 'info'
  text: string
}

const ToastCtx = createContext<(kind: Toast['kind'], text: string) => void>(() => {})

export function ToastProvider({ children }: { children: ReactNode }) {
  const [toasts, setToasts] = useState<Toast[]>([])
  const push = useCallback((kind: Toast['kind'], text: string) => {
    const id = Date.now() + Math.random()
    setToasts((ts) => [...ts.slice(-3), { id, kind, text }])
    setTimeout(() => setToasts((ts) => ts.filter((x) => x.id !== id)), kind === 'err' ? 8000 : 4000)
  }, [])
  return (
    <ToastCtx.Provider value={push}>
      {children}
      <div className="toasts" aria-live="polite">
        {toasts.map((t) => (
          <div key={t.id} className={`toast ${t.kind}`}>{t.text}</div>
        ))}
      </div>
    </ToastCtx.Provider>
  )
}

export const useToast = () => useContext(ToastCtx)

// errorText turns an API error into a translated message.
export function useErrorText() {
  const { t } = useTranslation()
  return (e: unknown) => {
    if (e instanceof ApiError) {
      const known = t(`errors.${e.code}`, { defaultValue: '' })
      return known || e.message
    }
    return String(e)
  }
}
