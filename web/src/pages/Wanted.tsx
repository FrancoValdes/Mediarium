import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { Link } from 'react-router-dom'
import { useUrlState } from '../useUrlState'
import { api, can, isAdmin, type Book, type BookFormat, type Movie, type MusicWanted, type Series, type SubtitleQuota, type SubtitleWanted, type WaitingRelease, type WantedItem } from '../api'
import { releaseLabel, localDate } from '../bookSeries'
import { useAuth } from '../AuthContext'
import { useConfirm } from '../components/ConfirmProvider'
import Cover from '../components/Cover'
import Icon, { type IconName } from '../components/Icon'
import Loading from '../components/Loading'
import InfoTip from '../components/InfoTip'
import HelpLink from '../components/HelpLink'
import { PosterFallback } from '../components/PosterCard'
import { KIND_LABEL, useKinds, useModules, type MediaKind } from '../ModulesContext'
import QuotaNote from '../components/QuotaNote'
import NoSourceNote from '../components/NoSourceNote'
import { noSourceInQuota } from '../subtitleSources'
import { describeState } from '../components/state'
import { useToast } from '../components/Toast'
import { languageName } from '../languages'
import { slotName } from '../subtitleProfiles'
import { useLive } from '../useLive'
import { plural, t, st } from '../i18n'
import { untilText } from '../delayText'

type Kind = 'missing' | 'cutoff' | 'subtitles'

// Rows shown before "Show more".
const PAGE = 50
// Above this many titles, "Search all" asks first: indexers often limit how
// many searches you may make.
const ASK_ABOVE = 25

// A row of the Missing and Upgrades lists: one movie, or one show with its
// episodes folded together.
type Row = { type: 'item'; item: WantedItem } | { type: 'show'; seriesId: number; title: string; items: WantedItem[] }

function groupRows(items: WantedItem[]): Row[] {
  const rows: Row[] = []
  const shows = new Map<number, Extract<Row, { type: 'show' }>>()
  for (const i of items) {
    if (i.kind === 'episode' && i.seriesId) {
      let g = shows.get(i.seriesId)
      if (!g) {
        g = { type: 'show', seriesId: i.seriesId, title: i.title, items: [] }
        shows.set(i.seriesId, g)
        rows.push(g)
      }
      g.items.push(i)
    } else {
      rows.push({ type: 'item', item: i })
    }
  }
  // A show with one episode reads better as that episode.
  return rows.map((r) => (r.type === 'show' && r.items.length === 1 ? { type: 'item', item: r.items[0] } : r))
}

// The kinds the list can be narrowed to: the media kinds, and books.
type ListKind = MediaKind | 'book'
const KIND_ICON: Record<ListKind, IconName> = { movie: 'film', tv: 'tv', music: 'music', book: 'book' }
const LIST_LABEL = (k: ListKind) => (k === 'book' ? t('Books') : KIND_LABEL[k])

// Wanted: what Mediarium is still hunting for. "Missing" is monitored movies
// with nothing downloaded and aired episodes without a file; "Upgrades" is
// downloaded items still below their profile's cutoff; "Subtitles" is
// downloaded titles missing a language you asked for.
export default function Wanted() {
  const toast = useToast()
  // Marking subtitles as not needed is for administrators.
  const me = useAuth().user
  const admin = isAdmin(me)
  // Searching needs the "manage" permission; a viewer or a basic account without it only looks.
  const canSearch = can(me, 'manage')
  // The tab, the kind chip and the search live in the address, so Back returns to them.
  const [ui, patchUi] = useUrlState({ tab: 'missing', media: 'all', q: '' })
  const chosenKind: Kind = ui.tab === 'subtitles' ? 'subtitles' : ui.tab === 'cutoff' ? 'cutoff' : 'missing'
  const setKind = (k: Kind) => patchUi({ tab: k })
  // The Subtitles tab, its count and its list exist only while subtitles are switched on.
  const { subtitlesOn } = useModules()
  const kind: Kind = chosenKind === 'subtitles' && !subtitlesOn ? 'missing' : chosenKind
  const [quota, setQuota] = useState<SubtitleQuota | null>(null)
  const [picked, setPicked] = useState<Set<string>>(new Set())
  const [showDismissed, setShowDismissed] = useState(false)
  const [items, setItems] = useState<WantedItem[] | null>(null)
  const [subs, setSubs] = useState<SubtitleWanted[] | null>(null)
  const [movies, setMovies] = useState<Movie[]>([])
  const [series, setSeries] = useState<Series[]>([])
  const [sweeping, setSweeping] = useState(false)
  // Releases held back by the delay profile (Settings > Languages and automation).
  const [waiting, setWaiting] = useState<WaitingRelease[]>([])
  const [error, setError] = useState('')
  const [busy, setBusy] = useState<string | null>(null)
  const [counts, setCounts] = useState<{ missing?: number; cutoff?: number; subtitles?: number }>({})
  // Music albums, when the music module is on, share the same lists.
  const mediaKinds = useKinds()
  const musicOn = mediaKinds.includes('music')
  const { on } = useModules()
  const booksOn = on('ebooks') || on('audiobooks')
  const kinds: ListKind[] = [...mediaKinds, ...(booksOn ? (['book'] as const) : [])]
  const media = ui.media as 'all' | ListKind
  const setMedia = (m: 'all' | ListKind) => patchUi({ media: m })
  // Books wanted in a format that is still missing (books have no upgrades).
  const [bookList, setBookList] = useState<Book[] | null>(null)
  const [music, setMusic] = useState<MusicWanted[] | null>(null)
  const [musicCounts, setMusicCounts] = useState<{ missing?: number; cutoff?: number }>({})
  // The search box, how many rows are shown, which shows are unfolded, and
  // the progress of "Search all".
  const query = ui.q
  const setQuery = (v: string) => patchUi({ q: v })
  const [limit, setLimit] = useState(PAGE)
  const [unfolded, setUnfolded] = useState<Set<number>>(new Set())
  const [bulk, setBulk] = useState<{ done: number; total: number } | null>(null)
  const confirm = useConfirm()

  useEffect(() => {
    api.listMovies().then(setMovies).catch(() => undefined)
    api.listSeries().then(setSeries).catch(() => undefined)
    api.searchWaiting().then(setWaiting).catch(() => undefined)
    api.getWanted('missing').then((r) => setCounts((c) => ({ ...c, missing: r.length }))).catch(() => undefined)
    api.getWanted('cutoff').then((r) => setCounts((c) => ({ ...c, cutoff: r.length }))).catch(() => undefined)
  }, [])
  useEffect(() => {
    if (!subtitlesOn) return setCounts((c) => ({ ...c, subtitles: undefined }))
    api.subtitlesWanted().then((r) => setCounts((c) => ({ ...c, subtitles: r.length }))).catch(() => undefined)
  }, [subtitlesOn])
  useEffect(() => {
    if (!musicOn) return setMusicCounts({})
    api.musicWanted('missing').then((r) => setMusicCounts((c) => ({ ...c, missing: r.length }))).catch(() => undefined)
    api.musicWanted('cutoff').then((r) => setMusicCounts((c) => ({ ...c, cutoff: r.length }))).catch(() => undefined)
  }, [musicOn])

  const posterOf = useMemo(() => {
    const byMovie = new Map(movies.map((m) => [m.tmdbId, m.posterUrl]))
    const bySeries = new Map(series.map((s) => [s.id, s.posterUrl]))
    return (i: { kind: string; tmdbId?: number; seriesId?: number }) => (i.kind === 'movie' ? byMovie.get(i.tmdbId ?? -1) : bySeries.get(i.seriesId ?? -1))
  }, [movies, series])

  // Each load and each quiet refresh belongs to the tab it started on; an answer that arrives after another tab was picked is dropped.
  const epoch = useRef(0)
  const load = useCallback(() => {
    const mine = ++epoch.current
    const current = () => mine === epoch.current
    setError('')
    if (kind === 'subtitles') {
      setSubs(null)
      api.subtitlesWanted(showDismissed).then((r) => current() && setSubs(r)).catch((e) => current() && setError(e instanceof Error ? e.message : String(e)))
      api.subtitleQuota().then(setQuota).catch(() => undefined)
      return
    }
    setItems(null)
    setMusic(null)
    api.getWanted(kind).then((r) => current() && setItems(r)).catch((e) => current() && setError(e instanceof Error ? e.message : String(e)))
    if (musicOn) api.musicWanted(kind).then((r) => current() && setMusic(r)).catch(() => current() && setMusic([]))
    if (booksOn) api.listBooks().then(setBookList).catch(() => setBookList([]))
  }, [kind, showDismissed, musicOn, booksOn])
  useEffect(load, [load])
  // Quiet refresh: update the lists in place without flashing the loading state.
  useLive(() => {
    const mine = epoch.current
    const current = () => mine === epoch.current
    if (kind === 'subtitles') {
      api
        .subtitlesWanted(showDismissed)
        .then((r) => {
          if (!current()) return
          setSubs(r)
          setError('')
        })
        .catch(() => undefined)
      api.subtitleQuota().then(setQuota).catch(() => undefined)
    } else {
      api
        .getWanted(kind)
        .then((r) => {
          if (!current()) return
          setItems(r)
          setError('')
        })
        .catch(() => undefined)
      if (musicOn) api.musicWanted(kind).then((r) => current() && setMusic(r)).catch(() => undefined)
      if (booksOn) api.listBooks().then(setBookList).catch(() => undefined)
    }
  }, 10000)

  const subKey = (i: { kind: string; id: number }) => `${i.kind}-${i.id}`
  const togglePick = (k: string) =>
    setPicked((cur) => {
      const n = new Set(cur)
      if (n.has(k)) n.delete(k)
      else n.add(k)
      return n
    })

  async function getSubtitles(all: boolean) {
    setSweeping(true)
    try {
      const items = (subs ?? []).filter((i) => picked.has(subKey(i)) && !i.dismissed).map((i) => ({ kind: i.kind, id: i.id }))
      const r = await api.subtitlesGet(all ? { all: true } : { items })
      setQuota(r.quota)
      if (r.stopped) toast.info(r.message)
      else toast.success(r.message)
      setPicked(new Set())
      load()
    } catch (e) {
      toast.error(e instanceof Error ? e.message : String(e))
    } finally {
      setSweeping(false)
    }
  }

  async function dismiss(undo: boolean) {
    const items = (subs ?? []).filter((i) => picked.has(subKey(i))).map((i) => ({ kind: i.kind, id: i.id }))
    if (items.length === 0) return
    try {
      await (undo ? api.undismissSubtitles(items) : api.dismissSubtitles(items))
      toast.success(undo ? t('Back on the list.') : t('Marked as not needed.'))
      setPicked(new Set())
      load()
    } catch (e) {
      toast.error(e instanceof Error ? e.message : String(e))
    }
  }

  const keyOf = (i: WantedItem) => `${i.kind}-${i.id}`

  async function searchNow(item: WantedItem) {
    setBusy(keyOf(item))
    try {
      const res =
        item.kind === 'movie'
          ? await api.searchNowMovie(item.movieId ?? item.id)
          : await api.searchNowSeries(item.seriesId!, { season: item.season, episode: item.episode })
      toast.info(`${item.title}${item.subtitle ? ' ' + item.subtitle.split(' · ')[0] : ''}: ${st(res.message)}`)
      if (res.grabbed > 0) load()
    } catch (e) {
      toast.error(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(null)
    }
  }

  const link = (i: { kind: string; tmdbId?: number; seriesId?: number }) => (i.kind === 'movie' ? `/title/${i.tmdbId}` : `/series/${i.seriesId}`)
  const missingState = describeState('searching')

  async function searchBookNow(b: Book, f: BookFormat) {
    setBusy(`book-${b.id}-${f}`)
    try {
      const res = await api.searchNowBook(b.id, f)
      toast.info(`${b.title}: ${st(res.message)}`)
      if (res.grabbed > 0) load()
    } catch (e) {
      toast.error(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(null)
    }
  }

  async function searchMusicNow(a: MusicWanted) {
    setBusy(`music-${a.id}`)
    try {
      const res = await api.searchNowAlbum(a.id)
      toast.info(`${a.artistName} – ${a.title}: ${st(res.message)}`)
      if (res.grabbed > 0) load()
    } catch (e) {
      toast.error(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(null)
    }
  }

  // Searches every title in the list now, one after another: one search per
  // movie and one per show (which covers all its missing episodes).
  async function searchAll(rows: Row[]) {
    if (rows.length > ASK_ABOVE) {
      const ok = await confirm({
        title: plural(rows.length, 'Search for {n} title now?', 'Search for {n} titles now?'),
        body: <p>{t("Each title is one search on every indexer. Many indexers allow only so many searches a day, so this may use up today's limit. The automatic searches go on as usual either way.")}</p>,
        confirmLabel: plural(rows.length, 'Search {n} title', 'Search {n} titles'),
      })
      if (!ok) return
    }
    setBulk({ done: 0, total: rows.length })
    let grabbed = 0
    for (let n = 0; n < rows.length; n++) {
      const r = rows[n]
      try {
        const res = r.type === 'show' ? await api.searchNowSeries(r.seriesId, {}) : r.item.kind === 'movie' ? await api.searchNowMovie(r.item.movieId ?? r.item.id) : await api.searchNowSeries(r.item.seriesId!, { season: r.item.season, episode: r.item.episode })
        grabbed += res.grabbed
      } catch {
        // one failed search does not stop the rest
      }
      setBulk({ done: n + 1, total: rows.length })
    }
    setBulk(null)
    toast.info(
      grabbed > 0
        ? plural(grabbed, 'Searched {count} titles and found {n} download.', 'Searched {count} titles and found {n} downloads.', { count: rows.length })
        : plural(rows.length, 'Searched {n} title. Nothing suitable was found right now.', 'Searched {n} titles. Nothing suitable was found right now.'),
    )
    if (grabbed > 0) load()
  }

  async function searchShow(seriesId: number, title: string) {
    setBusy(`show-${seriesId}`)
    try {
      const res = await api.searchNowSeries(seriesId, {})
      toast.info(`${title}: ${st(res.message)}`)
      if (res.grabbed > 0) load()
    } catch (e) {
      toast.error(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(null)
    }
  }

  // The hold on an item: a movie, an episode, or the season pack it is part of.
  const heldFor = (i: WantedItem): WaitingRelease | undefined =>
    waiting.find((w) => (i.kind === 'movie' ? w.movieId === i.movieId : w.seriesId === i.seriesId && w.season === (i.season ?? 0) && ((w.episode ?? 0) === 0 || w.episode === i.episode)))
  const itemRow = (i: WantedItem) => (
            <div key={keyOf(i)} className={`wrow kind-${i.kind === 'movie' ? 'movie' : 'tv'}`}>
              <div className="qthumb">{posterOf(i) ? <img src={posterOf(i)} alt="" loading="lazy" /> : <PosterFallback />}</div>
              <div className="wmain">
                <Link to={link(i)}>
                  <strong>
                    <Icon name={i.kind === 'movie' ? 'film' : 'tv'} size={13} /> {i.title}
                  </strong>
                </Link>
                {i.subtitle && <small>{i.subtitle}</small>}
              </div>
              <div className="wmeta">
                {kind === 'missing' && heldFor(i) ? (
                  <span className="state-tag st-pending" title={t('A {protocol} release was found. It is held back so a copy from the better protocol has time to turn up.', { protocol: heldFor(i)?.protocol === 'torrent' ? t('torrent') : t('Usenet') })}>
                    <Icon name="clock" size={12} /> {t('Waiting for a better protocol until {time}', { time: untilText(heldFor(i)?.until ?? '') })}
                  </span>
                ) : kind === 'missing' ? (
                  <span className={`state-tag st-${missingState.key}`} title={missingState.hint}>
                    <Icon name={missingState.icon} size={12} /> {missingState.label}
                  </span>
                ) : (
                  <span className="state-tag st-partial" title={t('Downloaded, but below the quality you asked for')}>
                    <Icon name="star" size={12} /> {i.quality} → {i.cutoff}
                  </span>
                )}
                {kind === 'missing' && i.date && <small>{i.date}</small>}
                {i.profileName && <span className="badge">{i.profileName}</span>}
              </div>
              <div className="row-actions">
                {canSearch && (
                  <button className="primary btn-sm btn-with-icon" disabled={busy !== null} onClick={() => void searchNow(i)}>
                    <Icon name="search" size={14} /> {busy === keyOf(i) ? t('Searching…') : t('Search now')}
                  </button>
                )}
                <Link className="icon-btn" to={link(i)} title={t('Open')} aria-label={t('Open')}>
                  <Icon name="open" size={17} />
                </Link>
              </div>
            </div>
  )

  const missingBooks = (booksOn ? (bookList ?? []) : []).flatMap((b) =>
    (['ebook', 'audiobook'] as BookFormat[]).filter((f) => on(f === 'ebook' ? 'ebooks' : 'audiobooks') && b[f].wanted && b[f].status === 'missing').map((f) => ({ book: b, format: f })),
  )
  const total = (k: Kind) => (counts[k] === undefined ? undefined : counts[k]! + (k === 'subtitles' ? 0 : (musicCounts[k] ?? 0)) + (k === 'missing' ? missingBooks.length : 0))
  const tab = (k: Kind, label: string) => (
    <button className={kind === k ? 'active' : ''} onClick={() => setKind(k)}>
      {label} {total(k) !== undefined && <small>({total(k)})</small>}
    </button>
  )

  // Which kinds of media to list, when more than one is switched on.
  const sel = media === 'all' || kinds.includes(media) ? media : 'all'
  const q = query.trim().toLowerCase()
  const matches = (text: string) => q === '' || text.toLowerCase().includes(q)
  const shownItems = (items ?? []).filter((i) => (sel === 'all' || (sel === 'movie' && i.kind === 'movie') || (sel === 'tv' && i.kind === 'episode')) && matches(i.title))
  const shownMusic = (sel === 'all' || sel === 'music' ? (music ?? []) : []).filter((a) => matches(`${a.artistName} ${a.title}`))
  const rows = groupRows(shownItems)
  const pagedRows = rows.slice(0, limit)
  const musicRoom = Math.max(0, limit - rows.length)
  const today = localDate(new Date())
  const shownBooks = kind === 'missing' && (sel === 'all' || sel === 'book') ? missingBooks.filter((x) => matches(`${x.book.author} ${x.book.title}`)) : []
  const bookRoom = Math.max(0, musicRoom - shownMusic.length)
  const loadingList = items === null || (musicOn && music === null) || (booksOn && bookList === null)
  const listed = rows.length + shownMusic.length + shownBooks.length
  const kindChips = kinds.length > 1 && kind !== 'subtitles' && (
    <div className="chip-row" style={{ marginBottom: 16, alignItems: 'center' }}>
      {(['all', ...kinds] as const).map((k) => (
        <button key={k} className={`chip${sel === k ? ' active' : ''}`} onClick={() => setMedia(k)}>
          {k !== 'all' && <Icon name={KIND_ICON[k]} size={13} />} {k === 'all' ? t('All') : LIST_LABEL(k)}
        </button>
      ))}
      {sel !== 'all' && (
        <button className="btn-sm" onClick={() => setMedia('all')}>
          {t('Clear filters')}
        </button>
      )}
    </div>
  )

  return (
    <div>
      <div className="page-header">
        <h1>{t('Wanted')}</h1>
        <div className="seg">
          {tab('missing', t('Missing'))}
          {tab('cutoff', t('Upgrades'))}
          {subtitlesOn && tab('subtitles', t('Subtitles'))}
        </div>
        <HelpLink topic="activity#the-wanted-page" text={t('Missing: monitored titles that have no file yet. Upgrades: titles you have that are below the quality you asked for. Subtitles: downloaded titles that lack a subtitle in your languages.')} />
      </div>

      {error && <p className="error-text">{error}</p>}
      {kindChips}
      {kind !== 'subtitles' && (items?.length ?? 0) + (music?.length ?? 0) + (kind === 'missing' ? missingBooks.length : 0) > 0 && (
        <div className="toolbar">
          <input className="wanted-search" type="search" placeholder={t('Find a title')} aria-label={t('Find a title in this list')} value={query} onChange={(e) => { setQuery(e.target.value); setLimit(PAGE) }} />
          <span className="spacer" />
          {rows.length > 0 && canSearch && (
            <button className="btn-with-icon" disabled={bulk !== null} onClick={() => void searchAll(rows)}>
              <Icon name="search" size={15} /> {bulk ? t('Searching {done} of {total}…', { done: bulk.done, total: bulk.total }) : t('Search all {n}', { n: rows.length })}
            </button>
          )}
          {rows.length > 0 && canSearch && (
            <InfoTip text={t('Runs one search per title in this list, one after another, on every indexer. Many indexers allow only so many searches a day, so a long list can use up the limit.')} />
          )}
        </div>
      )}

      {kind === 'subtitles' ? (
        subs === null ? (
          error ? null : <Loading height={120} />
        ) : (
          <>
            <QuotaNote quota={quota} />
            <NoSourceNote quota={quota} />
            <p style={{ color: 'var(--text-dim)', marginTop: 0 }}>
              {t("Downloaded titles with no subtitle in your languages. Pick the ones you want, or get them all, up to today's limit.")}
            </p>
            <div className="toolbar">
              <button className="btn-sm" onClick={() => setPicked(new Set(subs.filter((i) => !i.dismissed).map(subKey)))}>
                {t('Select all')}
              </button>
              <button className="primary btn-with-icon" onClick={() => void getSubtitles(false)} disabled={sweeping || picked.size === 0 || noSourceInQuota(quota)}>
                <Icon name="download" size={16} /> {sweeping ? t('Working…') : picked.size > 0 ? t('Get subtitles for {n} selected', { n: picked.size }) : t('Get subtitles for selected')}
              </button>
              <button className="btn-with-icon" onClick={() => void getSubtitles(true)} disabled={sweeping || subs.filter((i) => !i.dismissed).length === 0 || noSourceInQuota(quota)}>
                {t('Get all')}
              </button>
              <span className="spacer" />
              {admin && (
                <button className="btn-sm" onClick={() => void dismiss(showDismissed && subs.some((i) => picked.has(subKey(i)) && i.dismissed))} disabled={picked.size === 0}>
                  {showDismissed && subs.some((i) => picked.has(subKey(i)) && i.dismissed) ? t('Put back on the list') : t('Not needed')}
                </button>
              )}
              <label className="check-row" style={{ margin: 0 }}>
                <input type="checkbox" checked={showDismissed} onChange={(e) => setShowDismissed(e.target.checked)} /> {t('Show titles marked as not needed')}
              </label>
              <InfoTip text={t('Not needed takes a title off this list, for example when you do not want subtitles for it. Tick this box to see those titles again; Put back on the list brings one back.')} />
            </div>
            {subs.length === 0 ? (
              <div className="empty-state">
                <Icon name="check" size={44} />
                <p>{t('Every downloaded title has all its subtitles.')}</p>
              </div>
            ) : (
              <div className="wlist">
                {subs.map((s) => (
                  <div key={subKey(s)} className={`wrow kind-${s.kind === 'movie' ? 'movie' : 'tv'}${s.dismissed ? ' dismissed' : ''}`}>
                    <label className="pick-box" title={t('Select')}>
                      <input type="checkbox" checked={picked.has(subKey(s))} onChange={() => togglePick(subKey(s))} />
                    </label>
                    <div className="qthumb">{posterOf(s) ? <img src={posterOf(s)} alt="" loading="lazy" /> : <PosterFallback />}</div>
                    <div className="wmain">
                      <Link to={link(s)}>
                        <strong>{s.title}</strong>
                      </Link>
                      {s.subtitle && <small>{s.subtitle}</small>}
                    </div>
                    <div className="wmeta">
                      {s.dismissed && <span className="badge">{t('not needed')}</span>}
                      {s.missing.map((m) => (
                        <span key={m} className="badge missing">
                          {slotName(m, languageName)}
                        </span>
                      ))}
                    </div>
                    <Link className="icon-btn" to={link(s)} title={t('Open')} aria-label={t('Open')}>
                      <Icon name="open" size={17} />
                    </Link>
                  </div>
                ))}
              </div>
            )}
          </>
        )
      ) : loadingList ? (
        error ? null : <Loading height={120} />
      ) : shownItems.length === 0 && shownMusic.length === 0 && shownBooks.length === 0 ? (
        <div className="empty-state">
          <Icon name="check" size={44} />
          {sel !== 'all' && (items?.length ?? 0) + (music?.length ?? 0) + (kind === 'missing' ? missingBooks.length : 0) > 0 ? (
            <>
              <p>{t('Nothing matches this filter.')}</p>
              <button className="btn-with-icon" onClick={() => setMedia('all')}>
                {t('Clear filters')}
              </button>
            </>
          ) : (
            <p>{kind === 'missing' ? t("Nothing is missing. Everything you're monitoring is downloaded or hasn't come out yet.") : t("Everything you've downloaded is already at the quality you asked for.")}</p>
          )}
        </div>
      ) : (
        <div className="wlist">
          {pagedRows.map((r) =>
            r.type === 'item' ? (
              itemRow(r.item)
            ) : (
              <div key={`show-${r.seriesId}`} className="wgroup">
                <div className="wrow kind-tv">
                  <div className="qthumb">{posterOf(r.items[0]) ? <img src={posterOf(r.items[0])} alt="" loading="lazy" /> : <PosterFallback />}</div>
                  <div className="wmain">
                    <Link to={link(r.items[0])}>
                      <strong>
                        <Icon name="tv" size={13} /> {r.title}
                      </strong>
                    </Link>
                    <small>
                      {kind === 'missing' ? plural(r.items.length, '{n} episode missing', '{n} episodes missing') : plural(r.items.length, '{n} episode below the quality you asked for', '{n} episodes below the quality you asked for')}
                    </small>
                  </div>
                  <div className="wmeta">{r.items[0].profileName && <span className="badge">{r.items[0].profileName}</span>}</div>
                  <div className="row-actions">
                    <button className="btn-sm" aria-expanded={unfolded.has(r.seriesId)} onClick={() => setUnfolded((u) => { const n = new Set(u); if (n.has(r.seriesId)) n.delete(r.seriesId); else n.add(r.seriesId); return n })}>
                      {unfolded.has(r.seriesId) ? t('Hide episodes') : t('Show episodes')}
                    </button>
                    {canSearch && (
                      <button className="primary btn-sm btn-with-icon" disabled={busy !== null} onClick={() => void searchShow(r.seriesId, r.title)}>
                        <Icon name="search" size={14} /> {busy === `show-${r.seriesId}` ? t('Searching…') : t('Search show')}
                      </button>
                    )}
                  </div>
                </div>
                {unfolded.has(r.seriesId) && <div className="wgroup-items">{r.items.map((i) => itemRow(i))}</div>}
              </div>
            ),
          )}
          {shownMusic.slice(0, musicRoom).map((a) => {
            const to = `/music/artist/${a.artistId}#album-${a.id}`
            return (
              <div key={`music-${a.id}`} className="wrow kind-music">
                <div className="qthumb">
                  <Cover src={a.coverUrl} />
                </div>
                <div className="wmain">
                  <Link to={to}>
                    <strong>
                      <Icon name="music" size={13} /> {a.artistName} – {a.title}
                    </strong>
                  </Link>
                  <small>{[a.year || null, a.type === 'ep' ? 'EP' : a.type === 'single' ? t('Single') : t('Album')].filter(Boolean).join(' · ')}</small>
                </div>
                <div className="wmeta">
                  {kind === 'missing' ? (
                    <span className={`state-tag st-${missingState.key}`} title={a.lastSearch || missingState.hint}>
                      <Icon name={missingState.icon} size={12} /> {missingState.label}
                    </span>
                  ) : (
                    <span className="state-tag st-partial" title={t('Downloaded, but below the quality you asked for')}>
                      <Icon name="star" size={12} /> {a.quality} → {a.cutoff}
                    </span>
                  )}
                  {a.profileName && <span className="badge">{a.profileName}</span>}
                </div>
                <div className="row-actions">
                  {canSearch && (
                    <button className="primary btn-sm btn-with-icon" disabled={busy !== null} onClick={() => void searchMusicNow(a)}>
                      <Icon name="search" size={14} /> {busy === `music-${a.id}` ? t('Searching…') : t('Search now')}
                    </button>
                  )}
                  <Link className="icon-btn" to={to} title={t('Open')} aria-label={t('Open')}>
                    <Icon name="open" size={17} />
                  </Link>
                </div>
              </div>
            )
          })}
          {shownBooks.slice(0, bookRoom).map(({ book: b, format: f }) => (
            <div key={`book-${b.id}-${f}`} className="wrow kind-book">
              <div className="qthumb">{b.coverUrl ? <img src={b.coverUrl.replace('-M.jpg', '-S.jpg')} alt="" loading="lazy" /> : <PosterFallback />}</div>
              <div className="wmain">
                <Link to={`/book/${b.id}`}>
                  <strong>
                    <Icon name={f === 'ebook' ? 'book' : 'headphones'} size={13} /> {b.title}
                  </strong>
                </Link>
                <small>{[f === 'ebook' ? t('Ebook') : t('Audiobook'), b.author, b.year].filter(Boolean).join(' · ')}</small>
              </div>
              <div className="wmeta">
                {b.releaseDate && b.releaseDate > today ? (
                  <span className="state-tag st-upcoming" title={t('Mediarium starts looking for it on the day it comes out.')}>
                    <Icon name="calendar" size={12} /> {t('Out {date}', { date: releaseLabel(b.releaseDate) })}
                  </span>
                ) : (
                  <span className={`state-tag st-${missingState.key}`} title={missingState.hint}>
                    <Icon name={missingState.icon} size={12} /> {missingState.label}
                  </span>
                )}
              </div>
              <div className="row-actions">
                {canSearch && !(b.releaseDate && b.releaseDate > today) && (
                  <button className="primary btn-sm btn-with-icon" disabled={busy !== null} onClick={() => void searchBookNow(b, f)}>
                    <Icon name="search" size={14} /> {busy === `book-${b.id}-${f}` ? t('Searching…') : t('Search now')}
                  </button>
                )}
                <Link className="icon-btn" to={`/book/${b.id}`} title={t('Open')} aria-label={t('Open')}>
                  <Icon name="open" size={17} />
                </Link>
              </div>
            </div>
          ))}
        </div>
      )}
      {kind !== 'subtitles' && listed > limit && (
        <div className="rail-foot">
          <button className="btn-sm" onClick={() => setLimit((l) => l + PAGE)}>
            {t('Show more ({n} left)', { n: listed - limit })}
          </button>
        </div>
      )}
    </div>
  )
}
