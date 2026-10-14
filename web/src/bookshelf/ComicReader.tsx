import { useCallback, useEffect, useRef, useState } from 'react'
import { Link } from 'react-router-dom'
import { api, type Book } from '../api'
import Icon from '../components/Icon'
import { withBase } from '../base'
import { t } from '../i18n'
import { animateTurn, saveTurn, storedTurn, type Turn, type TurnSpeed } from './turn'
import { openingPage } from './shelfMath'

type View = 'single' | 'double'
type Fit = 'page' | 'width'
type Direction = 'auto' | 'ltr' | 'rtl'
interface Prefs {
  view: View
  fit: Fit
  direction: Direction
}

const PREFS_KEY = 'mediarium.comic'

function loadPrefs(): Prefs {
  try {
    const p = JSON.parse(localStorage.getItem(PREFS_KEY) ?? '{}') as Partial<Prefs>
    return { view: p.view === 'double' ? 'double' : 'single', fit: p.fit === 'width' ? 'width' : 'page', direction: p.direction === 'ltr' || p.direction === 'rtl' ? p.direction : 'auto' }
  } catch {
    return { view: 'single', fit: 'page', direction: 'auto' }
  }
}

const pageUrl = (id: number, n: number) => withBase(`/api/books/${id}/comic/${n}`)

// The comic reader: one picture at a time (or two side by side), turned with
// the arrows, a swipe or a tap on the side of the page. A comic from the
// right-to-left world (manga) turns the other way round.
export default function ComicReader({ book }: { book: Book }) {
  const [info, setInfo] = useState<{ pages: number; rtl: boolean } | null>(null)
  const [error, setError] = useState('')
  const [page, setPage] = useState(1) // the first page on screen
  const [prefs, setPrefs] = useState<Prefs>(loadPrefs)
  const [turnPrefs, setTurnPrefs] = useState(storedTurn)
  const [panel, setPanel] = useState(false)
  const [bad, setBad] = useState<Set<number>>(new Set())
  const stage = useRef<HTMLDivElement>(null)
  const turning = useRef(false)
  const armed = useRef(false)
  const last = useRef(1)
  const saveTimer = useRef<number | undefined>(undefined)

  // Open: how many pages, and where this reader left off.
  useEffect(() => {
    let cancelled = false
    void (async () => {
      try {
        const [meta, saved] = await Promise.all([
          fetch(withBase(`/api/books/${book.id}/comic`), { credentials: 'include' }).then(async (r) => {
            if (!r.ok) throw new Error(((await r.json().catch(() => null)) as { error?: string } | null)?.error ?? t("This comic couldn't be opened."))
            return (await r.json()) as { pages: number; rtl?: boolean }
          }),
          api.bookProgress(book.id, 'ebook').catch(() => null),
        ])
        if (cancelled) return
        if (meta.pages < 1) throw new Error(t("This comic couldn't be opened."))
        const start = openingPage(saved?.position, meta.pages)
        // What a save sends: the place read from the server until a page is turned,
        // so opening and leaving again keeps it instead of going back to page 1.
        last.current = start
        setInfo({ pages: meta.pages, rtl: !!meta.rtl })
        setPage(start)
        window.setTimeout(() => {
          armed.current = true
        }, 800)
      } catch (e) {
        if (!cancelled) setError(e instanceof Error ? e.message : String(e))
      }
    })()
    return () => {
      cancelled = true
    }
  }, [book.id])

  const pages = info?.pages ?? 0
  const rtl = prefs.direction === 'rtl' || (prefs.direction === 'auto' && !!info?.rtl)
  // Two pages at a time: the first page (the cover) stands alone, then pairs.
  const step = prefs.view === 'double' ? 2 : 1
  const shown = prefs.view === 'double' && page > 1 && page + 1 <= pages ? [page, page + 1] : [page]

  const save = useCallback(
    (keepalive = false) => {
      if (!pages || !armed.current) return
      const p = last.current
      void api.saveBookProgress(book.id, { format: 'ebook', position: `page:${p}`, percent: Math.round((p / pages) * 1000) / 10, finished: p >= pages }, keepalive).catch(() => undefined)
    },
    [book.id, pages],
  )

  const goTo = useCallback(
    (n: number) => {
      const next = Math.min(Math.max(1, n), Math.max(1, pages))
      last.current = next
      setPage(next)
      window.clearTimeout(saveTimer.current)
      saveTimer.current = window.setTimeout(() => save(), 1200)
    },
    [pages, save],
  )

  const turn = useCallback(
    async (dir: 'next' | 'prev') => {
      if (turning.current || !pages) return
      const forward = dir === 'next'
      // In double mode the first page is alone, so from page 1 a step is one page and going back from page 2 is one too.
      const move = prefs.view === 'double' ? (forward ? (page === 1 ? 1 : step) : page <= 2 ? 1 : step) : 1
      const target = forward ? page + move : page - move
      if (target < 1 || target > pages) return
      turning.current = true
      try {
        await animateTurn(stage.current, turnPrefs.turn, turnPrefs.speed, dir, () => goTo(target))
      } finally {
        turning.current = false
      }
    },
    [pages, page, prefs.view, step, turnPrefs, goTo],
  )
  // The side the next page is on: right normally, left for a right-to-left comic.
  const next = useCallback(() => void turn(rtl ? 'prev' : 'next'), [rtl, turn])
  const prev = useCallback(() => void turn(rtl ? 'next' : 'prev'), [rtl, turn])

  // Keys: the arrows go the way the comic reads, Space and Page Down always forward.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if ((e.target as HTMLElement | null)?.closest?.('input, select, textarea, button')) return
      if (e.key === 'ArrowRight') void turn(rtl ? 'prev' : 'next')
      else if (e.key === 'ArrowLeft') void turn(rtl ? 'next' : 'prev')
      else if (e.key === ' ' || e.key === 'PageDown') void turn('next')
      else if (e.key === 'PageUp') void turn('prev')
    }
    document.addEventListener('keyup', onKey)
    const onHide = () => {
      if (document.visibilityState === 'hidden') save(true)
    }
    document.addEventListener('visibilitychange', onHide)
    window.addEventListener('pagehide', onHide)
    return () => {
      document.removeEventListener('keyup', onKey)
      document.removeEventListener('visibilitychange', onHide)
      window.removeEventListener('pagehide', onHide)
    }
  }, [turn, rtl, save])
  useEffect(() => () => {
    window.clearTimeout(saveTimer.current)
    save(true)
  }, [save])

  useEffect(() => {
    try {
      localStorage.setItem(PREFS_KEY, JSON.stringify(prefs))
    } catch {
      // the choice just isn't remembered
    }
  }, [prefs])

  // Swipe, and a tap on the left or right third of the page.
  const touch = useRef<{ x: number; y: number } | null>(null)
  const onTouchStart = (e: React.TouchEvent) => {
    touch.current = { x: e.changedTouches[0].clientX, y: e.changedTouches[0].clientY }
  }
  const onTouchEnd = (e: React.TouchEvent) => {
    const s = touch.current
    touch.current = null
    if (!s) return
    const dx = e.changedTouches[0].clientX - s.x
    const dy = e.changedTouches[0].clientY - s.y
    if (Math.abs(dx) > 50 && Math.abs(dx) > Math.abs(dy) * 1.5) {
      // Swipe left to go to the page on the right.
      if (dx < 0) next()
      else prev()
    }
  }

  // The next pictures are fetched while this one is read.
  useEffect(() => {
    if (!pages) return
    const ahead = [page + shown.length, page + shown.length + 1].filter((n) => n >= 1 && n <= pages)
    const imgs = ahead.map((n) => {
      const img = new Image()
      img.src = pageUrl(book.id, n)
      return img
    })
    return () => {
      imgs.forEach((i) => (i.src = ''))
    }
  }, [book.id, page, pages, shown.length])

  const chooseTurn = (patch: { turn?: Turn; speed?: TurnSpeed }) => {
    saveTurn(patch)
    setTurnPrefs((p) => ({ ...p, ...patch }))
  }

  if (error)
    return (
      <div className="bks-message">
        <Icon name="book" size={44} />
        <h1>{book.title}</h1>
        <p>{error}</p>
        <div className="row-actions">
          <a className="primary btn-with-icon" href={api.bookFileUrl(book.id, true)}>
            <Icon name="download" size={16} /> {t('Download')}
          </a>
          <Link className="btn-with-icon" to="/bookshelf">
            <Icon name="chevron-left" size={16} /> {t('Back to your books')}
          </Link>
        </div>
      </div>
    )
  if (!info) return <div className="bks-loading">{t('Opening…')}</div>

  const ordered = rtl ? [...shown].reverse() : shown
  const percent = pages > 1 ? ((page - 1) / (pages - 1)) * 100 : 100
  return (
    <div className="rdr cmx" dir="ltr">
      <header className="rdr-top">
        <Link className="icon-btn" to="/bookshelf" title={t('Back to your books')} aria-label={t('Back to your books')}>
          <Icon name="chevron-left" size={20} />
        </Link>
        <span className="rdr-title">
          <strong>{book.title}</strong>
          <small>{t('Page {n} of {total}', { n: shown.length > 1 ? `${shown[0]}–${shown[1]}` : shown[0], total: pages })}</small>
        </span>
        <button className={`icon-btn${panel ? ' active' : ''}`} onClick={() => setPanel((v) => !v)} title={t('View')} aria-label={t('View')}>
          <Icon name="sliders" size={18} />
        </button>
        <button
          className="icon-btn"
          onClick={() => (document.fullscreenElement ? void document.exitFullscreen() : void document.documentElement.requestFullscreen?.())}
          title={t('Full screen')}
          aria-label={t('Full screen')}
        >
          <Icon name="monitor" size={18} />
        </button>
      </header>

      {panel && (
        <div className="rdr-panel rdr-settings">
          <div className="rdr-row">
            <span>{t('Pages')}</span>
            {(['single', 'double'] as View[]).map((v) => (
              <button key={v} className={`btn-sm${prefs.view === v ? ' primary' : ''}`} onClick={() => setPrefs((p) => ({ ...p, view: v }))}>
                {v === 'single' ? t('One page') : t('Two pages')}
              </button>
            ))}
          </div>
          <div className="rdr-row">
            <span>{t('Fit')}</span>
            {(['page', 'width'] as Fit[]).map((f) => (
              <button key={f} className={`btn-sm${prefs.fit === f ? ' primary' : ''}`} onClick={() => setPrefs((p) => ({ ...p, fit: f }))}>
                {f === 'page' ? t('Whole page') : t('Width')}
              </button>
            ))}
          </div>
          <div className="rdr-row">
            <span>{t('Reading direction')}</span>
            {(['auto', 'ltr', 'rtl'] as Direction[]).map((d) => (
              <button key={d} className={`btn-sm${prefs.direction === d ? ' primary' : ''}`} onClick={() => setPrefs((p) => ({ ...p, direction: d }))}>
                {d === 'auto' ? t('Automatic') : d === 'ltr' ? t('Left to right') : t('Right to left')}
              </button>
            ))}
          </div>
          <div className="rdr-row">
            <span>{t('Page turn')}</span>
            {(['slide', 'fade', 'none'] as Turn[]).map((x) => (
              <button key={x} className={`btn-sm${turnPrefs.turn === x ? ' primary' : ''}`} onClick={() => chooseTurn({ turn: x })}>
                {x === 'slide' ? t('Slide') : x === 'fade' ? t('Fade') : t('None')}
              </button>
            ))}
          </div>
          {turnPrefs.turn !== 'none' && (
            <div className="rdr-row">
              <span>{t('Turn speed')}</span>
              {(['fast', 'normal', 'slow'] as TurnSpeed[]).map((x) => (
                <button key={x} className={`btn-sm${turnPrefs.speed === x ? ' primary' : ''}`} onClick={() => chooseTurn({ speed: x })}>
                  {x === 'fast' ? t('Fast') : x === 'normal' ? t('Normal') : t('Slow')}
                </button>
              ))}
            </div>
          )}
        </div>
      )}

      <main className={`cmx-stage fit-${prefs.fit}`} onTouchStart={onTouchStart} onTouchEnd={onTouchEnd}>
        <button className="rdr-turn prev" onClick={prev} aria-label={rtl ? t('Next page') : t('Previous page')} disabled={rtl ? page + shown.length > pages : page <= 1}>
          <Icon name="chevron-left" size={26} />
        </button>
        <div className="cmx-pages" ref={stage}>
          {ordered.map((n) =>
            bad.has(n) ? (
              <div key={n} className="cmx-bad">
                {t("This page can't be shown.")}
              </div>
            ) : (
              <img key={n} src={pageUrl(book.id, n)} alt={t('Page {n} of {total}', { n, total: pages })} draggable={false} onError={() => setBad((s) => new Set(s).add(n))} />
            ),
          )}
        </div>
        <button className="rdr-turn next" onClick={next} aria-label={rtl ? t('Previous page') : t('Next page')} disabled={rtl ? page <= 1 : page + shown.length > pages}>
          <Icon name="chevron-right" size={26} />
        </button>
      </main>

      <footer className="rdr-foot">
        <input
          type="range"
          min={1}
          max={pages}
          step={1}
          value={page}
          dir={rtl ? 'rtl' : 'ltr'}
          aria-label={t('Place in the book')}
          onChange={(e) => goTo(Number(e.target.value))}
        />
        <span className="rdr-pct">{Math.round(percent)}%</span>
      </footer>
    </div>
  )
}
