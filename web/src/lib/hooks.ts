import { useCallback, useEffect, useRef, useState } from 'react'
import { ApiError } from '../api/client'

export interface Loadable<T> {
  data: T | undefined
  error: ApiError | undefined
  loading: boolean
  reload: () => void
}

// usePoll loads data and refreshes it at an interval while the tab is
// visible. Errors keep the last good data so tables do not flicker.
export function usePoll<T>(fn: () => Promise<T>, intervalMs: number | null, deps: unknown[] = []): Loadable<T> {
  const [data, setData] = useState<T>()
  const [error, setError] = useState<ApiError>()
  const [loading, setLoading] = useState(true)
  const fnRef = useRef(fn)
  fnRef.current = fn
  const seq = useRef(0)

  const load = useCallback(() => {
    const my = ++seq.current
    fnRef
      .current()
      .then((d) => {
        if (my !== seq.current) return
        setData(d)
        setError(undefined)
      })
      .catch((e) => {
        if (my !== seq.current) return
        setError(e instanceof ApiError ? e : new ApiError(0, 'unknown', String(e)))
      })
      .finally(() => {
        if (my === seq.current) setLoading(false)
      })
  }, [])

  useEffect(() => {
    setLoading(true)
    setData(undefined)
    load()
    if (!intervalMs) return
    const id = window.setInterval(() => {
      if (document.visibilityState === 'visible') load()
    }, intervalMs)
    const onVis = () => document.visibilityState === 'visible' && load()
    document.addEventListener('visibilitychange', onVis)
    return () => {
      window.clearInterval(id)
      document.removeEventListener('visibilitychange', onVis)
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [intervalMs, load, ...deps])

  return { data, error, loading, reload: load }
}

export function useStoredState<T>(key: string, initial: T): [T, (v: T) => void] {
  const [v, setV] = useState<T>(() => {
    try {
      const raw = localStorage.getItem(key)
      return raw ? (JSON.parse(raw) as T) : initial
    } catch {
      return initial
    }
  })
  const set = useCallback(
    (nv: T) => {
      setV(nv)
      try {
        localStorage.setItem(key, JSON.stringify(nv))
      } catch {
        // storage unavailable
      }
    },
    [key],
  )
  return [v, set]
}
