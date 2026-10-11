import { useEffect, useMemo, useState } from 'react'
import { Link } from 'react-router-dom'
import { api, type ManualFile, type Movie, type Series } from '../api'
import { formatBytes } from '../format'
import Icon from '../components/Icon'
import KindGlyph from '../components/KindGlyph'
import Loading from '../components/Loading'
import { useToast } from '../components/Toast'
import { t, tc } from '../i18n'
import '../import.css'

const norm = (s: string) => s.toLowerCase().replace(/[^a-z0-9]+/g, ' ').trim()

// Import a file by hand: video files in the downloads folder that were not
// matched, each with a guess of what it is. Pick the movie or the episode
// and Mediarium names it and moves it into the library.
export default function ManualImport() {
  const [files, setFiles] = useState<ManualFile[] | null>(null)
  const [folder, setFolder] = useState('')
  const [movies, setMovies] = useState<Movie[]>([])
  const [series, setSeries] = useState<Series[]>([])
  const [error, setError] = useState('')
  const [done, setDone] = useState<Set<string>>(new Set())

  useEffect(() => {
    api
      .manualImportList()
      .then((r) => {
        setFiles(r.files)
        setFolder(r.folder)
      })
      .catch((e) => setError(e instanceof Error ? e.message : String(e)))
    api.listMovies().then(setMovies).catch(() => undefined)
    api.listSeries().then(setSeries).catch(() => undefined)
  }, [])

  const left = (files ?? []).filter((f) => !done.has(f.path))
  return (
    <div className="import-page">
      <div className="page-header">
        <h1>{t('Import a file by hand')}</h1>
        <Link className="btn-link" to="/queue">
          <Icon name="activity" size={15} /> {t('Back to Activity')}
        </Link>
      </div>
      <p className="imp-lede">
        {folder
          ? t("Video files in the downloads folder ({folder}), biggest first. Choose what each one is, and Mediarium names it and puts it in your library. The title must be in your library already; add it first if it isn't.", { folder })
          : t("Video files in the downloads folder, biggest first. Choose what each one is, and Mediarium names it and puts it in your library. The title must be in your library already; add it first if it isn't.")}
      </p>
      {error && <p className="error-text">{error}</p>}
      {files === null && !error && <Loading height={140} />}
      {files !== null && left.length === 0 && (
        <div className="empty-state">
          <Icon name="check" size={44} />
          <p>{t('There are no video files waiting in the downloads folder.')}</p>
        </div>
      )}
      <div className="manual-list">
        {left.map((f) => (
          <ManualRow key={f.path} file={f} movies={movies} series={series} onDone={() => setDone((d) => new Set(d).add(f.path))} />
        ))}
      </div>
    </div>
  )
}

function ManualRow({ file: f, movies, series, onDone }: { file: ManualFile; movies: Movie[]; series: Series[]; onDone: () => void }) {
  const toast = useToast()
  const guessMovie = useMemo(() => movies.find((m) => norm(m.title) === norm(f.title ?? '') && (!f.year || m.year === f.year)), [movies, f])
  const guessShow = useMemo(() => series.find((s) => norm(s.title) === norm(f.title ?? '')), [series, f])
  const [kind, setKind] = useState<'movie' | 'episode'>(f.season || f.episode ? 'episode' : 'movie')
  const [movieId, setMovieId] = useState(0)
  const [seriesId, setSeriesId] = useState(0)
  const [season, setSeason] = useState(String(f.season ?? 1))
  const [episode, setEpisode] = useState(f.episode ? String(f.episode) : '')
  const [busy, setBusy] = useState(false)
  const chosenMovie = movieId || guessMovie?.id || 0
  const chosenShow = seriesId || guessShow?.id || 0

  async function run() {
    setBusy(true)
    try {
      const r =
        kind === 'movie'
          ? await api.manualImport({ path: f.path, movieId: chosenMovie })
          : await api.manualImport({ path: f.path, seriesId: chosenShow, season: Number(season), episode: Number(episode) })
      toast.success(t('Imported to {path}', { path: r.path }))
      onDone()
    } catch (e) {
      toast.error(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(false)
    }
  }

  const ready = kind === 'movie' ? chosenMovie > 0 : chosenShow > 0 && Number(episode) > 0 && Number(season) >= 0
  const guess = f.title
    ? ` · ${t('looks like {what}', { what: `${f.title}${f.year ? ` (${f.year})` : ''}${f.season ? ` S${String(f.season).padStart(2, '0')}E${String(f.episode ?? 0).padStart(2, '0')}` : ''}` })}`
    : ''
  const slash = f.path.lastIndexOf('/')
  return (
    <div className={`card manual-row k-${kind === 'movie' ? 'movie' : 'tv'}`}>
      <span className="manual-shape">
        <KindGlyph kind={kind === 'movie' ? 'movie' : 'tv'} size={30} variant="solid" />
      </span>
      <div className="manual-file">
        <strong>{f.path.slice(slash + 1)}</strong>
        {slash > 0 && <code>{f.path.slice(0, slash + 1)}</code>}
        <small>
          {formatBytes(f.size)}
          {f.quality ? ` · ${f.quality}` : ''}
          {guess}
        </small>
      </div>
      <div className="manual-pick">
        <div className="seg" role="radiogroup" aria-label={t('What it is')}>
          <button role="radio" aria-checked={kind === 'movie'} className={kind === 'movie' ? 'active' : ''} onClick={() => setKind('movie')}>
            {t('Movie')}
          </button>
          <button role="radio" aria-checked={kind === 'episode'} className={kind === 'episode' ? 'active' : ''} onClick={() => setKind('episode')}>
            {t('Episode')}
          </button>
        </div>
        {kind === 'movie' ? (
          <select aria-label={t('Movie')} value={chosenMovie} onChange={(e) => setMovieId(Number(e.target.value))}>
            <option value={0}>{t('Choose a movie…')}</option>
            {movies.map((m) => (
              <option key={m.id} value={m.id}>
                {m.title} ({m.year})
              </option>
            ))}
          </select>
        ) : (
          <>
            <select aria-label={tc('tv', 'Show')} value={chosenShow} onChange={(e) => setSeriesId(Number(e.target.value))}>
              <option value={0}>{t('Choose a show…')}</option>
              {series.map((s) => (
                <option key={s.id} value={s.id}>
                  {s.title}
                </option>
              ))}
            </select>
            <label className="inline-field">
              {t('Season')} <input inputMode="numeric" value={season} onChange={(e) => setSeason(e.target.value)} style={{ width: 60 }} />
            </label>
            <label className="inline-field">
              {t('Episode')} <input inputMode="numeric" value={episode} onChange={(e) => setEpisode(e.target.value)} style={{ width: 60 }} />
            </label>
          </>
        )}
        <button className="primary" disabled={!ready || busy} onClick={() => void run()}>
          {busy ? t('Importing…') : t('Import')}
        </button>
      </div>
    </div>
  )
}
