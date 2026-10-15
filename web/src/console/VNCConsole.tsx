import { useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
// noVNC ships CommonJS in lib/; Vite pre-bundles it.
import RFB from '@novnc/novnc/lib/rfb.js'
import { wsURL } from '../api/client'

type State = 'connecting' | 'connected' | 'disconnected' | 'error'

export function VNCConsole({ name }: { name: string }) {
  const { t } = useTranslation()
  const box = useRef<HTMLDivElement>(null)
  const rfb = useRef<RFB | null>(null)
  const [state, setState] = useState<State>('connecting')
  const [attempt, setAttempt] = useState(0)
  const [scale, setScale] = useState(true)

  useEffect(() => {
    if (!box.current) return
    setState('connecting')
    const r = new RFB(box.current, wsURL(`/vms/${encodeURIComponent(name)}/console`), { wsProtocols: ['binary'] })
    r.scaleViewport = scale
    r.resizeSession = false
    r.background = '#000'
    r.addEventListener('connect', () => setState('connected'))
    r.addEventListener('disconnect', (e: Event) => setState((e as CustomEvent).detail?.clean ? 'disconnected' : 'error'))
    rfb.current = r
    return () => {
      try {
        r.disconnect()
      } catch {
        // already closed
      }
      rfb.current = null
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [name, attempt])

  useEffect(() => {
    if (rfb.current) rfb.current.scaleViewport = scale
  }, [scale])

  return (
    <div>
      <div className="console-bar">
        <span className="status">
          <span className={`dot ${state === 'connected' ? 'ok' : state === 'error' ? 'err' : state === 'connecting' ? 'info pulse' : ''}`} />
          {t(`console.${state}`)}
        </span>
        <div className="grow" />
        <label className="check">
          <input type="checkbox" checked={scale} onChange={(e) => setScale(e.target.checked)} />
          {t('console.scale')}
        </label>
        <button className="btn sm" disabled={state !== 'connected'} onClick={() => rfb.current?.sendCtrlAltDel()}>Ctrl+Alt+Del</button>
        <button className="btn sm" disabled={state !== 'connected'} onClick={() => rfb.current?.focus()}>{t('console.focus')}</button>
        <button className="btn sm" onClick={() => setAttempt(attempt + 1)}>{t('console.reconnect')}</button>
      </div>
      <div className="console-frame" ref={box} />
    </div>
  )
}
