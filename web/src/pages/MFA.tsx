import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import QRCode from 'qrcode'
import { ApiError, api } from '../api/client'
import { useAuth } from '../auth'
import { Dialog, ErrorAlert, Field, useToast } from '../components/ui'

// Two-factor authentication (TOTP) in the account page.
export function MFAPanel() {
  const { t } = useTranslation()
  const { me, refresh } = useAuth()
  const [setup, setSetup] = useState(false)
  const [disable, setDisable] = useState(false)
  const enabled = !!me?.user.mfa_enabled
  return (
    <div className="panel">
      <div className="panel-head">
        <h2>{t('mfa.title')}</h2>
        <span className={`badge${enabled ? '' : ' warn'}`}>{enabled ? t('mfa.on') : t('mfa.off')}</span>
        <div className="actions">
          {enabled ? (
            <button className="btn sm danger" onClick={() => setDisable(true)}>{t('mfa.disable')}</button>
          ) : (
            <button className="btn sm primary" onClick={() => setSetup(true)}>{t('mfa.enable')}</button>
          )}
        </div>
      </div>
      <div className="panel-body faint">{t('mfa.text')}</div>
      {setup && <SetupDialog onClose={() => { setSetup(false); void refresh() }} />}
      {disable && <DisableDialog onClose={() => { setDisable(false); void refresh() }} />}
    </div>
  )
}

function SetupDialog({ onClose }: { onClose: () => void }) {
  const { t } = useTranslation()
  const [password, setPassword] = useState('')
  const [secret, setSecret] = useState('')
  const [qr, setQr] = useState('')
  const [code, setCode] = useState('')
  const [codes, setCodes] = useState<string[] | null>(null)
  const [err, setErr] = useState<ApiError>()
  const start = async () => {
    setErr(undefined)
    try {
      const r = await api.post<{ secret: string; uri: string }>('/auth/me/mfa/setup', { password })
      setSecret(r.secret)
      setQr(await QRCode.toDataURL(r.uri, { margin: 1, width: 200, color: { dark: '#000000', light: '#ffffff' } }))
    } catch (e) {
      setErr(e as ApiError)
    }
  }
  const confirm = async () => {
    setErr(undefined)
    try {
      const r = await api.post<{ recovery_codes: string[] }>('/auth/me/mfa/enable', { code })
      setCodes(r.recovery_codes)
    } catch (e) {
      setErr(e as ApiError)
    }
  }
  let body
  let foot
  if (codes) {
    body = (
      <>
        <div className="alert warn">{t('mfa.recovery_once')}</div>
        <div className="log mono">{codes.join('\n')}</div>
      </>
    )
    foot = <button className="btn primary" onClick={onClose}>{t('mfa.saved_codes')}</button>
  } else if (secret) {
    body = (
      <>
        <div>{t('mfa.scan')}</div>
        <div className="row" style={{ alignItems: 'flex-start', gap: 16 }}>
          {qr && <img src={qr} width={200} height={200} alt="QR" style={{ borderRadius: 3 }} />}
          <div className="stack" style={{ gap: 6 }}>
            <span className="faint">{t('mfa.manual')}</span>
            <code style={{ wordBreak: 'break-all' }}>{secret}</code>
          </div>
        </div>
        <Field label={t('auth.mfa_code')} error={err?.field === 'code' ? t('mfa.wrong_code') : undefined} htmlFor="mfa-code">
          <input id="mfa-code" className="input mono" inputMode="numeric" value={code} onChange={(e) => setCode(e.target.value)} autoComplete="one-time-code" />
        </Field>
      </>
    )
    foot = <><button className="btn" onClick={onClose}>{t('common.cancel')}</button><button className="btn primary" disabled={code.trim().length !== 6} onClick={confirm}>{t('mfa.enable')}</button></>
  } else {
    body = (
      <Field label={t('account.current_password')} error={err?.field === 'password' ? t('account.wrong_password') : undefined} htmlFor="mfa-pw">
        <input id="mfa-pw" className="input" type="password" value={password} onChange={(e) => setPassword(e.target.value)} autoComplete="current-password" />
      </Field>
    )
    foot = <><button className="btn" onClick={onClose}>{t('common.cancel')}</button><button className="btn primary" disabled={!password} onClick={start}>{t('mfa.continue')}</button></>
  }
  return (
    <Dialog title={t('mfa.enable')} onClose={codes ? () => {} : onClose} footer={foot}>
      {body}
      {err && !err.field && <ErrorAlert error={err} />}
    </Dialog>
  )
}

function DisableDialog({ onClose }: { onClose: () => void }) {
  const { t } = useTranslation()
  const toast = useToast()
  const [password, setPassword] = useState('')
  const [code, setCode] = useState('')
  const [err, setErr] = useState<ApiError>()
  useEffect(() => setErr(undefined), [password, code])
  const submit = async () => {
    try {
      await api.post('/auth/me/mfa/disable', { password, code })
      toast('ok', t('mfa.disabled'))
      onClose()
    } catch (e) {
      setErr(e as ApiError)
    }
  }
  return (
    <Dialog title={t('mfa.disable')} onClose={onClose} footer={<><button className="btn" onClick={onClose}>{t('common.cancel')}</button><button className="btn danger" disabled={!password || code.trim().length < 6} onClick={submit}>{t('mfa.disable')}</button></>}>
      <Field label={t('account.current_password')} error={err?.field === 'password' ? t('account.wrong_password') : undefined} htmlFor="md-pw">
        <input id="md-pw" className="input" type="password" value={password} onChange={(e) => setPassword(e.target.value)} />
      </Field>
      <Field label={t('auth.mfa_code')} hint={t('auth.mfa_hint')} error={err?.field === 'code' ? t('mfa.wrong_code') : undefined} htmlFor="md-code">
        <input id="md-code" className="input mono" value={code} onChange={(e) => setCode(e.target.value)} autoComplete="one-time-code" />
      </Field>
      {err && !err.field && <ErrorAlert error={err} />}
    </Dialog>
  )
}
