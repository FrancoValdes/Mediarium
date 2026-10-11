import { useEffect, useState } from 'react'
import { Link, useNavigate, useParams } from 'react-router-dom'
import { api, type MusicPreview } from '../api'
import AddMusicDialog from '../components/AddMusicDialog'
import Cover from '../components/Cover'
import Icon from '../components/Icon'
import PageLoading from '../components/PageLoading'
import { comingLabel } from '../discoverSources'
import { useDocumentTitle } from '../documentTitle'
import { t } from '../i18n'

const TYPE_WORD: Record<string, string> = { album: t('Album'), ep: t('EP'), single: t('Single') }

function length(ms?: number): string {
  if (!ms) return ''
  const s = Math.round(ms / 1000)
  return `${Math.floor(s / 60)}:${String(s % 60).padStart(2, '0')}`
}

// An album's page before it is in the library (the same idea as a movie's or
// a book's page from Discover): cover, artist, tracklist and Add. An album
// that is already in the library opens its own place on the artist page.
export default function MusicRelease() {
  const { mbid = '' } = useParams()
  const navigate = useNavigate()
  const [album, setAlbum] = useState<MusicPreview | null>(null)
  const [error, setError] = useState('')
  const [adding, setAdding] = useState(false)
  useDocumentTitle(album?.title)

  // The album shows as soon as the first answer is in; its tracks follow with the second.
  const [tracksState, setTracksState] = useState<'loading' | 'done'>('loading')
  useEffect(() => {
    setAlbum(null)
    setError('')
    setTracksState('loading')
    let live = true
    api
      .musicPreview(mbid, false)
      .then((a) => {
        if (!live) return
        if (a.inLibrary && a.artistId) {
          navigate(`/music/artist/${a.artistId}${a.albumId ? `#album-${a.albumId}` : ''}`, { replace: true })
          return
        }
        setAlbum(a)
        api
          .musicPreview(mbid)
          .then((full) => live && setAlbum(full))
          .catch(() => undefined)
          .finally(() => live && setTracksState('done'))
      })
      .catch((e) => live && setError(e instanceof Error ? e.message : String(e)))
    return () => {
      live = false
    }
  }, [mbid, navigate])

  if (error) return <p className="error-text">{error}</p>
  if (!album) return <PageLoading label={t('Looking up the album')} />

  const coming = comingLabel(album.releaseDate)
  const year = album.releaseDate ? album.releaseDate.slice(0, 4) : ''
  const discs = new Set(album.tracks.map((x) => x.disc)).size
  return (
    <div>
      <section className="title-hero kind-music">
        {album.coverUrl && (
          <div className="title-scene cover-scene" aria-hidden="true">
            <img src={album.coverUrl} alt="" />
          </div>
        )}
        <div className="release-sleeve">
          <span className="release-vinyl" aria-hidden="true" />
          <div className="title-poster release-cover">
            <Cover src={album.coverUrl} alt={album.title} />
          </div>
        </div>
        <div className="title-info">
          <div className="title-kicker">
            <span className="kind-tag kind-music">
              <Icon name="music" size={13} /> {TYPE_WORD[album.type] ?? t('Release')}
            </span>
          </div>
          <h1>{album.title}</h1>
          <p className="title-tagline">
            {album.artistName}
            {coming ? (
              <>
                {' · '}
                <span className="coming-soon">{coming}</span>
              </>
            ) : year ? (
              ` · ${year}`
            ) : (
              ''
            )}
          </p>
          {album.genres && album.genres.length > 0 && (
            <div className="title-chips">
              {album.genres.map((g) => (
                <span key={g} className="chip-genre">
                  {g}
                </span>
              ))}
            </div>
          )}
          <div className="title-actions">
            <button className="primary btn-with-icon big" onClick={() => setAdding(true)}>
              <Icon name="plus" size={18} /> {coming ? t('Add early') : t('Add to library')}
            </button>
            <a className="btn-with-icon big link-quiet" href={`https://www.youtube.com/results?search_query=${encodeURIComponent(`${album.artistName} ${album.title}`)}`} target="_blank" rel="noreferrer" title={t('Search for it on YouTube')}>
              <Icon name="play" size={16} /> {t('YouTube')}
            </a>
          </div>
          <p className="hint source-note">
            {coming ? t('Add it now and it downloads when it is released.') : t('Adding it looks for a copy on your indexers. The tracklist and cover come from the music database.')}
          </p>
        </div>
      </section>

      <section className="card track-preview">
        <h2>{t('Tracks')}</h2>
        {album.tracks.length === 0 && tracksState === 'loading' ? (
          <div className="skeleton" style={{ height: 120, borderRadius: 14 }} />
        ) : album.tracks.length === 0 ? (
          <p className="hint">{t('The tracklist is not available right now.')}</p>
        ) : (
          <ol className="track-list">
            {album.tracks.map((x, i) => (
              <li key={`${x.disc}-${x.position}-${i}`}>
                <span className="n">{discs > 1 ? `${x.disc}.${x.position}` : x.position}</span>
                <span className="tt">{x.title}</span>
                <a
                  className="tyt"
                  href={`https://www.youtube.com/results?search_query=${encodeURIComponent(`${album.artistName} ${x.title}`)}`}
                  target="_blank"
                  rel="noreferrer"
                  aria-label={t('Search for {title} on YouTube', { title: x.title })}
                  title={t('Search for it on YouTube')}
                >
                  <Icon name="play" size={12} />
                </a>
                <span className="len">{length(x.lengthMs)}</span>
              </li>
            ))}
          </ol>
        )}
        {album.artistMbid && (
          <p className="hint">
            <Link to="/discover?kind=music">{t('Back to Discover')}</Link>
          </p>
        )}
      </section>

      {adding && (
        <AddMusicDialog
          target={{ kind: 'album', item: album }}
          onClose={() => setAdding(false)}
          onAdded={(artistId, albumId) => {
            setAdding(false)
            navigate(`/music/artist/${artistId}${albumId ? `#album-${albumId}` : ''}`)
          }}
        />
      )}
    </div>
  )
}
