import { locale } from './i18n.ts'

// When a held release can be taken, for the Wanted page: "14:20" for later
// today, "10 Oct, 14:20" for another day. Times are the browser's.
export function untilText(iso: string, now: Date = new Date(), loc: string | undefined = locale()): string {
  const at = new Date(iso)
  if (Number.isNaN(at.getTime())) return ''
  const time = at.toLocaleTimeString(loc, { hour: '2-digit', minute: '2-digit' })
  if (at.toDateString() === now.toDateString()) return time
  return `${at.toLocaleDateString(loc, { day: 'numeric', month: 'short' })}, ${time}`
}
