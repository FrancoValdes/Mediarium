import { t } from './i18n.ts'

// What a rescan of a title's files found, in a sentence.
export function rescanMessage(r: { found: number; renamed: number; missing: number; skipped: number }): string {
  if (r.skipped > 0 && r.found + r.renamed + r.missing === 0) {
    return t('The library folder looks empty or not available, so nothing was changed. Is the drive plugged in?')
  }
  const parts = [t('{n} fine', { n: r.found })]
  if (r.renamed > 0) parts.push(t('{n} found under a new name', { n: r.renamed }))
  if (r.missing > 0) parts.push(t('{n} gone, wanted again', { n: r.missing }))
  if (r.skipped > 0) parts.push(t('{n} left alone because the library folder looks unavailable', { n: r.skipped }))
  return t('Checked the files: {what}.', { what: parts.join(', ') })
}
