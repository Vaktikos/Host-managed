import i18n from '../i18n'

const locale = () => (i18n.language === 'de' ? 'de-DE' : 'en-GB')

const units = ['B', 'KiB', 'MiB', 'GiB', 'TiB', 'PiB']

export function formatBytes(n: number | null | undefined, digits = 1): string {
  if (n === null || n === undefined || !isFinite(n)) return '–'
  let i = 0
  let v = n
  while (Math.abs(v) >= 1024 && i < units.length - 1) {
    v /= 1024
    i++
  }
  const nf = new Intl.NumberFormat(locale(), { maximumFractionDigits: i === 0 ? 0 : digits, minimumFractionDigits: 0 })
  return `${nf.format(v)} ${units[i]}`
}

export function formatRate(bps: number | null | undefined): string {
  if (bps === null || bps === undefined) return '–'
  return formatBytes(bps) + '/s'
}

export function formatPercent(v: number | null | undefined, digits = 1): string {
  if (v === null || v === undefined || !isFinite(v)) return '–'
  return new Intl.NumberFormat(locale(), { maximumFractionDigits: digits, minimumFractionDigits: digits }).format(v) + ' %'
}

export function formatNumber(v: number, digits = 2): string {
  return new Intl.NumberFormat(locale(), { maximumFractionDigits: digits }).format(v)
}

export function formatDate(s: string | null | undefined): string {
  if (!s) return '–'
  const d = new Date(s)
  if (isNaN(d.getTime())) return '–'
  return new Intl.DateTimeFormat(locale(), { dateStyle: 'short', timeStyle: 'medium' }).format(d)
}

export function formatTime(s: string | null | undefined): string {
  if (!s) return '–'
  const d = new Date(s)
  return new Intl.DateTimeFormat(locale(), { timeStyle: 'medium' }).format(d)
}

export function formatDuration(seconds: number): string {
  const t = i18n.t.bind(i18n)
  const d = Math.floor(seconds / 86400)
  const h = Math.floor((seconds % 86400) / 3600)
  const m = Math.floor((seconds % 3600) / 60)
  if (d > 0) return t('time.days_hours', { d, h })
  if (h > 0) return t('time.hours_minutes', { h, m })
  return t('time.minutes', { m })
}

export function durationBetween(a: string | null, b: string | null): string {
  if (!a) return '–'
  const end = b ? new Date(b).getTime() : Date.now()
  const s = Math.max(0, Math.round((end - new Date(a).getTime()) / 1000))
  if (s < 60) return i18n.t('time.seconds', { s })
  return formatDuration(s)
}

export function formatMiB(mib: number): string {
  return formatBytes(mib * 1024 * 1024, 2)
}
