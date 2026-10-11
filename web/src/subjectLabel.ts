import { t } from './i18n.ts'

// Open Library files some subjects as "series:Harry_Potter" or "place:London".
// Shown as words: "Series: Harry Potter", "Place: London".
export function subjectLabel(s: string): string {
  const m = /^(series|place|person|time|people):(.+)$/i.exec(s)
  const text = (m ? m[2] : s).replace(/_/g, ' ').trim()
  if (!m) return text
  const kind = { series: t('Series'), place: t('Place'), person: t('Person'), people: t('Person'), time: t('Time') }[m[1].toLowerCase()] ?? m[1]
  return `${kind}: ${text}`
}
