import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { ApiError, api } from '../api/client'
import type { APIToken, SessionInfo } from '../api/types'
import { useAuth } from '../auth'
import { Icon } from '../components/icons'
import { ConfirmDialog, Copy, Dialog, ErrorAlert, Field, useErrorText, useToast } from '../components/ui'
import { setLanguage } from '../i18n'
import { formatDate } from '../lib/format'
import { usePoll } from '../lib/hooks'
import { MFAPanel } from './MFA'

export function Account() {
  const { t, i18n } = useTranslation()
  const { me, refresh } = useAuth()
  const toast = useToast()
  const errText = useErrorText()
  const sessions = usePoll(() => api.get<SessionInfo[]>('/auth/me/sessions'), null)
  const tokens = usePoll(() => api.get<APIToken[]>('/auth/me/tokens'), null)
  const [cur, setCur] = useState('')
  const [pw, setPw] = useState('')
  const [pw2, setPw2] = useState('')
  const [pwErr, setPwErr] = useState<ApiError>()
  const [newToken, setNewToken] = useState(false)
  const [revoking, setRevoking] = useState<APIToken | null>(null)

  const changeLang = async (lang: 'de' | 'en') => {
    try {
      await api.put('/auth/me/language', { language: lang })
      setLanguage(lang)
      await refresh()
    } catch (e) {
      toast('err', errText(e))
    }
  }
  const changePw = async () => {
    setPwErr(undefined)
    try {
      await api.put('/auth/me/password', { current_password: cur, new_password: pw })
      setCur('')
      setPw('')
      setPw2('')
      toast('ok', t('account.password_changed'))
      sessions.reload()
    } catch (e) {
      setPwErr(e as ApiError)
    }
  }
  const revokeSession = async (s: SessionInfo) => {
    try {
      await api.del(`/auth/me/sessions/${s.id}`)
      sessions.reload()
    } catch (e) {
      toast('err', errText(e))
    }
  }

  return (
    <div className="page">
      <div className="page-head">
        <h1>{t('nav.account')}</h1>
        <span className="sub">{me?.user.username} · {me && t(`roles.${me.user.role.replace('-', '_')}`)}</span>
      </div>
      <div className="grid grid-2">
        <div className="panel">
          <div className="panel-head"><h2>{t('account.language')}</h2></div>
          <div className="panel-body row">
            <select className="select" style={{ width: 200 }} value={i18n.language} onChange={(e) => void changeLang(e.target.value as 'de' | 'en')} aria-label={t('account.language')}>
              <option value="de">Deutsch</option>
              <option value="en">English</option>
            </select>
          </div>
        </div>
        <div className="panel">
          <div className="panel-head"><h2>{t('account.change_password')}</h2></div>
          <div className="panel-body">
            <div className="form-grid">
              <Field label={t('account.current_password')} className="full" htmlFor="a-cur" error={pwErr?.field === 'current_password' ? t('account.wrong_password') : undefined}>
                <input id="a-cur" className="input" type="password" value={cur} onChange={(e) => setCur(e.target.value)} autoComplete="current-password" />
              </Field>
              <Field label={t('account.new_password')} hint={t('validation.password_policy')} htmlFor="a-new" error={pwErr?.field === 'new_password' ? pwErr.message : undefined}>
                <input id="a-new" className="input" type="password" value={pw} onChange={(e) => setPw(e.target.value)} autoComplete="new-password" />
              </Field>
              <Field label={t('account.repeat_password')} htmlFor="a-rep" error={pw2 && pw2 !== pw ? t('account.mismatch') : undefined}>
                <input id="a-rep" className="input" type="password" value={pw2} onChange={(e) => setPw2(e.target.value)} autoComplete="new-password" />
              </Field>
            </div>
            {pwErr && !pwErr.field && <ErrorAlert error={pwErr} />}
            <div className="row" style={{ marginTop: 12 }}>
              <span className="faint grow">{t('account.password_signs_out')}</span>
              <button className="btn primary" disabled={!cur || pw.length < 12 || pw !== pw2} onClick={changePw}>{t('account.change_password')}</button>
            </div>
          </div>
        </div>
      </div>

      <div style={{ marginTop: 12 }}><MFAPanel /></div>

      <div className="panel" style={{ marginTop: 12 }}>
        <div className="panel-head"><h2>{t('account.sessions')}</h2></div>
        <table className="data">
          <thead><tr><th>{t('account.last_seen')}</th><th>{t('audit.ip')}</th><th>{t('account.client')}</th><th>{t('account.expires')}</th><th /></tr></thead>
          <tbody>
            {sessions.data?.map((s) => (
              <tr key={s.id}>
                <td>{formatDate(s.last_seen)}</td>
                <td className="mono">{s.ip}</td>
                <td className="truncate faint" title={s.user_agent}>{s.user_agent}</td>
                <td>{formatDate(s.expires_at)}</td>
                <td className="right">{s.current ? <span className="badge">{t('account.this_session')}</span> : <button className="btn sm" onClick={() => revokeSession(s)}>{t('account.sign_out')}</button>}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>

      <div className="panel" style={{ marginTop: 12 }}>
        <div className="panel-head">
          <h2>{t('account.tokens')}</h2>
          <div className="actions"><button className="btn sm" onClick={() => setNewToken(true)}><Icon name="plus" size={12} /> {t('account.new_token')}</button></div>
        </div>
        <table className="data">
          <thead><tr><th>{t('common.name')}</th><th>ID</th><th>{t('users.created')}</th><th>{t('account.expires')}</th><th>{t('account.last_used')}</th><th /></tr></thead>
          <tbody>
            {tokens.data?.map((tk) => (
              <tr key={tk.id}>
                <td>{tk.name}</td>
                <td className="mono faint">{tk.id}</td>
                <td>{formatDate(tk.created_at)}</td>
                <td>{tk.expires_at ? formatDate(tk.expires_at) : t('account.never')}</td>
                <td>{formatDate(tk.last_used)}</td>
                <td className="right"><button className="btn sm danger" onClick={() => setRevoking(tk)}>{t('account.revoke')}</button></td>
              </tr>
            ))}
          </tbody>
        </table>
        {tokens.data?.length === 0 && <div className="empty">{t('account.no_tokens')}</div>}
        <div className="panel-body faint">{t('account.tokens_hint')}</div>
      </div>
      {newToken && <NewToken onClose={() => { setNewToken(false); tokens.reload() }} />}
      {revoking && (
        <ConfirmDialog
          title={t('account.revoke')}
          message={t('account.revoke_text', { name: revoking.name })}
          confirmLabel={t('account.revoke')}
          onConfirm={async () => {
            await api.del(`/auth/me/tokens/${revoking.id}`)
            tokens.reload()
          }}
          onClose={() => setRevoking(null)}
        />
      )}
    </div>
  )
}

function NewToken({ onClose }: { onClose: () => void }) {
  const { t } = useTranslation()
  const [name, setName] = useState('')
  const [days, setDays] = useState(90)
  const [token, setToken] = useState('')
  const [err, setErr] = useState<ApiError>()
  const create = async () => {
    try {
      const r = await api.post<{ token: string }>('/auth/me/tokens', { name, expires_in_days: days })
      setToken(r.token)
    } catch (e) {
      setErr(e as ApiError)
    }
  }
  return (
    <Dialog
      title={t('account.new_token')}
      onClose={onClose}
      footer={token ? <button className="btn primary" onClick={onClose}>{t('common.done')}</button> : <><button className="btn" onClick={onClose}>{t('common.cancel')}</button><button className="btn primary" disabled={!name.trim()} onClick={create}>{t('common.create')}</button></>}
    >
      {token ? (
        <>
          <div className="alert warn">{t('account.token_once')}</div>
          <div className="log mono">{token} <Copy value={token} /></div>
          <div className="faint">{t('account.token_usage')}</div>
          <div className="log mono">hostctl --url https://{location.host} --token $HM_TOKEN vm list</div>
        </>
      ) : (
        <div className="form-grid">
          <Field label={t('common.name')} htmlFor="tk-name"><input id="tk-name" className="input" value={name} onChange={(e) => setName(e.target.value)} placeholder="automation" /></Field>
          <Field label={t('account.expires_in_days')} hint={t('account.zero_never')} htmlFor="tk-days"><input id="tk-days" className="input" type="number" min={0} max={3650} value={days} onChange={(e) => setDays(Number(e.target.value))} /></Field>
        </div>
      )}
      <ErrorAlert error={err} />
    </Dialog>
  )
}
