import { useEffect, useRef, useState } from 'react'
import { useNavigate, useParams } from 'react-router-dom'
import AddDialog from '../components/AddDialog'
import Icon from '../components/Icon'
import InfoTip from '../components/InfoTip'
import PosterCard from '../components/PosterCard'
import ProfilePicker from '../components/ProfilePicker'
import ReleaseTable from '../components/ReleaseTable'
import TitleTags from '../components/Tags'
import WatchedLine from '../components/WatchedLine'
import CollectionCard from '../components/CollectionCard'
import CopiesCard from '../components/CopiesCard'
import TitleRequestActions from '../components/TitleRequestActions'
import FilesPanel from '../components/FilesPanel'
import SubtitlesOffNote from '../components/SubtitlesOffNote'
import SubtitlesPanel from '../components/SubtitlesPanel'
import Switch from '../components/Switch'
import TitleHero from '../components/TitleHero'
import { CastCard, titleLinks } from '../components/TitlePreview'
import WatchLinks from '../components/WatchLinks'
import TitleEvents from '../components/TitleEvents'
import SearchStatus from '../components/SearchStatus'
import { describeState } from '../components/state'
import { useToast } from '../components/Toast'
import { useAuth } from '../AuthContext'
import { useModules } from '../ModulesContext'
import { qualityText } from '../format'
import { rescanMessage } from '../rescanMessage'
import { api, can, isAdmin, type DiscoverMovie, type MovieDetail as MovieDetailData, type SearchResult } from '../api'
import { useLive } from '../useLive'
import { useConfirm } from '../components/ConfirmProvider'
import { removeOption } from '../diskUsage'
import ReleaseNotice from '../components/ReleaseNotice'
import type { Unanswered } from '../releaseHint'
import { useDocumentTitle } from '../documentTitle'
import { plural, t } from '../i18n'

// One page per movie. Not in your library: exactly one main action, "Add to
// library" (the dialog picks quality and where to download, and can start the
// search). In your library: "Find and download now" does the automatic search,
// and "Choose a release" is the manual version for when you want to pick.
export default function MovieDetail() {
  // Another movie (from "Similar movies") starts clean, so nothing of the last one stays on the page.
  return <MoviePage key={useParams<{ tmdbId: string }>().tmdbId} />
}

function MoviePage() {
  const confirm = useConfirm()
  const me = useAuth().user
  const admin = isAdmin(me)
  const { subtitlesOn } = useModules()
  const { tmdbId } = useParams<{ tmdbId: string }>()
  const navigate = useNavigate()
  const id = Number(tmdbId)
  const toast = useToast()

  const [movie, setMovie] = useState<MovieDetailData | null>(null)
  useDocumentTitle(movie?.title)
  const [similar, setSimilar] = useState<DiscoverMovie[]>([])
  const [error, setError] = useState('')
  const [adding, setAdding] = useState(false)
  const [monitored, setMonitored] = useState<boolean | null>(null)
  const [results, setResults] = useState<SearchResult[] | null>(null)
  // The releases list is drawn above the files and settings: bring it into view when a search answers.
  const releasesRef = useRef<HTMLElement>(null)
  const hasResults = results !== null
  useEffect(() => {
    if (hasResults) releasesRef.current?.scrollIntoView({ behavior: 'smooth', block: 'start' })
  }, [hasResults])
  const [answered, setAnswered] = useState<{ sources: number; unanswered: Unanswered[] }>({ sources: 0, unanswered: [] })
  const [searching, setSearching] = useState(false)
  const [searchingNow, setSearchingNow] = useState(false)
  const [grabbing, setGrabbing] = useState<string | null>(null)

  const [rescanning, setRescanning] = useState(false)
  async function rescan() {
    if (!movie?.libraryId) return
    setRescanning(true)
    try {
      toast.success(rescanMessage(await api.rescanMovie(movie.libraryId)))
      reload()
    } catch (e) {
      toast.error(e instanceof Error ? e.message : String(e))
    } finally {
      setRescanning(false)
    }
  }

  async function deleteFile() {
    if (!movie?.libraryId) return
    const answer = await confirm({
      title: t('Delete the file of {title}?', { title: movie.title }),
      body: <p>{t('The file goes to the recycle bin (or is deleted, if it is switched off). The movie stays in your library and is wanted again, so Mediarium can look for a new release.')}</p>,
      confirmLabel: t('Delete'),
      danger: true,
    })
    if (!answer) return
    try {
      await api.deleteMovieFile(movie.libraryId)
      reload()
    } catch (e) {
      toast.error(e instanceof Error ? e.message : String(e))
    }
  }

  function reload() {
    api
      .tmdbMovieDetail(id)
      .then((m) => {
        if (m.tmdbId !== id) return // an answer for the movie we just left
        setMovie(m)
        setError('')
      })
      .catch((e) => setError(e instanceof Error ? e.message : String(e)))
    api.tmdbSimilarMovies(id).then(setSimilar).catch(() => undefined)
  }
  useEffect(reload, [id])
  // Only a title in the library changes by itself (a download starts, finishes); the details of one that isn't don't need re-reading.
  useLive(() => api.tmdbMovieDetail(id).then((m) => m.tmdbId === id && setMovie(m)).catch(() => undefined), 5000, !!movie?.libraryId)

  const libraryId = movie?.libraryId
  useEffect(() => {
    if (!libraryId) return
    api
      .getMovie(libraryId)
      .then((m) => setMonitored(m.monitored ?? true))
      .catch(() => undefined)
  }, [libraryId])

  async function toggleMonitored(v: boolean) {
    if (!libraryId) return
    try {
      await api.setMovieMonitored(libraryId, v)
      setMonitored(v)
      toast.success(v ? t('Monitored: looking for this movie.') : t('Unmonitored: this movie is left alone.'))
    } catch (e) {
      toast.error(e instanceof Error ? e.message : String(e))
    }
  }

  async function searchNow() {
    if (!libraryId) return
    setSearchingNow(true)
    try {
      const res = await api.searchNowMovie(libraryId)
      toast.success(res.message)
      if (res.grabbed > 0) setTimeout(reload, 500)
    } catch (e) {
      toast.error(e instanceof Error ? e.message : String(e))
    } finally {
      setSearchingNow(false)
    }
  }

  async function chooseRelease() {
    if (!libraryId) return
    setSearching(true)
    setResults(null)
    try {
      const a = await api.movieSearch(libraryId)
      setAnswered({ sources: a.sources, unanswered: a.unanswered })
      setResults(a.results)
    } catch (e) {
      toast.error(e instanceof Error ? e.message : String(e))
    } finally {
      setSearching(false)
    }
  }

  async function grab(r: SearchResult) {
    if (!libraryId) return
    setGrabbing(r.downloadUrl)
    try {
      await api.grab(libraryId, { releaseTitle: r.title, downloadUrl: r.downloadUrl, sizeBytes: r.sizeBytes, protocol: r.protocol })
      toast.success(t('Started downloading "{title}". Follow it in Activity.', { title: r.title }))
      setTimeout(reload, 500)
    } catch (e) {
      toast.error(e instanceof Error ? e.message : String(e))
    } finally {
      setGrabbing(null)
    }
  }

  async function onRemove() {
    if (!movie?.libraryId) return
    const usage = await api.movieDiskUsage(movie.libraryId).catch(() => null)
    const copies = (await api.titleCopies('movie', movie.libraryId).catch(() => null))?.items ?? []
    const answer = await confirm({
      title: t('Remove {title} from your library?', { title: movie.title }),
      body: (
        <p>
          {t('Mediarium stops tracking this movie and cancels anything still downloading for it. You can add it back any time.')}
          {copies.length > 0 && ' ' + plural(copies.length, 'Its second copy ({names}) goes too, with its file if you tick the box.', 'Its second copies ({names}) go too, with their files if you tick the box.', { names: copies.map((c) => c.profileName).join(', ') })}
        </p>
      ),
      confirmLabel: t('Remove'),
      danger: true,
      option: removeOption('movie', usage),
    })
    if (!answer) return
    const deleteFiles = answer.checked
    try {
      await api.deleteMovie(movie.libraryId, deleteFiles)
      toast.success(t('{title} removed.', { title: movie.title }))
      navigate('/library')
    } catch (e) {
      toast.error(e instanceof Error ? e.message : String(e))
    }
  }

  if (error && !movie) return <p className="error-text">{error}</p>
  if (!movie) return <div className="skeleton" style={{ height: 380, borderRadius: 22 }} />

  const inLibrary = !!movie.libraryId
  const stateKey = !inLibrary ? null : movie.status === 'downloaded' ? 'downloaded' : movie.status === 'downloading' ? 'downloading' : monitored === false ? 'added' : 'searching'
  const state = stateKey ? describeState(stateKey) : null

  return (
    <div>
      <TitleHero
        info={{
          ...movie,
          kind: 'movie',
          facts: [
            ...(movie.directors?.length ? [{ label: movie.directors.length > 1 ? t('Directors') : t('Director'), value: movie.directors.join(', ') }] : []),
            ...(movie.releaseDate ? [{ label: t('Released'), value: movie.releaseDate }] : []),
            ...(movie.releaseStatus ? [{ label: t('Status'), value: movie.releaseStatus }] : []),
          ],
          links: titleLinks('movie', movie.tmdbId, movie.imdbId, movie.homepage),
        }}
        status={
          state && (
            <span className={`state-tag st-${state.key}`} title={state.hint}>
              <Icon name={state.icon} size={13} /> {state.label}
              {movie.quality ? ` · ${qualityText(movie.quality)}` : ''}
            </span>
          )
        }
        actions={
          inLibrary ? (
            <>
              {can(me, 'manage') && (
                <button className="primary btn-with-icon" onClick={searchNow} disabled={searchingNow || movie.status === 'downloading'}>
                  <Icon name="search" size={16} /> {searchingNow ? t('Searching…') : movie.status === 'downloaded' ? t('Look for a better copy') : t('Find and download now')}
                </button>
              )}
              {can(me, 'manage') && (
                <InfoTip
                  text={
                    movie.status === 'downloaded'
                      ? t('Searches your indexers right now for a better release than the file you have. It only looks when "Better versions" is on and the file is below the profile\'s cutoff.')
                      : t('Searches your indexers right now and starts the best release for the quality profile, even if the movie is not monitored.')
                  }
                />
              )}
              {can(me, 'releases') && (
                <button className="btn-with-icon" onClick={chooseRelease} disabled={searching}>
                  <Icon name="list" size={16} /> {searching ? t('Loading…') : t('Choose a release')}
                </button>
              )}
              {can(me, 'releases') && <InfoTip text={t('Lists every release your indexers found for this movie, so you can pick one yourself instead of letting Mediarium choose.')} />}
              {movie.status === 'downloaded' && <WatchLinks tmdbId={movie.tmdbId} kind="movie" />}
              {admin && movie.status === 'downloaded' && movie.filePath && (
                <button className="btn-with-icon" onClick={() => void rescan()} disabled={rescanning} title={t('Looks at the file on your disk again: if it was deleted by hand the movie is wanted again, and a renamed file is found.')}>
                  <Icon name="folder" size={16} /> {rescanning ? t('Checking…') : t('Rescan files')}
                </button>
              )}
              {admin && movie.status === 'downloaded' && movie.filePath && (
                <button className="btn-with-icon danger-ghost" onClick={() => void deleteFile()} title={t('Delete the file and keep the movie')}>
                  <Icon name="trash" size={16} /> {t('Delete file')}
                </button>
              )}
              {admin && (
                <button className="btn-with-icon danger-ghost" onClick={onRemove}>
                  <Icon name="trash" size={16} /> {t('Remove')}
                </button>
              )}
            </>
          ) : (
            <button className="primary btn-with-icon big" onClick={() => setAdding(true)}>
              <Icon name="plus" size={18} /> {t('Add to library')}
            </button>
          )
        }
      >
        {inLibrary && movie.status === 'missing' && movie.libraryId && <SearchStatus kind="movie" id={movie.libraryId} />}
        {inLibrary && (
          <div className="title-controls">
            <Switch checked={monitored ?? true} onChange={toggleMonitored} disabled={!can(me, 'manage')} label={<span className="has-tip">{t('Monitored')}<InfoTip text={t('Mediarium keeps searching and downloads a release when one turns up. Switch it off to leave this movie alone; files you already have stay where they are.')} /></span>} description={t('Keeps looking until it finds this movie.')} />
            <ProfilePicker kind="movie" itemId={movie.libraryId!}>
              {movie.libraryId && <TitleTags kind="movie" id={movie.libraryId} />}
            </ProfilePicker>
            {movie.filePath && <code className="filepath">{movie.filePath}</code>}
          </div>
        )}
        {inLibrary && movie.libraryId && <WatchedLine kind="movie" id={movie.libraryId} />}
      </TitleHero>

      {!inLibrary && <CastCard cast={movie.cast ?? []} />}

      {adding && (
        <AddDialog
          target={{ kind: 'movie', tmdbId: movie.tmdbId, title: movie.title, year: movie.year, posterUrl: movie.posterUrl, overview: movie.overview }}
          onClose={() => setAdding(false)}
          onAdded={() => {
            setAdding(false)
            reload()
          }}
        />
      )}

      {results !== null && (
        <section ref={releasesRef} className="card" style={{ marginBottom: 24, scrollMarginTop: 80 }}>
          <h2>{t('Releases')}</h2>
          <ReleaseNotice sources={answered.sources} unanswered={answered.unanswered} results={results.length} />
          {results.length === 0 && (
            <button className="btn-sm" onClick={() => setResults(null)}>
              {t('Close')}
            </button>
          )}
          {results.length > 0 && <ReleaseTable results={results} grabbing={grabbing} onGrab={grab} onClose={() => setResults(null)} />}
        </section>
      )}

      {movie.libraryId && <TitleRequestActions kind="movie" tmdbId={movie.tmdbId} libraryId={movie.libraryId} title={movie.title} />}
      {movie.libraryId && <CopiesCard kind="movie" id={movie.libraryId} admin={admin} />}

      {movie.libraryId && (
        <section className="card" style={{ marginBottom: 24 }}>
          <h2>{t('What happened')}</h2>
          <TitleEvents kind="movie" id={movie.libraryId} />
        </section>
      )}

      {movie.libraryId && movie.status === 'downloaded' && (
        <>
          <div className={subtitlesOn ? 'half-cols' : undefined} style={{ marginBottom: subtitlesOn ? 24 : 12 }}>
            <section className="card">
              <h2>{t('Files')}</h2>
              <FilesPanel kind="movie" id={movie.libraryId} />
            </section>
            {subtitlesOn && (
              <section className="card">
                <h2>{t('Subtitles')}</h2>
                <SubtitlesPanel kind="movie" id={movie.libraryId} />
              </section>
            )}
          </div>
          {!subtitlesOn && <SubtitlesOffNote />}
        </>
      )}

      {movie.collection && <CollectionCard collection={movie.collection} admin={admin} onChanged={reload} />}

      {similar.length > 0 && (
        <section>
          <div className="rail-head">
            <h2>{t('Similar movies')}</h2>
          </div>
          <div className="poster-grid">
            {similar.map((m) => (
              <PosterCard key={m.tmdbId} to={`/title/${m.tmdbId}`} poster={m.posterUrl} title={m.title} meta={m.year || undefined} genres={m.genres} rating={m.rating} kind="movie" />
            ))}
          </div>
        </section>
      )}
    </div>
  )
}
