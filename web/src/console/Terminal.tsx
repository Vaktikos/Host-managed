import { useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Terminal as XTerm } from '@xterm/xterm'
import { FitAddon } from '@xterm/addon-fit'
import '@xterm/xterm/css/xterm.css'
import { wsURL } from '../api/client'

type State = 'connecting' | 'connected' | 'disconnected' | 'error'

// Terminal attaches to a container console. Keystrokes are sent as binary
// messages, terminal size changes as JSON text messages.
export function Terminal({ path }: { path: string }) {
  const { t } = useTranslation()
  const box = useRef<HTMLDivElement>(null)
  const [state, setState] = useState<State>('connecting')
  const [attempt, setAttempt] = useState(0)

  useEffect(() => {
    if (!box.current) return
    const term = new XTerm({
      fontFamily: 'ui-monospace, SFMono-Regular, Menlo, Consolas, monospace',
      fontSize: 13,
      cursorBlink: true,
      theme: { background: '#060708', foreground: '#d9dce1', cursor: '#8aa7c8', selectionBackground: '#2b3d52' },
      scrollback: 5000,
    })
    const fit = new FitAddon()
    term.loadAddon(fit)
    term.open(box.current)
    fit.fit()
    setState('connecting')
    const ws = new WebSocket(wsURL(path))
    ws.binaryType = 'arraybuffer'
    const enc = new TextEncoder()
    const sendSize = () => {
      if (ws.readyState === WebSocket.OPEN) ws.send(JSON.stringify({ type: 'resize', cols: term.cols, rows: term.rows }))
    }
    ws.onopen = () => {
      setState('connected')
      sendSize()
      term.focus()
    }
    ws.onmessage = (e) => term.write(new Uint8Array(e.data as ArrayBuffer))
    ws.onclose = (e) => setState(e.wasClean ? 'disconnected' : 'error')
    ws.onerror = () => setState('error')
    const d1 = term.onData((d) => ws.readyState === WebSocket.OPEN && ws.send(enc.encode(d)))
    const d2 = term.onResize(sendSize)
    const ro = new ResizeObserver(() => fit.fit())
    ro.observe(box.current)
    return () => {
      ro.disconnect()
      d1.dispose()
      d2.dispose()
      ws.close()
      term.dispose()
    }
  }, [path, attempt])

  return (
    <div>
      <div className="console-bar">
        <span className="status">
          <span className={`dot ${state === 'connected' ? 'ok' : state === 'error' ? 'err' : state === 'connecting' ? 'info pulse' : ''}`} />
          {t(`console.${state}`)}
        </span>
        <div className="grow" />
        <button className="btn sm" onClick={() => setAttempt(attempt + 1)}>{t('console.reconnect')}</button>
      </div>
      <div className="console-frame term" ref={box} />
    </div>
  )
}
