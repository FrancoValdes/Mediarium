// What to say above or instead of a list of releases, from what the
// indexers answered. The old hint always blamed a missing indexer, even
// when one was set up and simply did not answer.
import { plural, t } from './i18n.ts'

export interface Unanswered {
  name: string
  message: string
}

export interface ReleaseHint {
  text: string
  // Whether the text ends with a link to Settings > Indexers & Search.
  link: boolean
  // True when there is nothing to list, so the hint stands in for the table.
  empty: boolean
}

export function releaseHint(sources: number, unanswered: Unanswered[], results: number): ReleaseHint | null {
  const names = unanswered.map((u) => u.name).join(', ')
  const n = unanswered.length
  if (results === 0) {
    if (sources === 0) {
      return { text: t('No indexer is set up yet. Add one under'), link: true, empty: true }
    }
    if (n >= sources) {
      const text = n === 1 ? t('The indexer did not answer: {names}. Check it under', { names }) : t('The indexers did not answer: {names}. Check them under', { names })
      return { text, link: true, empty: true }
    }
    if (n > 0) {
      return { text: plural(n, 'No matching releases. {n} indexer did not answer: {names}. Check it under', 'No matching releases. {n} indexers did not answer: {names}. Check them under', { names }), link: true, empty: true }
    }
    return { text: t('No matching releases right now. Your indexers answered, but none of them has this yet.'), link: false, empty: true }
  }
  if (n > 0) {
    return { text: plural(n, '{n} indexer did not answer: {names}. The list may be missing some releases.', '{n} indexers did not answer: {names}. The list may be missing some releases.', { names }), link: false, empty: false }
  }
  return null
}
