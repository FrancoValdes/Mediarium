import { useCallback, useEffect, useRef, useState } from 'react'
import { Link, useParams } from 'react-router-dom'
import type { Book as EpubBook, NavItem, Rendition } from 'epubjs'
import { api, type Book } from '../api'
import Icon from '../components/Icon'
import { useDocumentTitle } from '../documentTitle'
import { t } from '../i18n'
import ComicReader from './ComicReader'
import { animateTurn, type Turn, type TurnSpeed } from './turn'

type Theme = 'light' | 'sepia' | 'dark'
type Font = 'serif' | 'sans'
interface Prefs {
  size: number // percent
  theme: Theme
  font: Font
  turn: Turn // how a page turn looks
  speed: TurnSpeed // how long it takes
}


const PREFS_KEY = 'mediarium.reader'
const THEMES: Record<Theme, { bg: string; fg: string; link: string }> = {
  light: { bg: '#fbfaf7', fg: '#1d1c1a', link: '#1f6feb' },
  sepia: { bg: '#f4ecd8', fg: '#3b2f22', link: '#8a4b14' },
  dark: { bg: '#15171b', fg: '#d9dde3', link: '#7fb4ff' },
}
const FONTS: Record<Font, string> = {
  serif: 'Georgia, "Iowan Old Style", "Palatino Linotype", serif',
  sans: 'system-ui, -apple-system, "Segoe UI", Roboto, sans-serif',
}

function loadPrefs(): Prefs {
  try {
    const p = JSON.parse(localStorage.getItem(PREFS_KEY) ?? '{}') as Partial<Prefs>
    return {
      size: Math.min(200, Math.max(70, Number(p.size) || 105)),
      theme: p.theme && p.theme in THEMES ? p.theme : 'light',
      font: p.font === 'sans' ? 'sans' : 'serif',
      turn: p.turn === 'none' || p.turn === 'fade' ? p.turn : 'slide',
      speed: p.speed === 'fast' || p.speed === 'slow' ? p.speed : 'normal',
    }
  } catch {
    return { size: 105, theme: 'light', font: 'serif', turn: 'slide', speed: 'normal' }
  }
}

// The reader: one book, a page at a time. EPUBs are drawn here (epub.js);
// PDFs open in the browser's own viewer; MOBI and AZW3 can't be read in a
// browser, so those can be downloaded instead.
export default function Reader() {
  const { id } = useParams()
  const bookId = Number(id)
  const [book, setBook] = useState<Book | null>(null)
  const [error, setError] = useState('')
  useDocumentTitle(book ? t('{title} · Reading', { title: book.title }) : t('Reading'))

  useEffect(() => {
    api
      .getBook(bookId)
      .then(setBook)
      .catch((e) => setError(e instanceof Error ? e.message : String(e)))
  }, [bookId])

  if (error) return <ReaderMessage title={t("Couldn't open this book")} text={error} />
  if (!book) return <div className="bks-loading">{t('Opening…')}</div>
  if (book.ebook.status !== 'downloaded') return <ReaderMessage title={book.title} text={t('There is no ebook for this book yet. Once Mediarium has downloaded it, it opens here.')} />
  const format = (book.ebook.format ?? '').toLowerCase()
  if (format === 'pdf') return <PdfReader book={book} />
  if (COMIC.has(format)) return <ComicReader book={book} />
  // Kindle books open as an EPUB copy Mediarium makes for the reader.
  if (KINDLE.has(format)) return <EpubReader book={book} src={`${api.bookFileUrl(book.id)}?as=epub`} />
  if (format !== 'epub') {
    return (
      <ReaderMessage
        title={book.title}
        text={
          format
            ? t("This ebook is a {format} file, which browsers can't show. Download it for your e-reader, or look for an EPUB version in Mediarium.", { format: format.toUpperCase() })
            : t("This ebook is in a format browsers can't show. Download it for your e-reader, or look for an EPUB version in Mediarium.")
        }
        action={
          <a className="primary btn-with-icon" href={api.bookFileUrl(book.id, true)}>
            <Icon name="download" size={16} /> {t('Download')}
          </a>
        }
      />
    )
  }
  return <EpubReader book={book} src={api.bookFileUrl(book.id)} />
}

const KINDLE = new Set(['mobi', 'azw', 'azw3', 'prc'])
const COMIC = new Set(['cbz', 'cbr', 'cb7'])

function ReaderMessage({ title, text, action }: { title: string; text: string; action?: React.ReactNode }) {
  return (
    <div className="bks-message">
      <Icon name="book" size={44} />
      <h1>{title}</h1>
      <p>{text}</p>
      <div className="row-actions">
        {action}
        <Link className="btn-with-icon" to="/bookshelf">
          <Icon name="chevron-left" size={16} /> {t('Back to your books')}
        </Link>
      </div>
    </div>
  )
}

function PdfReader({ book }: { book: Book }) {
  useEffect(() => {
    // Opening it is enough to put it under Continue.
    void api.saveBookProgress(book.id, { format: 'ebook', position: 'pdf', percent: 1 })
  }, [book.id])
  return (
    <div className="rdr rdr-pdf">
      <header className="rdr-top">
        <Link className="icon-btn" to="/bookshelf" title={t('Back to your books')} aria-label={t('Back to your books')}>
          <Icon name="chevron-left" size={20} />
        </Link>
        <strong className="rdr-title">{book.title}</strong>
        <a className="icon-btn" href={api.bookFileUrl(book.id, true)} title={t('Download')} aria-label={t('Download')}>
          <Icon name="download" size={18} />
        </a>
      </header>
      <iframe className="rdr-pdf-frame" src={api.bookFileUrl(book.id)} title={book.title} />
    </div>
  )
}

function EpubReader({ book, src }: { book: Book; src: string }) {
  const viewer = useRef<HTMLDivElement>(null)
  const epub = useRef<EpubBook | null>(null)
  const rendition = useRef<Rendition | null>(null)
  const [prefs, setPrefs] = useState<Prefs>(loadPrefs)
  const prefsRef = useRef(prefs)
  prefsRef.current = prefs
  const turning = useRef(false)
  const [toc, setToc] = useState<NavItem[]>([])
  const [panel, setPanel] = useState<'' | 'toc' | 'settings'>('')
  const [percent, setPercent] = useState(0)
  const [located, setLocated] = useState(false) // page percentages are ready
  const [chapter, setChapter] = useState('')
  const [error, setError] = useState('')
  const [ready, setReady] = useState(false)
  const last = useRef<{ cfi: string; percent: number } | null>(null)
  const saveTimer = useRef<number | undefined>(undefined)
  // Nothing is saved until the book has opened where it was left: a book
  // drawn too early (a hidden window) reports the start, which must never
  // replace the real place.
  const armed = useRef(false)

  const save = useCallback(
    (keepalive = false) => {
      const at = last.current
      if (!at || !armed.current) return
      void api.saveBookProgress(book.id, { format: 'ebook', position: at.cfi, percent: at.percent, finished: at.percent >= 99.5 }, keepalive).catch(() => undefined)
    },
    [book.id],
  )

  // Turn a page: the old one slides or fades out, the new one comes in. With
  // "none" (or when the system asks for less motion) it just turns.
  const turn = useCallback(async (dir: 'next' | 'prev') => {
    const r = rendition.current
    if (!r || turning.current) return
    turning.current = true
    try {
      const { turn: effect, speed } = prefsRef.current
      await animateTurn(viewer.current, effect, speed, dir, () => (dir === 'next' ? r.next() : r.prev()))
    } finally {
      turning.current = false
    }
  }, [])
  const turnRef = useRef(turn)
  turnRef.current = turn

  // Open the book once.
  useEffect(() => {
    let cancelled = false
    let cleanup = () => {}
    void (async () => {
      try {
        const [{ default: ePub }, data, saved] = await Promise.all([
          import('epubjs'),
          fetch(src, { credentials: 'include' }).then(async (r) => {
            if (r.status === 422) throw new Error(((await r.json().catch(() => null)) as { error?: string } | null)?.error ?? t("This book couldn't be opened here."))
            if (!r.ok) throw new Error(r.status === 404 ? t("Couldn't find the book's file. It may have been moved or deleted.") : t("The book didn't load (error {status}).", { status: String(r.status) }))
            return r.arrayBuffer()
          }),
          api.bookProgress(book.id, 'ebook').catch(() => null),
        ])
        if (cancelled || !viewer.current) return
        await hasRoom(viewer.current)
        if (cancelled || !viewer.current) return
        const b = ePub(data)
        epub.current = b
        const r = b.renderTo(viewer.current, { width: '100%', height: '100%', flow: 'paginated', spread: 'auto', allowScriptedContent: false })
        rendition.current = r
        for (const [name, t] of Object.entries(THEMES)) {
          r.themes.register(name, { body: { background: `${t.bg} !important`, color: `${t.fg} !important` }, a: { color: `${t.link} !important` } })
        }
        applyPrefs(r, loadPrefs())
        const tocRef = { current: [] as NavItem[] }
        r.on('relocated', (loc: { start: { cfi: string; percentage: number; href: string } }) => {
          const cfi = loc.start.cfi
          const pct = b.locations.length() > 0 ? b.locations.percentageFromCfi(cfi) * 100 : loc.start.percentage * 100
          setPercent(pct)
          last.current = { cfi, percent: Math.round(pct * 10) / 10 }
          const item = findChapter(tocRef.current, loc.start.href)
          if (item) setChapter(item.label.trim())
          window.clearTimeout(saveTimer.current)
          if (armed.current && document.visibilityState === 'visible') saveTimer.current = window.setTimeout(() => save(), 1500)
        })
        // Swipe left or right on a touch screen.
        let startX = 0
        r.on('touchstart', (e: TouchEvent) => {
          startX = e.changedTouches[0]?.screenX ?? 0
        })
        r.on('touchend', (e: TouchEvent) => {
          const dx = (e.changedTouches[0]?.screenX ?? 0) - startX
          if (Math.abs(dx) > 50) void turnRef.current(dx < 0 ? 'next' : 'prev')
        })
        r.on('keyup', onKey)
        b.loaded.navigation.then((nav) => {
          tocRef.current = nav.toc
          setToc(nav.toc)
        })
        const start = saved?.position && saved.position.startsWith('epubcfi') ? saved.position : undefined
        // A place that came from an e-reader (a Kobo) is only a percentage, not an
        // EPUB location: the book opens at the start and jumps there once the page
        // numbers are worked out, unless the reader has already turned a page.
        const jumpTo = !start && saved && saved.percent > 0 && saved.percent < 100 ? saved.percent : 0
        await r.display(start)
        const firstCfi = (r.currentLocation() as unknown as { start?: { cfi: string } } | undefined)?.start?.cfi
        if (cancelled) return
        setReady(true)
        window.setTimeout(() => {
          armed.current = true
        }, 1000)
        // Page percentages: worked out once per book and kept in this browser.
        const key = `mediarium.reader.loc.${book.id}`
        let cached: string | null = null
        try {
          cached = localStorage.getItem(key)
        } catch {
          cached = null
        }
        if (cached) {
          b.locations.load(cached)
        } else {
          await b.locations.generate(1600)
          try {
            localStorage.setItem(key, b.locations.save())
          } catch {
            // a full storage only means working it out again next time
          }
        }
        if (cancelled) return
        setLocated(true)
        if (jumpTo > 0) {
          const now = (r.currentLocation() as unknown as { start?: { cfi: string } } | undefined)?.start?.cfi
          const target = b.locations.cfiFromPercentage(jumpTo / 100)
          if (target && now === firstCfi) {
            // Not saved: it is still the place the e-reader gave, to the page.
            armed.current = false
            await r.display(target)
            window.setTimeout(() => {
              armed.current = true
            }, 1200)
          }
        }
        const cur = r.currentLocation() as unknown as { start?: { cfi: string } }
        if (cur?.start?.cfi) setPercent(b.locations.percentageFromCfi(cur.start.cfi) * 100)
      } catch (e) {
        if (!cancelled) setError(e instanceof Error ? e.message : String(e))
      }
    })()
    function onKey(e: KeyboardEvent) {
      // The arrows move the place slider and Space presses a button: those keys belong to the control.
      if ((e.target as HTMLElement | null)?.closest?.('input, select, textarea, button')) return
      if (e.key === 'ArrowRight' || e.key === 'PageDown' || e.key === ' ') void turnRef.current('next')
      if (e.key === 'ArrowLeft' || e.key === 'PageUp') void turnRef.current('prev')
    }
    document.addEventListener('keyup', onKey)
    const onHide = () => {
      if (document.visibilityState === 'hidden') save(true)
    }
    document.addEventListener('visibilitychange', onHide)
    window.addEventListener('pagehide', onHide)
    cleanup = () => {
      document.removeEventListener('keyup', onKey)
      document.removeEventListener('visibilitychange', onHide)
      window.removeEventListener('pagehide', onHide)
    }
    return () => {
      cancelled = true
      cleanup()
      window.clearTimeout(saveTimer.current)
      save(true)
      epub.current?.destroy()
      epub.current = null
      rendition.current = null
    }
  }, [book.id, save, src])

  useEffect(() => {
    try {
      localStorage.setItem(PREFS_KEY, JSON.stringify(prefs))
    } catch {
      // the reader still works; the choice just isn't remembered
    }
    if (rendition.current) applyPrefs(rendition.current, prefs)
  }, [prefs])

  const theme = THEMES[prefs.theme]
  if (error)
    return (
      <ReaderMessage
        title={book.title}
        text={error}
        action={
          <a className="primary btn-with-icon" href={api.bookFileUrl(book.id, true)}>
            <Icon name="download" size={16} /> {t('Download')}
          </a>
        }
      />
    )
  return (
    <div className={`rdr theme-${prefs.theme}`} style={{ ['--rdr-bg' as string]: theme.bg, ['--rdr-fg' as string]: theme.fg }}>
      <header className="rdr-top">
        <Link className="icon-btn" to="/bookshelf" title={t('Back to your books')} aria-label={t('Back to your books')}>
          <Icon name="chevron-left" size={20} />
        </Link>
        <span className="rdr-title">
          <strong>{book.title}</strong>
          {chapter && <small>{chapter}</small>}
        </span>
        <button className={`icon-btn${panel === 'toc' ? ' active' : ''}`} onClick={() => setPanel(panel === 'toc' ? '' : 'toc')} title={t('Contents')} aria-label={t('Contents')}>
          <Icon name="list" size={18} />
        </button>
        <button className={`icon-btn${panel === 'settings' ? ' active' : ''}`} onClick={() => setPanel(panel === 'settings' ? '' : 'settings')} title={t('Text and colours')} aria-label={t('Text and colours')}>
          <span className="rdr-aa">Aa</span>
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

      {panel === 'toc' && (
        <nav className="rdr-panel rdr-toc" aria-label={t('Contents')}>
          {toc.length === 0 && <p className="hint">{t('This book has no table of contents.')}</p>}
          <TocList
            items={toc}
            onPick={(href) => {
              void rendition.current?.display(href)
              setPanel('')
            }}
          />
        </nav>
      )}
      {panel === 'settings' && (
        <div className="rdr-panel rdr-settings">
          <div className="rdr-row">
            <span>{t('Text size')}</span>
            <button className="btn-sm" onClick={() => setPrefs((p) => ({ ...p, size: Math.max(70, p.size - 10) }))} aria-label={t('Smaller text')}>
              A−
            </button>
            <span className="rdr-size">{prefs.size}%</span>
            <button className="btn-sm" onClick={() => setPrefs((p) => ({ ...p, size: Math.min(200, p.size + 10) }))} aria-label={t('Bigger text')}>
              A+
            </button>
          </div>
          <div className="rdr-row">
            <span>{t('Page')}</span>
            {(Object.keys(THEMES) as Theme[]).map((th) => (
              <button key={th} className={`rdr-swatch${prefs.theme === th ? ' active' : ''}`} style={{ background: THEMES[th].bg, color: THEMES[th].fg }} onClick={() => setPrefs((p) => ({ ...p, theme: th }))}>
                {th === 'light' ? t('Light') : th === 'sepia' ? t('Sepia') : t('Dark')}
              </button>
            ))}
          </div>
          <div className="rdr-row">
            <span>{t('Page turn')}</span>
            {(['slide', 'fade', 'none'] as Turn[]).map((x) => (
              <button key={x} className={`btn-sm${prefs.turn === x ? ' primary' : ''}`} onClick={() => setPrefs((p) => ({ ...p, turn: x }))}>
                {x === 'slide' ? t('Slide') : x === 'fade' ? t('Fade') : t('None')}
              </button>
            ))}
          </div>
          {prefs.turn !== 'none' && (
            <div className="rdr-row">
              <span>{t('Turn speed')}</span>
              {(['fast', 'normal', 'slow'] as TurnSpeed[]).map((x) => (
                <button key={x} className={`btn-sm${prefs.speed === x ? ' primary' : ''}`} onClick={() => setPrefs((p) => ({ ...p, speed: x }))}>
                  {x === 'fast' ? t('Fast') : x === 'normal' ? t('Normal') : t('Slow')}
                </button>
              ))}
            </div>
          )}
          <div className="rdr-row">
            <span>{t('Font')}</span>
            {(['serif', 'sans'] as Font[]).map((f) => (
              <button key={f} className={`btn-sm${prefs.font === f ? ' primary' : ''}`} style={{ fontFamily: FONTS[f] }} onClick={() => setPrefs((p) => ({ ...p, font: f }))}>
                {f === 'serif' ? t('Book') : t('Plain')}
              </button>
            ))}
          </div>
        </div>
      )}

      <main className="rdr-page">
        <button className="rdr-turn prev" onClick={() => void turn('prev')} aria-label={t('Previous page')}>
          <Icon name="chevron-left" size={26} />
        </button>
        <div className="rdr-viewer" ref={viewer} />
        <button className="rdr-turn next" onClick={() => void turn('next')} aria-label={t('Next page')}>
          <Icon name="chevron-right" size={26} />
        </button>
        {!ready && <div className="rdr-opening">{t('Opening the book…')}</div>}
      </main>

      <footer className="rdr-foot">
        <input
          type="range"
          min={0}
          max={100}
          step={0.1}
          value={percent}
          disabled={!located}
          title={located ? t('Go to a place in the book') : t('Working out the pages…')}
          aria-label={t('Place in the book')}
          onChange={(e) => setPercent(Number(e.target.value))}
          onMouseUp={(e) => jump(Number((e.target as HTMLInputElement).value))}
          onTouchEnd={(e) => jump(Number((e.target as HTMLInputElement).value))}
          onKeyUp={(e) => jump(Number((e.target as HTMLInputElement).value))}
        />
        <span className="rdr-pct">{located ? `${Math.round(percent)}%` : '…'}</span>
      </footer>
    </div>
  )

  function jump(p: number) {
    const b = epub.current
    if (!b || b.locations.length() === 0) return
    void rendition.current?.display(b.locations.cfiFromPercentage(p / 100))
  }
}

// hasRoom waits until el has a size: a book laid out in a hidden window has
// no pages to turn.
function hasRoom(el: HTMLElement): Promise<void> {
  if (el.offsetWidth > 0 && el.offsetHeight > 0) return Promise.resolve()
  return new Promise((resolve) => {
    const ro = new ResizeObserver(() => {
      if (el.offsetWidth > 0 && el.offsetHeight > 0) {
        ro.disconnect()
        resolve()
      }
    })
    ro.observe(el)
  })
}

function applyPrefs(r: Rendition, p: Prefs) {
  r.themes.select(p.theme)
  r.themes.fontSize(`${p.size}%`)
  r.themes.font(FONTS[p.font])
}

function findChapter(items: NavItem[], href: string): NavItem | undefined {
  const base = href.split('#')[0]
  for (const it of items) {
    if (it.href.split('#')[0] === base) return it
    const sub = findChapter(it.subitems ?? [], href)
    if (sub) return sub
  }
  return undefined
}

function TocList({ items, onPick }: { items: NavItem[]; onPick: (href: string) => void }) {
  return (
    <ul>
      {items.map((it) => (
        <li key={it.id || it.href}>
          <button onClick={() => onPick(it.href)}>{it.label.trim()}</button>
          {it.subitems && it.subitems.length > 0 && <TocList items={it.subitems} onPick={onPick} />}
        </li>
      ))}
    </ul>
  )
}
