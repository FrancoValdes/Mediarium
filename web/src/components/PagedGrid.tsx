import { useEffect, useMemo, useRef, useState } from 'react'
import { useUrlState } from '../useUrlState'
import type { DiscoverMovie } from '../api'
import { fetchPage, sourceToQuery, type Kind, type Source } from '../discoverSources'
import { useGridColumns } from '../useGridColumns'
import { useHideOwned } from '../useHideOwned'
import type { Owned } from '../useOwned'
import type { AddTarget } from './AddDialog'
import { DiscoverCards } from './DiscoverRail'
import Icon from './Icon'
import { scrollToTop } from '../scrollTop'
import { t } from '../i18n'

const PER_FETCH = 20 // TMDB returns 20 titles per page
const MAX_ITEMS = 500 * PER_FETCH // TMDB serves at most 500 pages

interface Props {
  source: Source
  kind: Kind
  owned: Owned
  onAdd: (t: AddTarget) => void
  rows?: number
  title?: string
  hint?: string
  // Shown next to "Nothing matches these filters" so the filters can be put back.
  onClear?: () => void
}

// A Discover list split into pages of whole rows: Previous, page numbers and
// Next move between pages, and every move goes back to the top. Only the
// pages needed for what is on screen are fetched. No total is shown: the lists are
// huge and a number like "10,000" says nothing useful.
export default function PagedGrid({ source, kind, owned, onAdd, rows = 3, title, hint, onClear }: Props) {
  const grid = useRef<HTMLDivElement>(null)
  const cols = useGridColumns(grid)
  const size = cols * rows
  const [hideOwned] = useHideOwned()
  const [fetched, setFetched] = useState<Map<number, DiscoverMovie[]>>(new Map())
  const [total, setTotal] = useState<number | undefined>()
  // Where you are, in pages of `size` titles, so a change in the number of
  // columns (window resize, sidebar) keeps you on the same page.
  // The page lives in the address (with the list it belongs to), so Back from a title returns to the same page.
  const [ui, patchUi] = useUrlState({ pg: '1', pgs: '' })
  const listKey = sourceToQuery(source)
  const current = ui.pgs === listKey ? Math.max(1, Number(ui.pg) || 1) : 1
  const setCurrent = (n: number) => patchUi({ pg: String(n), pgs: n > 1 ? listKey : '' })
  const [error, setError] = useState('')
  const [attempt, setAttempt] = useState(0) // raised by Try again, to ask for the failed pages once more
  const inflight = useRef(new Set<number>())

  const start = (current - 1) * size
  const end = Math.min(start + size, total ?? MAX_ITEMS)
  const needed = useMemo(() => {
    const first = Math.floor(start / PER_FETCH) + 1
    const last = Math.max(first, Math.ceil(end / PER_FETCH))
    return Array.from({ length: last - first + 1 }, (_, i) => first + i)
  }, [start, end])

  useEffect(() => {
    const missing = needed.filter((p) => !fetched.has(p) && !inflight.current.has(p))
    if (missing.length === 0) return
    missing.forEach((p) => inflight.current.add(p))
    Promise.all(missing.map((p) => fetchPage(source, p).then((r) => [p, r] as const)))
      .then((res) => {
        setError('')
        setFetched((m) => {
          const next = new Map(m)
          for (const [p, r] of res) next.set(p, r.results)
          return next
        })
        for (const [p, r] of res) {
          if (r.results.length < PER_FETCH) setTotal((p - 1) * PER_FETCH + r.results.length)
          else setTotal((t) => t ?? Math.min(r.totalResults ?? r.totalPages * PER_FETCH, MAX_ITEMS))
        }
      })
      .catch((e) => setError(e instanceof Error ? e.message : String(e)))
      .finally(() => missing.forEach((p) => inflight.current.delete(p)))
  }, [needed, fetched, source, attempt])

  // What is on screen, without repeats (TMDB lists shift a little between pages).
  const { items, waiting } = useMemo(() => {
    const out: DiscoverMovie[] = []
    const seen = new Set<number>()
    let waiting = 0
    for (let i = start; i < end; i++) {
      const page = fetched.get(Math.floor(i / PER_FETCH) + 1)
      if (!page) {
        waiting++
        continue
      }
      const m = page[i % PER_FETCH]
      if (m && !seen.has(m.tmdbId)) {
        seen.add(m.tmdbId)
        out.push(m)
      }
    }
    return { items: out, waiting }
  }, [fetched, start, end])

  const pageCount = total !== undefined ? Math.max(1, Math.ceil(total / size)) : undefined
  const hasMore = pageCount === undefined || current < pageCount

  function goTo(page: number) {
    setCurrent(page)
    scrollToTop()
  }

  // A few page numbers around where you are; never the last one (that would
  // just be a total in disguise).
  const numbers: (number | '…')[] = []
  const from = Math.max(1, current - 2)
  const to = Math.min(pageCount ?? current + 2, current + 2)
  if (from > 1) numbers.push(1, ...(from > 2 ? (['…'] as const) : []))
  for (let n = from; n <= to; n++) numbers.push(n)
  if (hasMore && to < (pageCount ?? Infinity)) numbers.push('…')

  if (total === 0) {
    return (
      <section className="discover-rail">
        {title && (
          <div className="rail-head">
            <h2>{title}</h2>
          </div>
        )}
        <div className="empty-state">
          <Icon name="compass" size={36} />
          <p>{t('Nothing matches these filters.')}</p>
          {onClear && (
            <button className="btn-with-icon" onClick={onClear}>
              {t('Clear filters')}
            </button>
          )}
        </div>
      </section>
    )
  }

  return (
    <section className="discover-rail paged-grid">
      {title && (
        <div className="rail-head">
          <h2>{title}</h2>
          {hint && <span>{hint}</span>}
          {current > 1 && <span className="page-at">{t('Page {page}', { page: current })}</span>}
        </div>
      )}
      {error && (
        <p className="error-text">
          {error}{' '}
          <button
            className="btn-sm"
            onClick={() => {
              setError('')
              setAttempt((n) => n + 1)
            }}
          >
            {t('Try again')}
          </button>
        </p>
      )}
      <div className="poster-grid" ref={grid}>
        <DiscoverCards items={hideOwned ? items.filter((m) => !(kind === 'movie' ? owned.movies : owned.shows).has(m.tmdbId)) : items} kind={kind} owned={owned} onAdd={onAdd} />
        {!error && Array.from({ length: waiting }, (_, i) => (
          <div key={`s${i}`} className="skeleton" style={{ aspectRatio: '2 / 3.5' }} />
        ))}
      </div>

      <nav className="pager" aria-label={t('Pages')}>
        <button className="btn-sm btn-with-icon" onClick={() => goTo(current - 1)} disabled={current <= 1}>
          <Icon name="chevron-left" size={15} /> {t('Previous')}
        </button>
        <div className="pager-numbers">
          {numbers.map((n, i) =>
            n === '…' ? (
              <span key={`e${i}`} className="pager-gap">
                …
              </span>
            ) : (
              <button key={n} className={`btn-sm${n === current ? ' active' : ''}`} onClick={() => goTo(n)} aria-current={n === current ? 'page' : undefined}>
                {n}
              </button>
            ),
          )}
        </div>
        <button className="btn-sm btn-with-icon" onClick={() => goTo(current + 1)} disabled={!hasMore}>
          {t('Next')} <Icon name="chevron-right" size={15} />
        </button>
      </nav>
    </section>
  )
}
