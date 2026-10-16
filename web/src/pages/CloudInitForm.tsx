import { useTranslation } from 'react-i18next'
import type { TFunction } from 'i18next'
import type { CloudInit } from '../api/types'
import { Field } from '../components/ui'

const userRe = /^[a-z_][a-z0-9_-]{0,31}$/
const keyRe = /^(ssh-(rsa|ed25519|dss)|ecdsa-sha2-nistp(256|384|521)|sk-(ssh-ed25519|ecdsa-sha2-nistp256)@openssh\.com) [A-Za-z0-9+/=]+( .{0,200})?$/
const cidr4 = /^(\d{1,3}\.){3}\d{1,3}\/\d{1,2}$/
const ip4 = /^(\d{1,3}\.){3}\d{1,3}$/
const pkgRe = /^[a-z0-9][a-z0-9.+-]{0,63}$/

export interface CloudInitDraft {
  user: string
  password: string
  keys: string
  hostname: string
  ipMode: 'dhcp' | 'static'
  ipv4: string
  gateway4: string
  ipv6: string
  dns: string
  packages: string
  guestAgent: boolean
  keepPassword: boolean
}

export function draftFrom(ci: CloudInit | undefined, hostname: string): CloudInitDraft {
  return {
    user: ci?.user ?? 'admin',
    password: '',
    keys: (ci?.ssh_keys ?? []).join('\n'),
    hostname: ci?.hostname ?? hostname,
    ipMode: ci?.ipv4 ? 'static' : 'dhcp',
    ipv4: ci?.ipv4 ?? '',
    gateway4: ci?.gateway4 ?? '',
    ipv6: ci?.ipv6 ?? '',
    dns: (ci?.dns ?? []).join(', '),
    packages: (ci?.packages ?? []).join(' '),
    guestAgent: ci?.guest_agent ?? true,
    keepPassword: ci?.password_hash === 'set',
  }
}

export function toCloudInit(d: CloudInitDraft): CloudInit {
  const list = (s: string, sep: RegExp) => s.split(sep).map((x) => x.trim()).filter(Boolean)
  const ci: CloudInit = {
    user: d.user.trim(),
    ssh_keys: list(d.keys, /\n/),
    hostname: d.hostname.trim() || undefined,
    ipv4: d.ipMode === 'static' ? d.ipv4.trim() : undefined,
    gateway4: d.ipMode === 'static' ? d.gateway4.trim() || undefined : undefined,
    ipv6: d.ipv6.trim() || undefined,
    dns: list(d.dns, /[,\s]+/),
    packages: list(d.packages, /[,\s]+/),
    guest_agent: d.guestAgent,
  }
  if (d.password) ci.password = d.password
  else if (d.keepPassword) ci.password_hash = 'set'
  return ci
}

export function cloudInitErrors(d: CloudInitDraft, t: TFunction): Record<string, string> {
  const e: Record<string, string> = {}
  if (!userRe.test(d.user.trim())) e.user = t('validation.ci_user')
  if (d.password && d.password.length < 8) e.password = t('validation.min_chars', { n: 8 })
  const keys = d.keys.split('\n').map((x) => x.trim()).filter(Boolean)
  if (keys.some((k) => !keyRe.test(k))) e.keys = t('validation.ssh_key')
  if (!d.password && !d.keepPassword && keys.length === 0) e.keys = t('validation.ci_login')
  if (d.ipMode === 'static') {
    if (!cidr4.test(d.ipv4.trim())) e.ipv4 = t('validation.cidr')
    if (d.gateway4.trim() && !ip4.test(d.gateway4.trim())) e.gateway4 = t('validation.ip')
  }
  if (d.packages.split(/[,\s]+/).filter(Boolean).some((p) => !pkgRe.test(p))) e.packages = t('validation.packages')
  return e
}

export function CloudInitForm({ d, set, errors, idPrefix = 'ci' }: { d: CloudInitDraft; set: (d: CloudInitDraft) => void; errors: Record<string, string>; idPrefix?: string }) {
  const { t } = useTranslation()
  const up = <K extends keyof CloudInitDraft>(k: K, v: CloudInitDraft[K]) => set({ ...d, [k]: v })
  const id = (s: string) => `${idPrefix}-${s}`
  return (
    <div className="form-grid">
      <Field label={t('ci.user')} error={errors.user} htmlFor={id('user')}>
        <input id={id('user')} className="input" value={d.user} onChange={(e) => up('user', e.target.value)} autoComplete="off" />
      </Field>
      <Field label={t('ci.password')} error={errors.password} hint={d.keepPassword ? t('ci.password_keep') : t('ci.password_hint')} htmlFor={id('pw')}>
        <input id={id('pw')} className="input" type="password" value={d.password} onChange={(e) => up('password', e.target.value)} autoComplete="new-password" />
      </Field>
      <Field label={t('ci.ssh_keys')} className="full" error={errors.keys} hint={t('ci.ssh_keys_hint')} htmlFor={id('keys')}>
        <textarea id={id('keys')} className="textarea" rows={3} value={d.keys} onChange={(e) => up('keys', e.target.value)} placeholder="ssh-ed25519 AAAA… user@host" />
      </Field>
      <Field label={t('ci.hostname')} htmlFor={id('host')}>
        <input id={id('host')} className="input" value={d.hostname} onChange={(e) => up('hostname', e.target.value)} />
      </Field>
      <Field label={t('ci.ipv4_mode')} htmlFor={id('mode')}>
        <select id={id('mode')} className="select" value={d.ipMode} onChange={(e) => up('ipMode', e.target.value as 'dhcp' | 'static')}>
          <option value="dhcp">DHCP</option>
          <option value="static">{t('ci.static')}</option>
        </select>
      </Field>
      {d.ipMode === 'static' && (
        <>
          <Field label={t('ct.ipv4')} error={errors.ipv4} htmlFor={id('ip')}>
            <input id={id('ip')} className="input mono" value={d.ipv4} onChange={(e) => up('ipv4', e.target.value)} placeholder="203.0.113.10/24" />
          </Field>
          <Field label={t('ct.gateway')} error={errors.gateway4} htmlFor={id('gw')}>
            <input id={id('gw')} className="input mono" value={d.gateway4} onChange={(e) => up('gateway4', e.target.value)} placeholder="203.0.113.1" />
          </Field>
        </>
      )}
      <Field label="IPv6" hint={t('ci.ipv6_hint')} htmlFor={id('ip6')}>
        <input id={id('ip6')} className="input mono" value={d.ipv6} onChange={(e) => up('ipv6', e.target.value)} placeholder="auto" />
      </Field>
      <Field label={t('ci.dns')} hint={t('ci.dns_hint')} htmlFor={id('dns')}>
        <input id={id('dns')} className="input mono" value={d.dns} onChange={(e) => up('dns', e.target.value)} placeholder="1.1.1.1, 9.9.9.9" />
      </Field>
      <Field label={t('ci.packages')} error={errors.packages} className="full" htmlFor={id('pkg')}>
        <input id={id('pkg')} className="input mono" value={d.packages} onChange={(e) => up('packages', e.target.value)} placeholder="htop curl" />
      </Field>
      <label className="check full">
        <input type="checkbox" checked={d.guestAgent} onChange={(e) => up('guestAgent', e.target.checked)} />
        {t('ci.guest_agent')}
      </label>
    </div>
  )
}

// Official cloud images offered as quick picks in the download dialog. They
// track the distributions' "latest"/"current" builds; checksums are published
// next to them (SHA512SUMS / SHA256SUMS).
export const cloudImages = [
  { label: 'Debian 13 (trixie) genericcloud', url: 'https://cloud.debian.org/images/cloud/trixie/latest/debian-13-genericcloud-amd64.qcow2', sums: 'https://cloud.debian.org/images/cloud/trixie/latest/SHA512SUMS' },
  { label: 'Debian 12 (bookworm) genericcloud', url: 'https://cloud.debian.org/images/cloud/bookworm/latest/debian-12-genericcloud-amd64.qcow2', sums: 'https://cloud.debian.org/images/cloud/bookworm/latest/SHA512SUMS' },
  { label: 'Ubuntu 24.04 LTS (noble)', url: 'https://cloud-images.ubuntu.com/noble/current/noble-server-cloudimg-amd64.img', sums: 'https://cloud-images.ubuntu.com/noble/current/SHA256SUMS' },
  { label: 'Ubuntu 22.04 LTS (jammy)', url: 'https://cloud-images.ubuntu.com/jammy/current/jammy-server-cloudimg-amd64.img', sums: 'https://cloud-images.ubuntu.com/jammy/current/SHA256SUMS' },
]
