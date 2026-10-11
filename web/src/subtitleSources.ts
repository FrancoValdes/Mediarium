import type { Settings, SubtitleSourceId, SubtitleSourceStatus } from './api'

// Where subtitles can be searched: OpenSubtitles, SubDL and the add-ons. Each
// has a switch, and a source with no key (or no add-on yet) can't be on.

// The settings field that holds a source's switch.
export const SOURCE_FIELD = {
  opensubtitles: 'openSubtitlesEnabled',
  subdl: 'subdlEnabled',
  addons: 'subtitleAddonsEnabled',
} as const satisfies Record<SubtitleSourceId, keyof Settings>

// A source is searched while it is on, or on but out of downloads for today.
export function isSearched(s: SubtitleSourceStatus): boolean {
  return s.state === 'on' || s.state === 'limit'
}

// At least one source is switched on and set up.
export function anySourceOn(sources: SubtitleSourceStatus[] | undefined): boolean {
  return (sources ?? []).some(isSearched)
}

// At least one source is set up (has a key or an add-on), switched on or not.
export function anySourceSetUp(sources: SubtitleSourceStatus[] | undefined): boolean {
  return (sources ?? []).some((s) => s.ready)
}

// Whether one particular source is searched (false before the list has loaded).
export function isSearchedSource(sources: SubtitleSourceStatus[] | undefined, id: SubtitleSourceId): boolean {
  const s = sourceById(sources, id)
  return !!s && isSearched(s)
}

export function sourceById(sources: SubtitleSourceStatus[] | undefined, id: SubtitleSourceId): SubtitleSourceStatus | undefined {
  return (sources ?? []).find((s) => s.id === id)
}

// The server says (in the quota reply) that no source is switched on. Unknown,
// or an older server that does not say, counts as fine.
export function noSourceInQuota(quota: { hasSource?: boolean } | null): boolean {
  return !!quota && quota.hasSource === false
}

// What to tell someone when nothing can be searched: nothing is set up yet, or
// everything that is has been switched off. Null while at least one is on, or
// before the list has loaded.
export type NoSourceReason = 'none_set_up' | 'all_off'
export function noSourceReason(sources: SubtitleSourceStatus[] | undefined): NoSourceReason | null {
  if (!sources || sources.length === 0 || anySourceOn(sources)) return null
  return anySourceSetUp(sources) ? 'all_off' : 'none_set_up'
}
