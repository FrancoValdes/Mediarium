// Which quality profile a title with none of its own follows. Movies use the
// default profile; shows use the default for TV when one is set, and the same
// default as movies when it isn't.
export interface ProfileDefaults {
  defaultId: number
  defaultTvId?: number
}

export type TitleKind = 'movie' | 'series' | 'tv'

export function defaultProfileFor(kind: TitleKind, d: ProfileDefaults): number {
  if (kind === 'movie') return d.defaultId
  return d.defaultTvId || d.defaultId
}
