// The words the import banner, the import page and its report use, kept apart
// from the screens so they can be tested. Everything here is plain text for a
// person to read.

import { plural, t } from './i18n.ts'

// Only the fields these helpers read.
export interface BatchCounts {
  kind: 'movie' | 'tv'
  elapsedMs: number // how long the import has run, by the server's clock
  total: number // titles being added
  done: number // of those, details finished
  added: number
  already: number
  problems: number
  monitor: boolean // the titles are watched for new episodes and better versions
  noUpgrade: boolean
  monitorMissing: boolean
}

export type ImportKind = 'movie' | 'tv'

// "movie", "movies", "show", "shows".
export function unitFor(kind: ImportKind, n: number): string {
  if (kind === 'tv') return n === 1 ? t('show') : t('shows')
  return n === 1 ? t('movie') : t('movies')
}

export function problemWord(n: number): string {
  return n === 1 ? t('problem') : t('problems')
}

// "25 shows added", "1 movie added".
function addedPart(kind: ImportKind, n: number): string {
  return kind === 'tv' ? plural(n, '{n} show added', '{n} shows added') : plural(n, '{n} movie added', '{n} movies added')
}

// "0 problems", "1 problem".
function problemsPart(n: number): string {
  return plural(n, '{n} problem', '{n} problems')
}

// "Importing your library: 4 of 25 shows."
export function progressText(b: BatchCounts): string {
  return b.kind === 'tv'
    ? plural(b.total, 'Importing your library: {done} of {n} show.', 'Importing your library: {done} of {n} shows.', { done: b.done })
    : plural(b.total, 'Importing your library: {done} of {n} movie.', 'Importing your library: {done} of {n} movies.', { done: b.done })
}

// "Import finished. 25 shows added, 0 problems."
export function finishedText(b: BatchCounts): string {
  return t('Import finished. {added}, {problems}.', { added: addedPart(b.kind, b.added), problems: problemsPart(b.problems) })
}

// "25 shows added, 0 already in library, 0 problems"
export function totalLine(b: BatchCounts): string {
  return t('{added}, {already}, {problems}', { added: addedPart(b.kind, b.added), already: t('{n} already in library', { n: b.already }), problems: problemsPart(b.problems) })
}

// A rough time left, worked out from how fast the titles so far went. It says
// nothing until there is enough to go on.
export function etaText(b: BatchCounts): string {
  const elapsed = b.elapsedMs
  if (b.done < 2 || elapsed < 3000 || b.done >= b.total) return ''
  const left = (elapsed / b.done) * (b.total - b.done)
  const seconds = left / 1000
  if (seconds < 10) return t('Almost done.')
  if (seconds < 60) return t('About {n} seconds left.', { n: Math.max(10, Math.round(seconds / 5) * 5) })
  const minutes = Math.ceil(seconds / 60)
  if (minutes < 90) return plural(minutes, 'About {n} minute left.', 'About {n} minutes left.')
  return t('More than an hour left.')
}

// What Mediarium goes on to do once the import is done, for the choices made.
export function afterText(kind: ImportKind, monitor: boolean, monitorMissing: boolean): string {
  if (kind === 'movie') {
    return monitor
      ? t("Mediarium may replace movies that are below your quality profile's target with a better version.")
      : t('Nothing will be downloaded. Your files stay where they are.')
  }
  if (monitor && monitorMissing) {
    return t("Episodes you are missing will be searched for and downloaded, including new ones as they come out. Mediarium may also replace episodes that are below your quality profile's target.")
  }
  if (monitorMissing) {
    return t('Episodes you are missing will be searched for and downloaded, including new ones as they come out. The episodes you have are left alone.')
  }
  if (monitor) {
    return t("New episodes will be downloaded as they come out, and Mediarium may replace episodes that are below your quality profile's target. Episodes you are missing now are left alone.")
  }
  return t('Nothing will be downloaded for these shows. Your files stay where they are.')
}

// The short version for the banner: what starts once the details are in
// (or, for a finished import, what Mediarium does from now on).
export function upNextText(b: BatchCounts, finished = false): string {
  const wanted: string[] = []
  if (b.kind === 'tv' && b.monitorMissing) wanted.push(t('the episodes you are missing'))
  else if (b.kind === 'tv' && b.monitor) wanted.push(t('new episodes'))
  if (b.monitor && !b.noUpgrade) wanted.push(t('better versions of what you already have'))
  if (wanted.length === 0) return finished ? t('They are not monitored, so nothing will be downloaded.') : t('Nothing will be downloaded.')
  const list = wanted.length === 2 ? t('{first} and {second}', { first: wanted[0], second: wanted[1] }) : wanted[0]
  return finished ? t('From now on, Mediarium looks for {list}.', { list }) : t("When it's done, Mediarium starts looking for {list}.", { list })
}

// What the report says was set on the titles of a finished import.
export function setText(b: BatchCounts): string {
  const tv = b.kind === 'tv'
  if (!b.monitor && !(tv && b.monitorMissing)) {
    return tv
      ? t('These shows were added without monitoring, and Mediarium leaves what you have alone. Nothing will be downloaded until you start monitoring them.')
      : t('These movies were added without monitoring, and Mediarium leaves what you have alone. Nothing will be downloaded until you start monitoring them.')
  }
  const after = afterText(b.kind, b.monitor, b.monitorMissing)
  return tv ? t('These shows are monitored. {after}', { after }) : t('These movies are monitored. {after}', { after })
}
