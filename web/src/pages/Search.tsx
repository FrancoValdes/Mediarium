import { useEffect, useRef, useState } from 'react'
import { Link, useNavigate, useSearchParams } from 'react-router-dom'
import { api, type TitleResult } from '../api'
import AddDialog, { type AddTarget } from '../components/AddDialog'
import Icon from '../components/Icon'
import PosterCard from '../components/PosterCard'
import { capitalizeFirst } from '../components/SearchBox'
import { resultState } from '../components/state'
import { t, tc } from '../i18n'
import KindGlyph from '../components/KindGlyph'
import '../search.css'

// Search the movie and TV databases as you type, like Radarr's and Sonarr's
// "Add New": pick a title, choose how to add it, and Mediarium then searches
// the indexers for releases of that title. Titles you already own are listed
// first and say where they stand.
export default function Search() {
  const [params, setParams] = useSearchParams()
  const navigate = useNavigate()
  const urlQ = params.get('q') ?? ''
  const [q, setQ] = useState(urlQ)
  const [results, setResults] = useState<TitleResult[] | null>(null)
  const [error, setError] = useState('')
  const [adding, setAdding] = useState<AddTarget | null>(null)
  const inputRef = useRef<HTMLInputElement>(null)

  useEffect(() => inputRef.current?.focus(), [])
  // A new query from the top bar replaces what is typed here. The address is
  // updated with the trimmed text after each pause, so leave what is typed
  // alone when it only differs by spaces (or the word being typed loses its space).
  useEffect(() => setQ((cur) => (cur.trim() === urlQ ? cur : urlQ)), [urlQ])

  // Live: search shortly after the last keystroke, dropping stale answers.
  useEffect(() => {
    const term = q.trim()
    if (term.length < 2) {
      setResults(null)
      setError('')
      // Deleting down to one letter (or nothing) must not leave the old search in the address.
      if (term !== urlQ) setParams(term ? { q: term } : {}, { replace: true })
      return
    }
    const ctl = new AbortController()
    const timer = setTimeout(() => {
      setError('')
      api
        .discoverSearch(term, ctl.signal)
        .then(setResults)
        .catch((e) => {
          if (e instanceof DOMException && e.name === 'AbortError') return
          setError(e instanceof Error ? e.message : String(e))
        })
      setParams(term ? { q: term } : {}, { replace: true })
    }, 250)
    return () => {
      clearTimeout(timer)
      ctl.abort()
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [q])

  const open = (r: TitleResult) => navigate(r.kind === 'movie' ? `/title/${r.tmdbId}` : `/series/${r.libraryId}`)

  const grid = (list: TitleResult[]) => (
    <div className="poster-grid">
      {list.map((r) => (
        <PosterCard
          key={`${r.kind}-${r.tmdbId}`}
          to={r.inLibrary ? (r.kind === 'movie' ? `/title/${r.tmdbId}` : `/series/${r.libraryId}`) : r.kind === 'movie' ? `/title/${r.tmdbId}` : `/show/${r.tmdbId}`}
          poster={r.posterUrl}
          title={r.title}
          meta={[r.year || null, r.kind === 'tv' ? t('TV show') : t('Movie')].filter(Boolean).join(' · ')}
          state={resultState(r)}
          kind={r.kind}
          genres={r.genres}
          rating={r.rating}
          footer={
            <div className="pcard-footer">
              {r.inLibrary ? (
                <button className="btn-sm btn-with-icon" onClick={() => open(r)}>
                  <Icon name="open" size={15} /> {t('Open')}
                </button>
              ) : (
                <button
                  className="primary btn-sm btn-with-icon"
                  onClick={() => setAdding({ kind: r.kind, tmdbId: r.tmdbId, title: r.title, year: r.year, posterUrl: r.posterUrl, overview: r.overview })}
                >
                  <Icon name="plus" size={15} /> {t('Add')}
                </button>
              )}
            </div>
          }
        />
      ))}
    </div>
  )

  const owned = results?.filter((r) => r.inLibrary) ?? []
  const fresh = results?.filter((r) => !r.inLibrary) ?? []

  return (
    <div className="search-page">
      <div className="page-header">
        <h1>{tc('noun', 'Search')}</h1>
      </div>
      <nav className="search-modes" aria-label={tc('noun', 'Search')}>
        <Link to="/search" className="active" aria-current="page">
          <Icon name="film" size={15} /> {t('Movies and shows')}
        </Link>
        <Link to="/search/releases">
          <Icon name="list" size={15} /> {t('Releases')}
        </Link>
      </nav>

      <div className="search-bar">
        <Icon name="search" size={22} />
        <input
          ref={inputRef}
          type="search"
          value={q}
          onChange={(e) => setQ(capitalizeFirst(e.target.value))}
          placeholder={t('Type a movie or show name…')}
          aria-label={t('Search')}
        />
      </div>

      {error && <p className="error-text">{error}</p>}
      {results === null && !error && q.trim().length < 2 && (
        <div className="search-hint">
          <span className="search-hint-shapes" aria-hidden="true">
            <KindGlyph kind="movie" size={30} variant="solid" />
            <KindGlyph kind="tv" size={30} variant="solid" />
          </span>
          <p>{t('Start typing to search movies and TV shows. Anything already in your library shows where it stands.')}</p>
        </div>
      )}
      {results !== null && results.length === 0 && <div className="search-hint">{t('Nothing found for “{q}”.', { q: q.trim() })}</div>}
      {owned.length > 0 && (
        <section style={{ marginBottom: 28 }}>
          <div className="rail-head">
            <h2>{t('In your library')}</h2>
            <span>{owned.length}</span>
          </div>
          {grid(owned)}
        </section>
      )}
      {fresh.length > 0 && (
        <section>
          <div className="rail-head">
            <h2>{t('Add new')}</h2>
            <span>{fresh.length}</span>
          </div>
          {grid(fresh)}
        </section>
      )}

      {adding && (
        <AddDialog
          target={adding}
          onClose={() => setAdding(null)}
          onAdded={(id) => {
            const added = adding
            setAdding(null)
            navigate(added.kind === 'movie' ? `/title/${added.tmdbId}` : `/series/${id}`)
          }}
        />
      )}
    </div>
  )
}
