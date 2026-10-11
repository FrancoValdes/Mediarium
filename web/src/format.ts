import { locale, plural, t } from './i18n.ts'

// "just now", "5 min ago", "3 hours ago", "2 days ago", or the date for anything older.
export function timeAgo(iso: string | undefined): string {
  if (!iso) return ''
  const then = new Date(iso).getTime()
  if (Number.isNaN(then)) return ''
  const sec = Math.max(0, Math.round((Date.now() - then) / 1000))
  if (sec < 45) return t('just now')
  if (sec < 3600) return plural(Math.round(sec / 60), '{n} min ago', '{n} min ago')
  if (sec < 86400) return plural(Math.round(sec / 3600), '{n} hour ago', '{n} hours ago')
  if (sec < 7 * 86400) return plural(Math.round(sec / 86400), '{n} day ago', '{n} days ago')
  return new Date(then).toLocaleDateString(locale())
}

export function formatBytes(bytes: number): string {
  if (!bytes) return '—'
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  let i = 0
  let n = bytes
  while (n >= 1024 && i < units.length - 1) {
    n /= 1024
    i++
  }
  return `${n.toFixed(1)} ${units[i]}`
}

// A stored quality for display. A file whose name doesn't say is "Not stated"
// rather than an alarming "Unknown".
export function qualityText(q: string | undefined | null): string {
  if (!q) return '—'
  return q === 'Unknown' ? t('Not stated') : q
}
