// Sorting and filtering for the lists of releases and subtitles: pure
// functions, so the table components only draw what they return.

export type ReleaseSortKey = 'release' | 'quality' | 'language' | 'score' | 'indexer' | 'size' | 'seeders'
export type SortDir = 1 | -1

// The direction a column starts in when first clicked: biggest, best or
// highest first for numbers, A to Z for names.
export const FIRST_DIRECTION: Record<ReleaseSortKey, SortDir> = {
  release: 1,
  quality: -1,
  language: 1,
  score: -1,
  indexer: 1,
  size: -1,
  seeders: -1,
}

export interface SortableRelease {
  title: string
  indexerName: string
  protocol: string
  sizeBytes: number
  resolution?: string
  source?: string
  seeders?: number
  formatScore?: number
  language?: string
}

// resolutionOf is the number in "1080p" (2160 for 4K), 0 when there is none.
export function resolutionOf(r: { resolution?: string }): number {
  const res = (r.resolution ?? '').toLowerCase()
  if (res === '4k') return 2160
  const n = parseInt(res, 10)
  return Number.isFinite(n) ? n : 0
}

const text = (a: string, b: string) => a.localeCompare(b, undefined, { sensitivity: 'base', numeric: true })

function compare(a: SortableRelease, b: SortableRelease, key: ReleaseSortKey): number {
  switch (key) {
    case 'release':
      return text(a.title, b.title)
    case 'quality':
      return resolutionOf(a) - resolutionOf(b)
    case 'language':
      return text(a.language ?? '', b.language ?? '')
    case 'score':
      return (a.formatScore ?? 0) - (b.formatScore ?? 0)
    case 'indexer':
      return text(a.indexerName, b.indexerName)
    case 'size':
      return a.sizeBytes - b.sizeBytes
    case 'seeders':
      // Usenet has no seeders: those sort as none at all.
      return (a.protocol === 'torrent' ? (a.seeders ?? 0) : -1) - (b.protocol === 'torrent' ? (b.seeders ?? 0) : -1)
  }
}

// sortReleases returns a new list ordered by key. Equal rows keep the order
// they came in (the best match first, as the server sent them).
export function sortReleases<T extends SortableRelease>(list: T[], key: ReleaseSortKey | null, dir: SortDir): T[] {
  if (!key) return list
  return list
    .map((item, i) => ({ item, i }))
    .sort((x, y) => compare(x.item, y.item, key) * dir || x.i - y.i)
    .map((x) => x.item)
}

export interface ReleaseFilter {
  text: string
  indexer: string // '' for any
  quality: string // '' for any, 'other' for no resolution, else '1080p' and so on
}

export const qualityOf = (r: { resolution?: string }): string => (resolutionOf(r) > 0 ? `${resolutionOf(r)}p` : 'other')

export function filterReleases<T extends SortableRelease>(list: T[], f: ReleaseFilter): T[] {
  const words = f.text.toLowerCase().split(/\s+/).filter(Boolean)
  return list.filter((r) => {
    if (f.indexer && r.indexerName !== f.indexer) return false
    if (f.quality && qualityOf(r) !== f.quality) return false
    const title = r.title.toLowerCase()
    return words.every((w) => title.includes(w))
  })
}

// The distinct values to offer in a filter, in a sensible order.
export function indexersIn<T extends SortableRelease>(list: T[]): string[] {
  return [...new Set(list.map((r) => r.indexerName))].sort(text)
}

export function qualitiesIn<T extends SortableRelease>(list: T[]): string[] {
  const found = new Set(list.map(qualityOf))
  const ordered = [...found].filter((q) => q !== 'other').sort((a, b) => parseInt(b, 10) - parseInt(a, 10))
  return found.has('other') ? [...ordered, 'other'] : ordered
}

// Subtitles: rating and fit are numbers, the rest are names.
export type SubtitleSortKey = 'release' | 'source' | 'rating' | 'fit'

export interface SortableSubtitle {
  release: string
  source?: string
  rating: number
  score: number
}

export const SUBTITLE_FIRST_DIRECTION: Record<SubtitleSortKey, SortDir> = { release: 1, source: 1, rating: -1, fit: -1 }

export function sortSubtitles<T extends SortableSubtitle>(list: T[], key: SubtitleSortKey | null, dir: SortDir): T[] {
  if (!key) return list
  const cmp = (a: T, b: T) => {
    switch (key) {
      case 'release':
        return text(a.release, b.release)
      case 'source':
        return text(a.source ?? '', b.source ?? '')
      case 'rating':
        return (a.source ? 0 : a.rating) - (b.source ? 0 : b.rating)
      case 'fit':
        return a.score - b.score
    }
  }
  return list
    .map((item, i) => ({ item, i }))
    .sort((x, y) => cmp(x.item, y.item) * dir || x.i - y.i)
    .map((x) => x.item)
}
