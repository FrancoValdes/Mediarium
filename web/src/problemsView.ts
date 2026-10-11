// The wording and the filters of Settings > System > Logs and errors, kept apart
// from the page so they can be tested. Nothing here talks to the server.
import { locale, plural, t } from './i18n.ts'

export interface ProblemRow {
  id: number
  level: 'error' | 'warning'
  area: string
  areaLabel: string
  code: string
  title: string
  message: string
  explain?: string
  try?: string
  linkLabel?: string
  linkPath?: string
  detail?: string
  forTitle?: string
  forLink?: string
  downloadId?: number
  count: number
  firstAt: string
  lastAt: string
  read: boolean
  known: boolean
}

export interface ProblemCounts {
  errorsToday: number
  warningsToday: number
  errorsWeek: number
  warningsWeek: number
  unread: number
  unreadErrors24h: number
}

export interface ProblemList {
  items: ProblemRow[]
  total: number // how many match the filters, in all (the server pages the list)
  limit?: number // the page size the server used
  offset?: number // where this page starts
  counts: ProblemCounts
  areas: { id: string; label: string }[]
  notifyOnProblems: boolean
  keepDays: number
}

export type RangePreset = 'any' | 'day' | 'week' | 'month' | 'custom'

export interface ProblemFilters {
  level: 'all' | 'error' | 'warning'
  area: string // '' for every area
  range: RangePreset
  from: string // yyyy-mm-dd, used when range is custom
  to: string
  text: string
  unreadOnly: boolean
}

export const NO_FILTERS: ProblemFilters = { level: 'all', area: '', range: 'any', from: '', to: '', text: '', unreadOnly: false }

export const RANGE_LABELS: Record<RangePreset, string> = {
  any: t('Any time'),
  day: t('Last 24 hours'),
  week: t('Last 7 days'),
  month: t('Last 30 days'),
  custom: t('Choose dates'),
}

// The from and to values sent to the server for a range. A preset counts back
// from now (an exact time); chosen dates are plain days.
export function rangeBounds(f: Pick<ProblemFilters, 'range' | 'from' | 'to'>, now: Date = new Date()): { from?: string; to?: string } {
  const back = (ms: number) => new Date(now.getTime() - ms).toISOString()
  switch (f.range) {
    case 'day':
      return { from: back(24 * 3600e3) }
    case 'week':
      return { from: back(7 * 24 * 3600e3) }
    case 'month':
      return { from: back(30 * 24 * 3600e3) }
    case 'custom':
      return { from: f.from || undefined, to: f.to || undefined }
    default:
      return {}
  }
}

// The query string for the list and the text file: only what is filled in.
export function problemQuery(f: ProblemFilters, extra: Record<string, string | number> = {}, now: Date = new Date()): string {
  const q = new URLSearchParams()
  if (f.level !== 'all') q.set('level', f.level)
  if (f.area) q.set('area', f.area)
  const { from, to } = rangeBounds(f, now)
  if (from) q.set('from', from)
  if (to) q.set('to', to)
  if (f.text.trim()) q.set('q', f.text.trim())
  if (f.unreadOnly) q.set('unread', '1')
  for (const [k, v] of Object.entries(extra)) q.set(k, String(v))
  const s = q.toString()
  return s ? `?${s}` : ''
}

export function filtersActive(f: ProblemFilters): boolean {
  return f.level !== 'all' || !!f.area || f.range !== 'any' || !!f.text.trim() || f.unreadOnly
}

// "once", "3 times".
export function timesText(n: number): string {
  return n <= 1 ? t('once') : plural(n, '{n} time', '{n} times')
}

// "just now", "2 minutes ago", "5 hours ago", "3 days ago".
export function agoText(iso: string, now: number = Date.now()): string {
  const then = new Date(iso).getTime()
  if (Number.isNaN(then)) return ''
  const sec = Math.max(0, Math.round((now - then) / 1000))
  if (sec < 60) return t('just now')
  if (sec < 3600) return plural(Math.round(sec / 60), '{n} minute ago', '{n} minutes ago')
  if (sec < 48 * 3600) return plural(Math.round(sec / 3600), '{n} hour ago', '{n} hours ago')
  return plural(Math.round(sec / 86400), '{n} day ago', '{n} days ago')
}

// "Happened 14 times, last 2 minutes ago". A problem that happened once says only when.
export function happenedText(row: Pick<ProblemRow, 'count' | 'lastAt'>, now: number = Date.now()): string {
  const ago = agoText(row.lastAt, now)
  return row.count > 1 ? t('Happened {times}, last {ago}', { times: timesText(row.count), ago }) : t('Happened {ago}', { ago })
}

// The exact time, for a tooltip.
export function exactTime(iso: string): string {
  const d = new Date(iso)
  return Number.isNaN(d.getTime()) ? '' : d.toLocaleString(locale())
}

// The heading counts: "3 errors", "1 warning", "No errors".
export function countText(n: number, word: 'error' | 'warning'): string {
  if (word === 'error') return n === 0 ? t('No errors') : plural(n, '{n} error', '{n} errors')
  return n === 0 ? t('No warnings') : plural(n, '{n} warning', '{n} warnings')
}

// The text copied from one problem: what happened, what to try and the detail.
export function problemText(row: ProblemRow, now: number = Date.now()): string {
  const head = { title: row.title, area: row.areaLabel, code: row.code }
  const lines = [row.level === 'error' ? t('Error: {title} ({area}, {code})', head) : t('Warning: {title} ({area}, {code})', head), happenedText(row, now)]
  if (row.message && row.message !== row.title) lines.push(row.message)
  if (row.forTitle) lines.push(t('For: {title}', { title: row.forTitle }))
  if (row.explain) lines.push('', t('What happened:'), row.explain)
  if (row.try) lines.push('', t('What to try:'), row.try)
  if (row.detail) lines.push('', t('Technical detail:'), row.detail)
  return lines.join('\n')
}

// The line under the cards: how many rows are showing.
export function showingText(shown: number, total: number): string {
  if (total === 0) return ''
  if (shown >= total) return plural(total, '{n} problem', '{n} problems')
  return t('Showing {shown} of {total}', { shown, total })
}

// The message shown when nothing matches. With filters on, it says so.
export function emptyText(filtered: boolean, keepDays: number): string {
  return filtered ? t('Nothing matches these filters.') : plural(keepDays, 'No problems in the last {n} day.', 'No problems in the last {n} days.')
}

// How many rows one request asks for. The server allows at most MAX_PAGE.
export const PAGE_SIZE = 30
export const MAX_PAGE = 100

// Adds the next page to the rows already shown. A row that is on screen already
// is kept as it is, so a problem that repeated meanwhile does not show twice.
export function appendPage(rows: ProblemRow[], page: ProblemRow[]): ProblemRow[] {
  const have = new Set(rows.map((r) => r.id))
  return rows.concat(page.filter((r) => !have.has(r.id)))
}

// The requests that fetch the first `count` rows again, each within the
// server's page limit: 250 rows are [0,100], [100,100], [200,50].
export function pageRequests(count: number, max: number = MAX_PAGE): { offset: number; limit: number }[] {
  const out: { offset: number; limit: number }[] = []
  for (let offset = 0; offset < count; offset += max) out.push({ offset, limit: Math.min(max, count - offset) })
  return out
}

// A short text that changes when a row on the page changes (repeated, read, new).
export function pageSignature(rows: ProblemRow[], total: number): string {
  return `${total}|` + rows.map((r) => `${r.id}.${r.count}.${r.lastAt}.${r.read ? 1 : 0}`).join(',')
}

// What the rows look like after some were marked as read here. With "only
// unread" on, they leave the list (and the total goes down by as many).
export function afterMarkedRead(
  rows: ProblemRow[],
  total: number,
  mark: { ids?: number[]; all?: boolean },
  unreadOnly: boolean,
): { rows: ProblemRow[]; total: number } {
  const hit = (r: ProblemRow) => !!mark.all || !!mark.ids?.includes(r.id)
  if (unreadOnly) {
    const kept = rows.filter((r) => r.read || !hit(r))
    // Everything that matched is read now, including rows not loaded yet.
    return { rows: kept, total: mark.all ? 0 : Math.max(0, total - (rows.length - kept.length)) }
  }
  return { rows: rows.map((r) => (hit(r) ? { ...r, read: true } : r)), total }
}

// The label of the button under the list: what the next click adds.
export function moreText(shown: number, total: number, page: number = PAGE_SIZE): string {
  const next = Math.max(0, Math.min(page, total - shown))
  return next > 0 ? t('Show {n} more', { n: next }) : ''
}
