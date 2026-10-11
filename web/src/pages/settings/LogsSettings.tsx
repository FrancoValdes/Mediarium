import { useCallback, useEffect, useRef, useState } from 'react'
import { Link } from 'react-router-dom'
import { api } from '../../api'
import { useConfirm } from '../../components/ConfirmProvider'
import { copyText } from '../../components/CopyBox'
import Icon from '../../components/Icon'
import Switch from '../../components/Switch'
import { useToast } from '../../components/Toast'
import {
  afterMarkedRead,
  agoText,
  appendPage,
  countText,
  emptyText,
  exactTime,
  filtersActive,
  happenedText,
  moreText,
  NO_FILTERS,
  PAGE_SIZE,
  pageRequests,
  pageSignature,
  problemQuery,
  problemText,
  RANGE_LABELS,
  showingText,
  type ProblemFilters,
  type ProblemList,
  type ProblemRow,
  type RangePreset,
} from '../../problemsView'
import { formatSupportReport } from '../../supportReport'
import { problemsChanged } from '../../useProblems'
import { useLive } from '../../useLive'
import { plural, t, tx } from '../../i18n'
import { withBase } from '../../base'

// Settings > System > Logs and errors: the things that went wrong (a download
// that failed, a provider that refused a login, a full disk), each with what it
// means and what to try. The server sends the list a page at a time; "Show
// more" adds the next page, and the page keeps itself up to date.
export default function LogsSettings() {
  const toast = useToast()
  const confirm = useConfirm()
  // The filters, and the moment they were set: "Last 24 hours" counts back from
  // then and stays put while the filters do, so every page and the file are cut
  // at the same moment.
  const [view, setView] = useState(() => ({ filters: NO_FILTERS, asOf: new Date() }))
  const { filters, asOf } = view
  const setFilters = useCallback(
    (change: (f: ProblemFilters) => ProblemFilters) =>
      setView((v) => {
        const next = change(v.filters)
        return next === v.filters ? v : { filters: next, asOf: new Date() }
      }),
    [],
  )
  const [typed, setTyped] = useState('')
  const [data, setData] = useState<ProblemList | null>(null) // the cards, areas and settings; its own items are the first page
  const [rows, setRows] = useState<ProblemRow[]>([]) // every row loaded so far
  const [total, setTotal] = useState(0) // how many match the filters, in all
  const [error, setError] = useState('')
  const [open, setOpen] = useState<Set<number>>(new Set())
  const [copying, setCopying] = useState(false)
  const [loadingMore, setLoadingMore] = useState(false)
  const [marking, setMarking] = useState(false)
  const epoch = useRef(0) // goes up when the filters change, so answers to an older question are dropped
  const loaded = useRef({ rows, total, more: false }) // what the page holds, for the code that runs later
  useEffect(() => {
    loaded.current = { rows, total, more: loadingMore }
  })

  // Wait for a pause in typing before asking again.
  useEffect(() => {
    const timer = setTimeout(() => setFilters((f) => (f.text === typed ? f : { ...f, text: typed })), 300)
    return () => clearTimeout(timer)
  }, [typed, setFilters])

  const pageQuery = useCallback((offset: number, limit: number) => problemQuery(filters, { limit, offset }, asOf), [filters, asOf])

  // Looks at the first page. With force, or when something on it changed, the
  // rows shown are fetched again, as many as were showing, so the list keeps
  // its place.
  const refresh = useCallback(
    async (force: boolean) => {
      const mine = epoch.current
      const was = loaded.current.rows.length
      try {
        const d = await api.problems(pageQuery(0, PAGE_SIZE))
        if (mine !== epoch.current) return // the filters changed meanwhile
        setData(d)
        setError('')
        const cur = loaded.current
        if (!force && pageSignature(cur.rows.slice(0, PAGE_SIZE), cur.total) === pageSignature(d.items, d.total)) return
        let next = d.items
        if (!force && was > PAGE_SIZE && d.total > PAGE_SIZE) {
          next = []
          for (const r of pageRequests(Math.min(was, d.total))) next = next.concat((await api.problems(pageQuery(r.offset, r.limit))).items)
          if (mine !== epoch.current) return
        }
        // Someone pressed "Show more" while this ran: leave it to the next look.
        if (!force && (loaded.current.more || loaded.current.rows.length !== was)) return
        setRows(next)
        setTotal(d.total)
      } catch (e) {
        if (mine === epoch.current) setError(e instanceof Error ? e.message : String(e))
      }
    },
    [pageQuery],
  )
  useEffect(() => {
    epoch.current++
    void refresh(true)
  }, [refresh])
  useLive(() => refresh(false), 5000)

  async function showMore() {
    const mine = epoch.current
    setLoadingMore(true)
    try {
      const d = await api.problems(pageQuery(rows.length, PAGE_SIZE))
      if (mine !== epoch.current) return
      setRows((r) => appendPage(r, d.items))
      setTotal(d.total)
      setData((cur) => (cur ? { ...cur, counts: d.counts } : cur))
    } catch (e) {
      toast.error(e instanceof Error ? e.message : String(e))
    } finally {
      setLoadingMore(false)
    }
  }

  const change = (patch: Partial<ProblemFilters>) => setFilters((f) => ({ ...f, ...patch }))
  const clearFilters = () => {
    setFilters(() => NO_FILTERS)
    setTyped('')
  }

  const toggle = (id: number) =>
    setOpen((s) => {
      const next = new Set(s)
      if (!next.delete(id)) next.add(id)
      return next
    })

  // Marks problems as read and updates the rows on screen where they are, so
  // the list does not jump.
  async function markRead(req: { ids?: number[]; all?: boolean }) {
    try {
      const res = await api.markProblemsRead(req, req.all ? problemQuery(filters, {}, asOf) : '')
      const after = afterMarkedRead(loaded.current.rows, loaded.current.total, req, filters.unreadOnly)
      setRows(after.rows)
      setTotal(after.total)
      setData((d) => (d ? { ...d, counts: res.counts } : d))
      problemsChanged()
    } catch (e) {
      toast.error(e instanceof Error ? e.message : String(e))
    }
  }

  // Everything that matches the filters, not only the rows loaded: say how many first.
  async function markAllRead() {
    setMarking(true)
    try {
      const n = (await api.problems(problemQuery({ ...filters, unreadOnly: true }, { limit: 1 }, asOf))).total
      if (n === 0) {
        toast.info(filtersActive(filters) ? t('Nothing unread matches these filters.') : t('Everything is read already.'))
        return
      }
      const ok = await confirm({
        title: plural(n, 'Mark {n} problem as read?', 'Mark {n} problems as read?'),
        body: <p>{filtersActive(filters) ? t('This is every unread problem that matches your filters, not only the ones on screen.') : t('This is every unread problem, not only the ones on screen.')}</p>,
        confirmLabel: t('Mark as read'),
      })
      if (!ok) return
      await markRead({ all: true })
    } catch (e) {
      toast.error(e instanceof Error ? e.message : String(e))
    } finally {
      setMarking(false)
    }
  }

  async function copyForSupport() {
    setCopying(true)
    try {
      const ok = await copyText(formatSupportReport(await api.diagnostics()))
      if (ok) toast.success(t('Copied. Paste it into your support request.'))
      else toast.error(t('Your browser blocked copying. Use Download log file instead.'))
    } catch (e) {
      toast.error(e instanceof Error ? e.message : String(e))
    } finally {
      setCopying(false)
    }
  }

  async function setNotify(on: boolean) {
    try {
      await api.setNotifyOnProblems(on)
      setData((d) => (d ? { ...d, notifyOnProblems: on } : d))
      toast.success(on ? t('Error messages are on.') : t('Error messages are off.'))
    } catch (e) {
      toast.error(e instanceof Error ? e.message : String(e))
    }
  }

  const filtered = filtersActive(filters)
  const counts = data?.counts

  return (
    <div className="pl-page">
      <p className="pl-intro">
        {t('Things that went wrong, with what to try. The same problem, and the same problem for one title, count on one line, and problems are kept for {n} days.', { n: data?.keepDays ?? 30 })}
      </p>

      <div className="pl-cards">
        <StatCard tone="bad" icon="warning" label={t('Errors today')} value={counts?.errorsToday} word="error" />
        <StatCard tone="warn" icon="info" label={t('Warnings today')} value={counts?.warningsToday} word="warning" />
        <StatCard tone="bad" icon="calendar" label={t('Errors this week')} value={counts?.errorsWeek} word="error" />
        <StatCard tone="warn" icon="calendar" label={t('Warnings this week')} value={counts?.warningsWeek} word="warning" />
      </div>

      <div className="pl-toolbar">
        <div className="pl-filters">
          <div className="seg" role="group" aria-label={t('Show')}>
            {(
              [
                ['all', t('All')],
                ['error', t('Errors')],
                ['warning', t('Warnings')],
              ] as const
            ).map(([value, label]) => (
              <button key={value} type="button" className={filters.level === value ? 'active' : ''} aria-pressed={filters.level === value} onClick={() => change({ level: value })}>
                {label}
              </button>
            ))}
          </div>
          <select aria-label={t('Area')} value={filters.area} onChange={(e) => change({ area: e.target.value })}>
            <option value="">{t('All areas')}</option>
            {(data?.areas ?? []).map((a) => (
              <option key={a.id} value={a.id}>
                {a.label}
              </option>
            ))}
          </select>
          <select aria-label={t('When')} value={filters.range} onChange={(e) => change({ range: e.target.value as RangePreset })}>
            {(Object.keys(RANGE_LABELS) as RangePreset[]).map((r) => (
              <option key={r} value={r}>
                {RANGE_LABELS[r]}
              </option>
            ))}
          </select>
          {filters.range === 'custom' && (
            <>
              <label className="pl-date">
                <span>{t('From')}</span>
                <input type="date" value={filters.from} max={filters.to || undefined} onChange={(e) => change({ from: e.target.value })} />
              </label>
              <label className="pl-date">
                <span>{t('To')}</span>
                <input type="date" value={filters.to} min={filters.from || undefined} onChange={(e) => change({ to: e.target.value })} />
              </label>
            </>
          )}
          <input className="pl-search" type="search" placeholder={t('Search the log')} aria-label={t('Search the log')} value={typed} onChange={(e) => setTyped(e.target.value)} />
          <label className="pl-unread">
            <input type="checkbox" checked={filters.unreadOnly} onChange={(e) => change({ unreadOnly: e.target.checked })} /> {t('Only unread')}
          </label>
          {filtered && (
            <button type="button" className="pl-clear" onClick={clearFilters}>
              <Icon name="x" size={13} /> {t('Clear filters')}
            </button>
          )}
        </div>
      </div>

      <div className="pl-bar">
        <span className="pl-bar-text">
          {data && <span>{showingText(rows.length, total)}</span>}
          {counts && counts.unread > 0 && <span className="pl-unread-count">{t('{n} unread', { n: counts.unread })}</span>}
        </span>
        <div className="pl-actions">
          <button type="button" className="btn-with-icon" onClick={() => void copyForSupport()} disabled={copying}>
            <Icon name="chat" size={15} /> {copying ? t('Copying…') : t('Copy for support')}
          </button>
          <a className="btn-link" href={withBase(`/api/system/problems/export${problemQuery(filters, {}, asOf)}`)} download>
            <Icon name="download" size={15} /> {t('Download log file')}
          </a>
          <button type="button" className="btn-with-icon" onClick={() => void markAllRead()} disabled={!counts || counts.unread === 0 || marking}>
            <Icon name="check" size={15} /> {t('Mark all as read')}
          </button>
        </div>
      </div>

      {error && <p className="error-text">{error}</p>}

      {!data && !error && (
        <div className="pl-list" aria-busy="true">
          {[0, 1, 2].map((i) => (
            <div key={i} className="skeleton" style={{ height: 64, borderRadius: 14 }} />
          ))}
        </div>
      )}

      {data && rows.length === 0 && (
        <div className="pl-empty">
          <Icon name={filtered ? 'search' : 'check'} size={34} />
          <strong>{emptyText(filtered, data.keepDays)}</strong>
          <span>
            {filtered
              ? t('Try clearing a filter.')
              : t('When something goes wrong, like a failed download or a refused login, it shows up here with what to try.')}
          </span>
        </div>
      )}

      {data && rows.length > 0 && (
        <>
          <ul className="pl-list">
            {rows.map((p) => (
              <ProblemItem key={p.id} p={p} open={open.has(p.id)} onToggle={() => toggle(p.id)} onRead={() => void markRead({ ids: [p.id] })} />
            ))}
          </ul>
          {rows.length < total && (
            <div className="pl-more">
              <span className="pl-more-count">{showingText(rows.length, total)}</span>
              <button type="button" onClick={() => void showMore()} disabled={loadingMore}>
                {loadingMore ? t('Loading…') : moreText(rows.length, total)}
              </button>
            </div>
          )}
        </>
      )}

      <fieldset className="group alerts pl-notify">
        <legend>
          <Icon name="mail" size={14} /> {t('Messages about errors')}
        </legend>
        <Switch
          checked={data?.notifyOnProblems ?? false}
          disabled={!data}
          onChange={(on) => void setNotify(on)}
          label={t('Tell me when an error happens')}
          showState
          description={
            <>
              {tx('Goes to the places under {notifications} that have "Something is wrong" ticked. Errors only, and a repeat is sent once.', {
                notifications: <Link to="/settings/notifications">{t('Notifications')}</Link>,
              })}
            </>
          }
        />
      </fieldset>
    </div>
  )
}

function StatCard({ tone, icon, label, value, word }: { tone: 'bad' | 'warn'; icon: 'warning' | 'info' | 'calendar'; label: string; value?: number; word: 'error' | 'warning' }) {
  return (
    <div className={`pl-card ${tone}${value ? ' has' : ''}`}>
      <span className="pl-card-icon">
        <Icon name={icon} size={18} />
      </span>
      <span className="pl-card-label">{label}</span>
      <span className="pl-card-value">{value ?? '…'}</span>
      <span className="pl-card-sub">{value === undefined ? '' : countText(value, word)}</span>
    </div>
  )
}

function ProblemItem({ p, open, onToggle, onRead }: { p: ProblemRow; open: boolean; onToggle: () => void; onRead: () => void }) {
  const toast = useToast()
  const bodyId = `pl-body-${p.id}`
  const other = p.message && p.message !== p.title

  async function copy(text: string) {
    if (await copyText(text)) toast.success(t('Copied.'))
    else toast.error(t('Your browser blocked copying. Select the text and copy it yourself.'))
  }

  return (
    <li className={`pl-row ${p.level}${p.read ? ' read' : ' unread'}${open ? ' open' : ''}`}>
      <button type="button" className="pl-head" aria-expanded={open} aria-controls={bodyId} onClick={onToggle}>
        <span className="pl-head-top">
          <span className={`pl-level ${p.level}`}>
            <Icon name={p.level === 'error' ? 'warning' : 'info'} size={13} /> {p.level === 'error' ? t('Error') : t('Warning')}
          </span>
          {!p.read && <span className="pl-new">{t('New')}</span>}
          <Icon name="chevron-down" size={16} className="pl-chev" />
        </span>
        <span className="pl-title">{p.title}</span>
      </button>

      {(other || p.forTitle || p.downloadId) && (
        <div className="pl-sub">
          {other && <span>{p.message}</span>}
          {p.forTitle &&
            (p.forLink ? (
              <span>{tx('For {title}', { title: <Link to={p.forLink}>{p.forTitle}</Link> })}</span>
            ) : (
              <span>{t('For {title}', { title: p.forTitle })}</span>
            ))}
          {p.downloadId ? (
            <span>
              <Link to="/queue">{t('Download {id}', { id: String(p.downloadId) })}</Link>
            </span>
          ) : null}
        </div>
      )}

      <div className="pl-foot">
        <span className="pl-meta">
          <span className="pl-area">{p.areaLabel}</span>
          {p.count > 1 && <span className="pl-times">{t('{n} times', { n: p.count })}</span>}
          <span className="pl-when" title={exactTime(p.lastAt)}>
            {agoText(p.lastAt)}
          </span>
        </span>
        {!p.read && (
          <button type="button" className="pl-read" onClick={onRead}>
            <Icon name="check" size={13} /> {t('Mark as read')}
          </button>
        )}
      </div>

      {open && (
        <div className="pl-body" id={bodyId}>
          <section>
            <h4>{t('What happened')}</h4>
            <p>{p.explain || p.message}</p>
            <p className="pl-dim">{happenedText(p)}</p>
            <p className="pl-dim">
              {t('First seen {when}.', { when: exactTime(p.firstAt) })} <code>{p.code}</code>
            </p>
          </section>
          <section>
            <h4>{t('What to try')}</h4>
            <p>{p.try || t('Nothing specific to try. If it keeps happening, send the technical detail to support.')}</p>
            {p.linkPath && (
              <Link className="btn-link primary-look" to={p.linkPath}>
                {p.linkLabel || t('Open settings')} <Icon name="open" size={14} />
              </Link>
            )}
          </section>
          <section>
            <div className="pl-detail-head">
              <h4>{t('Technical detail')}</h4>
              <button type="button" className="pl-copy" onClick={() => void copy(problemText(p))}>
                <Icon name="list" size={13} /> {t('Copy')}
              </button>
            </div>
            {p.detail ? <pre className="pl-detail">{p.detail}</pre> : <p className="pl-dim">{t('No more detail for this one.')}</p>}
          </section>
        </div>
      )}
    </li>
  )
}
