import { FormEvent, useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { ApiError, api } from '../api/client'
import { useAuth } from '../auth'
import { Logo } from '../components/icons'
import { setLanguage } from '../i18n'

export function Login() {
  const { t, i18n } = useTranslation()
  const { login } = useAuth()
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const [product, setProduct] = useState('')

  useEffect(() => {
    api
      .get<{ product: string; default_language: 'de' | 'en' }>('/info')
      .then((i) => {
        setProduct(i.product)
        document.title = i.product
        try {
          if (!localStorage.getItem('hm.lang')) setLanguage(i.default_language)
        } catch {
          // ignore
        }
      })
      .catch(() => setProduct(''))
  }, [])

  const submit = async (e: FormEvent) => {
    e.preventDefault()
    setBusy(true)
    setError('')
    try {
      await login(username, password)
    } catch (err) {
      const code = err instanceof ApiError ? err.code : 'network'
      setError(t(`errors.${code}`, { defaultValue: t('errors.login_failed') }))
      setBusy(false)
    }
  }

  return (
    <div className="login">
      <div className="login-box">
        <div className="brand">
          <Logo size={24} />
          <span>{product}</span>
        </div>
        <div className="panel">
          <div className="panel-head">
            <h2>{t('auth.sign_in')}</h2>
          </div>
          <div className="panel-body">
            <form onSubmit={submit}>
              <div className="field">
                <label htmlFor="u">{t('auth.username')}</label>
                <input id="u" className="input" autoComplete="username" value={username} onChange={(e) => setUsername(e.target.value)} autoFocus required />
              </div>
              <div className="field">
                <label htmlFor="p">{t('auth.password')}</label>
                <input id="p" className="input" type="password" autoComplete="current-password" value={password} onChange={(e) => setPassword(e.target.value)} required />
              </div>
              {error && <div className="alert err" role="alert">{error}</div>}
              <button className="btn primary" type="submit" disabled={busy || !username || !password}>
                {busy ? t('auth.signing_in') : t('auth.sign_in')}
              </button>
            </form>
          </div>
        </div>
        <div className="login-foot">
          <span>{location.protocol === 'https:' ? t('auth.secure_hint') : t('auth.insecure_hint')}</span>
          <span className="row">
            {(['de', 'en'] as const).map((l) => (
              <button key={l} className={`btn ghost sm${i18n.language === l ? ' active' : ''}`} onClick={() => setLanguage(l)} aria-pressed={i18n.language === l}>
                {l.toUpperCase()}
              </button>
            ))}
          </span>
        </div>
      </div>
    </div>
  )
}
