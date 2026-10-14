import { t } from '../i18n'

// A small "Movie" or "TV" tag, for lists where the two kinds can be told
// apart only by reading the row.
export default function TypeTag({ kind }: { kind: 'movie' | 'tv' | 'series' }) {
  const movie = kind === 'movie'
  return <span className={`type-tag ${movie ? 'type-movie' : 'type-tv'}`}>{movie ? t('Movie') : t('TV')}</span>
}
