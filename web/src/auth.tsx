import { ReactNode, createContext, useCallback, useContext, useEffect, useState } from 'react'
import { api, setCSRF, setUnauthorizedHandler } from './api/client'
import type { Me } from './api/types'
import { setLanguage } from './i18n'

interface AuthState {
  me: Me | null
  ready: boolean
  // login returns an MFA challenge token when a second factor is needed.
  login: (username: string, password: string) => Promise<string | null>
  loginMFA: (token: string, code: string) => Promise<void>
  logout: () => Promise<void>
  refresh: () => Promise<void>
  can: (perm: string) => boolean
}

const Ctx = createContext<AuthState>(null as unknown as AuthState)

export function AuthProvider({ children }: { children: ReactNode }) {
  const [me, setMe] = useState<Me | null>(null)
  const [ready, setReady] = useState(false)

  const apply = (m: Me | null) => {
    setMe(m)
    if (m?.csrf_token) setCSRF(m.csrf_token)
    if (m?.user.language) setLanguage(m.user.language)
    if (m?.product) document.title = m.product
  }

  const refresh = useCallback(async () => {
    try {
      apply(await api.get<Me>('/auth/me'))
    } catch {
      apply(null)
    } finally {
      setReady(true)
    }
  }, [])

  useEffect(() => {
    setUnauthorizedHandler(() => setMe(null))
    void refresh()
  }, [refresh])

  const login = async (username: string, password: string) => {
    const r = await api.post<Me & { mfa_required?: boolean; mfa_token?: string }>('/auth/login', { username, password })
    if (r.mfa_required && r.mfa_token) return r.mfa_token
    apply(r)
    return null
  }
  const loginMFA = async (token: string, code: string) => {
    apply(await api.post<Me>('/auth/login/mfa', { mfa_token: token, code }))
  }
  const logout = async () => {
    try {
      await api.post('/auth/logout')
    } finally {
      setCSRF('')
      setMe(null)
    }
  }
  const can = (perm: string) => !!me?.permissions.includes(perm)
  return <Ctx.Provider value={{ me, ready, login, loginMFA, logout, refresh, can }}>{children}</Ctx.Provider>
}

export const useAuth = () => useContext(Ctx)
