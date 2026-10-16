import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { ApiError, api } from '../api/client'
import type { RoleInfo, User } from '../api/types'
import { useAuth } from '../auth'
import { DataTable, Column } from '../components/DataTable'
import { Icon } from '../components/icons'
import { ConfirmDialog, Dialog, ErrorAlert, Field, Menu, StatusDot, useErrorText, useToast } from '../components/ui'
import { formatDate } from '../lib/format'
import { usePoll } from '../lib/hooks'

export function Users() {
  const { t } = useTranslation()
  const { me } = useAuth()
  const toast = useToast()
  const errText = useErrorText()
  const users = usePoll(() => api.get<User[]>('/users'), null)
  const roles = usePoll(() => api.get<RoleInfo[]>('/roles'), null)
  const [creating, setCreating] = useState(false)
  const [resetting, setResetting] = useState<User | null>(null)
  const [deleting, setDeleting] = useState<User | null>(null)
  const [roleInfo, setRoleInfo] = useState(false)
  const [resettingMFA, setResettingMFA] = useState<User | null>(null)

  const update = async (u: User, body: object) => {
    try {
      await api.put(`/users/${u.id}`, body)
      toast('ok', t('common.saved'))
      users.reload()
    } catch (e) {
      toast('err', errText(e))
    }
  }

  const columns: Column<User>[] = [
    { id: 'state', label: t('common.status'), sort: (u) => (u.disabled ? 1 : 0), render: (u) => <StatusDot state={u.disabled ? 'inactive' : 'active'} label={u.disabled ? t('users.disabled') : t('common.active')} /> },
    { id: 'name', label: t('auth.username'), sort: (u) => u.username, render: (u) => <strong style={{ fontWeight: 500 }}>{u.username}</strong> },
    { id: 'role', label: t('users.role'), sort: (u) => u.role, render: (u) => t(`roles.${u.role.replace('-', '_')}`) },
    { id: 'mfa', label: t('mfa.short'), sort: (u) => (u.mfa_enabled ? 1 : 0), render: (u) => (u.mfa_enabled ? t('mfa.on') : <span className="faint">{t('mfa.off')}</span>) },
    { id: 'lang', label: t('account.language'), sort: (u) => u.language, render: (u) => u.language.toUpperCase() },
    { id: 'created', label: t('users.created'), sort: (u) => u.created_at, render: (u) => formatDate(u.created_at) },
    { id: 'pw', label: t('users.password_changed'), sort: (u) => u.password_changed_at, render: (u) => formatDate(u.password_changed_at), hidden: true },
  ]

  return (
    <div className="page">
      <div className="page-head">
        <h1>{t('nav.users')}</h1>
        <div className="actions">
          <button className="btn" onClick={() => setRoleInfo(true)}>{t('users.roles_overview')}</button>
          <button className="btn primary" onClick={() => setCreating(true)}><Icon name="plus" /> {t('users.create')}</button>
        </div>
      </div>
      <ErrorAlert error={users.error} />
      <div className="panel">
        <DataTable
          id="users"
          columns={columns}
          rows={users.data}
          rowKey={(u) => String(u.id)}
          rowMenu={(u) => (
            <Menu
              label={t('common.actions')}
              items={[
                ...(roles.data ?? []).map((r) => ({ label: t('users.set_role', { role: t(`roles.${r.name.replace('-', '_')}`) }), onClick: () => update(u, { role: r.name }), disabled: r.name === u.role })),
                { label: '', onClick: () => {}, separator: true },
                { label: t('users.reset_password'), onClick: () => setResetting(u) },
                { label: t('mfa.reset'), onClick: () => setResettingMFA(u), disabled: !u.mfa_enabled },
                { label: u.disabled ? t('users.enable') : t('users.disable'), onClick: () => update(u, { disabled: !u.disabled }), disabled: u.id === me?.user.id },
                { label: t('common.delete'), onClick: () => setDeleting(u), danger: true, disabled: u.id === me?.user.id },
              ]}
            />
          )}
        />
      </div>
      <div className="alert info" style={{ marginTop: 12 }}>{t('users.scope_hint')}</div>
      {creating && <CreateUser roles={roles.data ?? []} onClose={() => setCreating(false)} onDone={users.reload} />}
      {resetting && <ResetPassword user={resetting} onClose={() => setResetting(null)} />}
      {deleting && (
        <ConfirmDialog
          title={t('users.delete_title', { name: deleting.username })}
          message={t('users.delete_text')}
          confirmLabel={t('common.delete')}
          confirmText={deleting.username}
          onConfirm={async () => {
            await api.del(`/users/${deleting.id}`)
            users.reload()
          }}
          onClose={() => setDeleting(null)}
        />
      )}
      {resettingMFA && (
        <ConfirmDialog
          title={t('mfa.reset')}
          message={t('mfa.reset_text', { name: resettingMFA.username })}
          confirmLabel={t('mfa.reset')}
          onConfirm={async () => {
            await api.del(`/users/${resettingMFA.id}/mfa`)
            users.reload()
          }}
          onClose={() => setResettingMFA(null)}
        />
      )}
      {roleInfo && (
        <Dialog title={t('users.roles_overview')} onClose={() => setRoleInfo(false)} wide>
          <table className="data">
            <thead><tr><th>{t('users.role')}</th><th>{t('users.permissions')}</th></tr></thead>
            <tbody>
              {roles.data?.map((r) => (
                <tr key={r.name}>
                  <td style={{ verticalAlign: 'top', paddingTop: 6 }}>{t(`roles.${r.name.replace('-', '_')}`)}</td>
                  <td style={{ whiteSpace: 'normal', padding: '6px 12px' }}>{r.permissions.map((p) => <span key={p} className="tag mono">{p}</span>)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </Dialog>
      )}
    </div>
  )
}

function CreateUser({ roles, onClose, onDone }: { roles: RoleInfo[]; onClose: () => void; onDone: () => void }) {
  const { t, i18n } = useTranslation()
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [role, setRole] = useState('operator')
  const [lang, setLang] = useState(i18n.language)
  const [err, setErr] = useState<ApiError>()
  const valid = /^[a-z][a-z0-9._-]{1,31}$/.test(username) && password.length >= 12
  const submit = async () => {
    try {
      await api.post('/users', { username, password, role, language: lang })
      onDone()
      onClose()
    } catch (e) {
      setErr(e as ApiError)
    }
  }
  return (
    <Dialog title={t('users.create')} onClose={onClose} footer={<><button className="btn" onClick={onClose}>{t('common.cancel')}</button><button className="btn primary" disabled={!valid} onClick={submit}>{t('users.create')}</button></>}>
      <div className="form-grid">
        <Field label={t('auth.username')} hint={t('users.username_hint')} error={err?.field === 'username' ? err.message : undefined} htmlFor="nu-name">
          <input id="nu-name" className="input" value={username} onChange={(e) => setUsername(e.target.value.toLowerCase())} autoComplete="off" />
        </Field>
        <Field label={t('auth.password')} hint={t('validation.password_policy')} error={err?.field === 'password' ? err.message : undefined} htmlFor="nu-pw">
          <input id="nu-pw" className="input" type="password" value={password} onChange={(e) => setPassword(e.target.value)} autoComplete="new-password" />
        </Field>
        <Field label={t('users.role')} htmlFor="nu-role">
          <select id="nu-role" className="select" value={role} onChange={(e) => setRole(e.target.value)}>
            {roles.map((r) => <option key={r.name} value={r.name}>{t(`roles.${r.name.replace('-', '_')}`)}</option>)}
          </select>
        </Field>
        <Field label={t('account.language')} htmlFor="nu-lang">
          <select id="nu-lang" className="select" value={lang} onChange={(e) => setLang(e.target.value)}>
            <option value="de">Deutsch</option>
            <option value="en">English</option>
          </select>
        </Field>
      </div>
      {err && !err.field && <ErrorAlert error={err} />}
    </Dialog>
  )
}

function ResetPassword({ user, onClose }: { user: User; onClose: () => void }) {
  const { t } = useTranslation()
  const toast = useToast()
  const [pw, setPw] = useState('')
  const [err, setErr] = useState<ApiError>()
  const submit = async () => {
    try {
      await api.put(`/users/${user.id}/password`, { password: pw })
      toast('ok', t('users.password_reset_done', { name: user.username }))
      onClose()
    } catch (e) {
      setErr(e as ApiError)
    }
  }
  return (
    <Dialog title={t('users.reset_password_for', { name: user.username })} onClose={onClose} footer={<><button className="btn" onClick={onClose}>{t('common.cancel')}</button><button className="btn primary" disabled={pw.length < 12} onClick={submit}>{t('common.save')}</button></>}>
      <Field label={t('account.new_password')} hint={t('validation.password_policy')} htmlFor="rp-pw">
        <input id="rp-pw" className="input" type="password" value={pw} onChange={(e) => setPw(e.target.value)} autoComplete="new-password" />
      </Field>
      <div className="faint">{t('users.reset_signs_out')}</div>
      <ErrorAlert error={err} />
    </Dialog>
  )
}
