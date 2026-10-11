import { useCallback, useEffect, useRef, useState } from 'react'
import { Link, useSearchParams } from 'react-router-dom'
import {
  api,
  type ImportActive,
  type ImportBatchDetail,
  type ImportBatchItem,
  type ImportCandidate,
  type ImportItem,
  type ImportJob,
} from '../api'
import Switch from '../components/Switch'
import { useToast } from '../components/Toast'
import { formatBytes } from '../format'
import { afterText, etaText, progressText, setText, totalLine } from '../importText'
import { plural, t } from '../i18n'
import KindGlyph from '../components/KindGlyph'
import '../import.css'
import { useLive } from '../useLive'
import { firstError, folderPath, required } from '../validate'
import { FieldError, useValidation } from '../useValidation'

type Kind = 'movie' | 'tv'

const LAST_TAB = 'mediarium-library-tab'

function startingKind(asked: string | null): Kind {
  if (asked === 'tv' || asked === 'movie') return asked
  try {
    return localStorage.getItem(LAST_TAB) === 'tv' ? 'tv' : 'movie'
  } catch {
    return 'movie'
  }
}

// Import an existing library: point at a folder that already holds movies or
// TV, review what Mediarium recognised (and fix any wrong match), then
// confirm. Confirming only writes the titles down, at once; the details
// (posters, summaries, episode lists) are filled in by the server in the
// background, so leaving this page is safe and coming back picks up where
// the import is. Files are left exactly where they are.
// The review list can show just one kind of row, so a second pass over a big
// library goes straight to what still needs a hand.
const REVIEW_FILTERS: { id: string; label: string; test: (i: ImportItem) => boolean }[] = [
  { id: 'all', label: t('All'), test: () => true },
  { id: 'unmatched', label: t('Unmatched'), test: (i) => !i.inLibrary && i.match === 'unmatched' },
  { id: 'check', label: t('Check match'), test: (i) => !i.inLibrary && i.match === 'ambiguous' },
  { id: 'matched', label: t('Matched'), test: (i) => !i.inLibrary && i.match === 'matched' },
  { id: 'library', label: t('Already in library'), test: (i) => i.inLibrary },
]

export default function ImportLibrary() {
  const [params, setParams] = useSearchParams()
  const [kind, setKind] = useState<Kind>(() => startingKind(params.get('kind')))
  const [path, setPath] = useState('')
  const [defaults, setDefaults] = useState<{ movie: string; tv: string }>({ movie: '', tv: '' })
  const [job, setJob] = useState<ImportJob | null>(null)
  const [batchId, setBatchId] = useState<number | null>(() => {
    const n = Number(params.get('batch'))
    return Number.isInteger(n) && n > 0 ? n : null
  })
  const [batch, setBatch] = useState<ImportBatchDetail | null>(null)
  const [active, setActive] = useState<ImportActive | null>(null)
  const [error, setError] = useState('')
  const [starting, setStarting] = useState(false)
  const [confirming, setConfirming] = useState(false)

  // The choices for this import. Both start off, so an import never
  // downloads anything by itself.
  const [monitor, setMonitor] = useState(false)
  const [monitorMissing, setMonitorMissing] = useState(false)

  // Per-row review state, keyed by item key.
  const [checked, setChecked] = useState<Record<string, boolean>>({})
  const [reviewFilter, setReviewFilter] = useState('all')
  const [chosen, setChosen] = useState<Record<string, number>>({})
  const [candidates, setCandidates] = useState<Record<string, ImportCandidate[]>>({})

  const example = kind === 'movie' ? '/movies' : '/tv'
  const v = useValidation({
    path: firstError(required(path, t('Add the folder to scan, for example {example}.', { example })), folderPath(path, example)),
  })

  const defaultsRef = useRef(defaults)
  defaultsRef.current = defaults
  useEffect(() => {
    api
      .getSettings()
      .then((s) => {
        const d = { movie: s.moviesPath, tv: s.tvPath ?? '' }
        setDefaults(d)
        setPath((p) => p || d[kind])
      })
      .catch(() => undefined)
    // Only the first load: later changes of kind set the folder themselves.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  function chooseKind(next: Kind) {
    setKind(next)
    setPath(defaults[next])
    setParams({ kind: next }, { replace: true })
  }

  // Find what is going on at the server: a scan waiting to be reviewed, or an
  // import that is still running or has finished. This is what lets the
  // person leave and come back.
  const attached = useRef(false)
  const refreshActive = useCallback(() => {
    api
      .importActive()
      .then(setActive)
      .catch(() => undefined)
  }, [])
  useEffect(() => refreshActive(), [refreshActive])
  useLive(refreshActive, 3000)
  useEffect(() => {
    if (!active || attached.current) return
    attached.current = true
    if (batchId !== null) return // the link asked for a particular import
    // A scan of this kind that is running or waiting to be reviewed, else an
    // import of this kind that is still filling in details. A finished import
    // is opened from the banner's link, not every time this page opens.
    const scan = active.jobs.find((j) => j.kind === kind)
    if (scan) {
      setPath(scan.root)
      api
        .getScan(scan.id)
        .then(setJob)
        .catch(() => undefined)
      return
    }
    const running = active.batches.find((x) => x.kind === kind && x.running)
    if (running) setBatchId(running.id)
  }, [active, batchId, kind])

  const jobId = job?.id
  const phase = job?.phase
  const reviewInit = useRef<string | null>(null)

  // Poll while the server is scanning and matching.
  useEffect(() => {
    if (!jobId || !(phase === 'scanning' || phase === 'matching')) return
    const timer = setInterval(() => {
      api
        .getScan(jobId)
        .then(setJob)
        .catch((e) => setError(e instanceof Error ? e.message : String(e)))
    }, 700)
    return () => clearInterval(timer)
  }, [jobId, phase])

  // When a scan first becomes ready, pre-select the confident matches.
  useEffect(() => {
    if (!job || job.phase !== 'ready' || reviewInit.current === job.id) return
    reviewInit.current = job.id
    const c: Record<string, boolean> = {}
    const pick: Record<string, number> = {}
    for (const item of job.items) {
      c[item.key] = item.match === 'matched' && !item.inLibrary
      if (item.candidates.length > 0) pick[item.key] = item.candidates[0].tmdbId
    }
    setChecked(c)
    setChosen(pick)
    setCandidates({})
  }, [job])

  // Follow the import once it is confirmed.
  const loadBatch = useCallback(() => {
    if (batchId === null) return
    api
      .importBatch(batchId)
      .then(setBatch)
      .catch((e) => setError(e instanceof Error ? e.message : String(e)))
  }, [batchId])
  useEffect(() => loadBatch(), [loadBatch])
  useLive(loadBatch, batch?.running === false ? 60000 : 1500)

  async function start() {
    if (!v.attempt()) return
    setError('')
    setStarting(true)
    setJob(null)
    setBatchId(null)
    setBatch(null)
    reviewInit.current = null
    try {
      const { jobId } = await api.scanLibrary(path.trim(), kind)
      setJob(await api.getScan(jobId))
      refreshActive()
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setStarting(false)
    }
  }

  const rowCandidates = useCallback(
    (item: ImportItem) => candidates[item.key] ?? item.candidates,
    [candidates],
  )

  async function searchRow(item: ImportItem, query: string) {
    if (!query.trim()) return
    try {
      const found = await api.tmdbSearch(kind, query.trim())
      setCandidates((prev) => ({ ...prev, [item.key]: found }))
      if (found.length > 0) {
        setChosen((prev) => ({ ...prev, [item.key]: found[0].tmdbId }))
        setChecked((prev) => ({ ...prev, [item.key]: true }))
      }
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    }
  }

  async function confirmImport() {
    if (!job) return
    setError('')
    setConfirming(true)
    const selections = job.items
      .filter((i) => checked[i.key] && chosen[i.key])
      .map((i) => {
        const pick = rowCandidates(i).find((c) => c.tmdbId === chosen[i.key])
        return { key: i.key, tmdbId: chosen[i.key], title: pick?.title, year: pick?.year }
      })
    try {
      const res = await api.runImport(job.id, selections, { monitor, noUpgrade: !monitor, monitorMissing: kind === 'tv' ? monitorMissing : false })
      setJob(null)
      setBatch(null)
      setBatchId(res.batchId)
      setParams({ kind, batch: String(res.batchId) }, { replace: true })
      refreshActive()
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setConfirming(false)
    }
  }

  function another() {
    setBatchId(null)
    setBatch(null)
    setJob(null)
    setError('')
    setParams({ kind }, { replace: true })
  }

  const selectedCount = job ? job.items.filter((i) => checked[i.key] && chosen[i.key]).length : 0
  const scanning = job && (job.phase === 'scanning' || job.phase === 'matching')
  const runningHere = !!active?.batches.some((b) => b.running && b.kind === kind)

  const step = batchId !== null ? 4 : job?.phase === 'ready' ? 3 : job && (job.phase === 'scanning' || job.phase === 'matching') ? 2 : 1
  return (
    <div className="import-page">
      <h1 className="imp-title">{t('Import existing library')}</h1>
      <p className="imp-lede">
        {t('Point Mediarium at a folder of movies or TV shows. It matches the file and folder names to the right titles and lets you review before anything is added. Your files stay where they are, and nothing is moved, renamed or deleted.')}
      </p>
      <ol className="imp-steps" aria-label={t('Import')}>
        {[t('Pick a folder'), t('Scan folder'), t('Check the matches'), t('Import')].map((label, i) => (
          <li key={label} className={i + 1 === step ? 'now' : i + 1 < step ? 'done' : undefined} aria-current={i + 1 === step ? 'step' : undefined}>
            <b>{String(i + 1).padStart(2, '0')}</b> {label}
          </li>
        ))}
      </ol>

      {batchId === null && (
        <section className="card imp-start">
          <div className="imp-kinds" role="radiogroup" aria-label={t('What it is')}>
            <button role="radio" aria-checked={kind === 'movie'} className={`k-movie${kind === 'movie' ? ' active' : ''}`} onClick={() => chooseKind('movie')} disabled={!!scanning}>
              <KindGlyph kind="movie" size={34} variant="solid" />
              {t('Movies')}
            </button>
            <button role="radio" aria-checked={kind === 'tv'} className={`k-tv${kind === 'tv' ? ' active' : ''}`} onClick={() => chooseKind('tv')} disabled={!!scanning}>
              <KindGlyph kind="tv" size={34} variant="solid" />
              {t('TV shows')}
            </button>
          </div>
          <div className="imp-folder">
          <label>
            {t('Folder')}
            <input
              value={path}
              onChange={(e) => setPath(e.target.value)}
              placeholder={example}
              disabled={!!scanning}
              {...v.bind('path', path, setPath)}
            />
            <FieldError v={v} name="path" />
          </label>
          <p style={{ color: 'var(--text-dim)', fontSize: '0.85rem', margin: 0 }}>
            {t('The path as the container sees it. Running in Docker? Mount the folder first.')}
          </p>
          {runningHere && (
            <p style={{ margin: 0 }}>
              {kind === 'movie'
                ? t('A movie import is already running. It carries on in the background, so you can keep using Mediarium.')
                : t('A TV show import is already running. It carries on in the background, so you can keep using Mediarium.')}{' '}
              <Link to={`/import?kind=${kind}&batch=${active?.batches.find((b) => b.running && b.kind === kind)?.id}`}>{t('See how it is going')}</Link>
            </p>
          )}
          <button className="primary btn-with-icon" onClick={start} disabled={starting || !!scanning || runningHere}>
            {starting ? t('Starting…') : t('Scan folder')}
          </button>
          </div>
        </section>
      )}

      {error && <p className="error-text">{error}</p>}

      {batchId === null && job?.phase === 'failed' && <p className="error-text">{job.error}</p>}

      {batchId === null && job && (job.phase === 'scanning' || job.phase === 'matching') && (
        <div className="imp-working">
          <span className="imp-spin" aria-hidden="true" />
          <p>{job.phase === 'scanning' ? t('Scanning files…') : t('Matching titles… {done} / {total}', { done: job.done, total: job.total })}</p>
          {job.phase === 'matching' && job.total > 0 && (
            <div className="bar active" aria-hidden="true">
              <span style={{ width: `${Math.max(3, (job.done / job.total) * 100)}%` }} />
            </div>
          )}
        </div>
      )}

      {batchId === null && job && job.phase === 'ready' && (
        <section>
          {job.items.length === 0 ? (
            <div className="empty-state">{kind === 'movie' ? t('No movies found in that folder.') : t('No episodes found in that folder.')}</div>
          ) : (
            <>
              <div className="imp-found">
                <strong>
                  {kind === 'tv' ? plural(job.items.length, 'Found {n} show', 'Found {n} shows') : plural(job.items.length, 'Found {n} movie', 'Found {n} movies')}
                </strong>
                <button
                  onClick={() =>
                    setChecked(Object.fromEntries(job.items.map((i) => [i.key, i.match === 'matched' && !i.inLibrary])))
                  }
                >
                  {t('Select confident matches')}
                </button>
                <button onClick={() => setChecked({})}>{t('Select none')}</button>
              </div>
              <div className="import-options">
                <Switch
                  checked={monitor}
                  onChange={setMonitor}
                  label={kind === 'tv' ? t('Watch these titles for new episodes and better versions') : t('Watch these titles for better versions')}
                  description={
                    kind === 'tv'
                      ? t("Downloads new episodes as they come out and replaces episodes below your quality profile's target.")
                      : t("Replaces movies below your quality profile's target with a better version.")
                  }
                />
                {kind === 'tv' && (
                  <Switch
                    checked={monitorMissing}
                    onChange={setMonitorMissing}
                    label={t('Also look for missing episodes')}
                    description={t("Searches for and downloads the episodes you don't have, including new ones.")}
                  />
                )}
                <p className="after">
                  <strong>{t('What happens next:')}</strong> {afterText(kind, monitor, kind === 'tv' ? monitorMissing : false)}
                </p>
              </div>

              <button className="primary" disabled={selectedCount === 0 || confirming} onClick={confirmImport}>
                {confirming ? t('Adding…') : t('Import {n} selected', { n: selectedCount })}
              </button>

              <div className="chip-row" role="group" aria-label={t('Show')} style={{ margin: '14px 0 10px' }}>
                {REVIEW_FILTERS.map((f) => (
                  <button key={f.id} className={`chip${reviewFilter === f.id ? ' active' : ''}`} aria-pressed={reviewFilter === f.id} onClick={() => setReviewFilter(f.id)}>
                    {f.label} <small>{job.items.filter(f.test).length}</small>
                  </button>
                ))}
              </div>

              <table className="data-table imp-table">
                <thead>
                  <tr>
                    <th style={{ width: 32 }}></th>
                    <th>{t('Found on disk')}</th>
                    <th>{t('Matched title')}</th>
                    <th>{t('Status')}</th>
                  </tr>
                </thead>
                <tbody>
                  {job.items.filter(REVIEW_FILTERS.find((f) => f.id === reviewFilter)?.test ?? (() => true)).map((item) => (
                    <ReviewRow
                      key={item.key}
                      item={item}
                      kind={kind}
                      checked={!!checked[item.key]}
                      onCheck={(v) => setChecked((p) => ({ ...p, [item.key]: v }))}
                      options={rowCandidates(item)}
                      chosen={chosen[item.key]}
                      onChoose={(id) => setChosen((p) => ({ ...p, [item.key]: id }))}
                      onSearch={(q) => searchRow(item, q)}
                    />
                  ))}
                </tbody>
              </table>

            </>
          )}
          {job.skipped.length > 0 && (
            <details style={{ marginTop: 16 }}>
              <summary>
                {plural(job.skipped.length, "{n} file wasn't recognised", "{n} files weren't recognised")}
              </summary>
              <ul style={{ fontFamily: 'var(--font-mono)', fontSize: '0.8rem', color: 'var(--text-dim)' }}>
                {job.skipped.map((s) => (
                  <li key={s}>{s}</li>
                ))}
              </ul>
            </details>
          )}
        </section>
      )}

      {batchId !== null && batch && <Report batch={batch} onAnother={another} onChanged={loadBatch} />}
      {batchId !== null && !batch && !error && <p>{t('Loading…')}</p>}
    </div>
  )
}

// The progress of an import that has been confirmed, and its report.
function Report({ batch, onAnother, onChanged }: { batch: ImportBatchDetail; onAnother: () => void; onChanged: () => void }) {
  const eta = batch.running ? etaText(batch) : ''
  const pct = batch.total > 0 ? (batch.done / batch.total) * 100 : 100
  const episodes = batch.kind === 'tv'
  return (
    <section>
      <h2 style={{ marginTop: 0 }}>{batch.running ? t('Importing your library') : t('Import finished')}</h2>
      {batch.running && (
        <div className="import-progress">
          <div>
            <strong>{progressText(batch)}</strong> {eta} {t('You can leave this page: the import carries on in the background.')}
          </div>
          <div className="bar active" aria-hidden="true">
            <span style={{ width: `${Math.max(3, pct)}%` }} />
          </div>
        </div>
      )}
      {batch.added > 0 && <WhatWasSet batch={batch} onChanged={onChanged} />}
      <table className="data-table imp-table">
        <thead>
          <tr>
            <th>{t('Title')}</th>
            <th>{episodes ? t('Episodes added') : t('Added')}</th>
            <th>{t('Already in library')}</th>
            <th>{t('Notes')}</th>
          </tr>
        </thead>
        <tbody>
          {batch.items.map((it, i) => (
            <ReportRow key={`${it.title}-${i}`} item={it} />
          ))}
        </tbody>
      </table>
      <p className="import-total">{totalLine(batch)}</p>
      <p className="import-note">
        {t('Added means new to your library. Already in library means it was here before, so nothing changed.')}
        {episodes && ` ${t('For shows, the numbers count episodes.')}`}
        {batch.problems > 0 && ` ${t('The ones with problems are retried in the background.')}`}
      </p>
      <p style={{ display: 'flex', gap: 16, alignItems: 'center', flexWrap: 'wrap' }}>
        <Link to={`/library?kind=${batch.kind}`}>{t('Go to Library →')}</Link>
        <button onClick={onAnother}>{t('Import another folder')}</button>
      </p>
    </section>
  )
}

// Says what was set on the imported titles, and offers to start monitoring
// exactly these titles (not the rest of the library) once the import is done.
function WhatWasSet({ batch, onChanged }: { batch: ImportBatchDetail; onChanged: () => void }) {
  const toast = useToast()
  const [busy, setBusy] = useState<'watch' | 'missing' | null>(null)
  const [error, setError] = useState('')
  const shows = batch.kind === 'tv'

  async function run(what: 'watch' | 'missing') {
    setBusy(what)
    setError('')
    try {
      const res = await api.watchImport(batch.id, what === 'watch' ? { watch: true } : { missing: true })
      toast.success(res.message)
      onChanged()
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(null)
    }
  }

  const canWatch = !batch.monitor
  const canMissing = shows && !batch.monitorMissing
  return (
    <div className="import-set">
      <p>
        <strong>{t('What was set:')}</strong> {setText(batch)}
        {batch.running && (canWatch || canMissing) && ` ${t('You can change this when the import has finished.')}`}
      </p>
      {!batch.running && (canWatch || canMissing) && (
        <div className="import-set-actions">
          {canWatch && (
            <button className="primary" disabled={busy !== null} onClick={() => void run('watch')}>
              {busy === 'watch' ? t('Working…') : t('Start monitoring these titles')}
            </button>
          )}
          {canMissing && (
            <button disabled={busy !== null} onClick={() => void run('missing')}>
              {busy === 'missing' ? t('Working…') : t('Look for missing episodes')}
            </button>
          )}
        </div>
      )}
      {error && <p className="error-text">{error}</p>}
    </div>
  )
}

function ReportRow({ item }: { item: ImportBatchItem }) {
  const waiting = item.state === 'pending'
  let added: string | number
  let already: string | number
  if (item.kind === 'series') {
    added = waiting ? '…' : item.imported
    already = waiting ? '…' : item.skipped
  } else {
    added = item.outcome === 'added' ? 1 : 0
    already = item.outcome === 'already' ? 1 : 0
  }
  const problem = item.state === 'problem' || item.outcome === 'failed'
  return (
    <tr>
      <td>{item.title}</td>
      <td>{added}</td>
      <td>{already}</td>
      <td className={problem ? 'error-text' : undefined} style={problem ? undefined : { color: 'var(--text-dim)' }}>
        {waiting && !item.note ? t('Getting details…') : item.note || (waiting ? t('Getting details…') : '')}
      </td>
    </tr>
  )
}

function ReviewRow(props: {
  item: ImportItem
  kind: Kind
  checked: boolean
  onCheck: (v: boolean) => void
  options: ImportCandidate[]
  chosen: number | undefined
  onChoose: (id: number) => void
  onSearch: (q: string) => void
}) {
  const { item, kind, checked, options, chosen } = props
  const [searching, setSearching] = useState(false)
  const [query, setQuery] = useState(item.title)

  const summary =
    kind === 'tv'
      ? `${plural(item.fileCount, '{n} episode file', '{n} episode files')}${item.seasons && item.seasons.length ? ` · ${seasonRange(item.seasons)}` : ''}`
      : `${item.fileCount > 1 ? `${t('{n} files', { n: item.fileCount })} · ` : ''}${item.quality && item.quality !== 'Unknown' ? item.quality + ' · ' : ''}${formatBytes(item.sizeBytes)}`

  return (
    <tr>
      <td>
        <input
          type="checkbox"
          checked={checked}
          disabled={!chosen}
          onChange={(e) => props.onCheck(e.target.checked)}
          aria-label={t('Import {title}', { title: item.title })}
        />
      </td>
      <td>
        <strong>
          {item.title} {item.year ? `(${item.year})` : ''}
        </strong>
        <div style={{ color: 'var(--text-dim)', fontSize: '0.8rem' }}>{summary}</div>
        <div style={{ color: 'var(--text-dim)', fontSize: '0.75rem', fontFamily: 'var(--font-mono)', wordBreak: 'break-all' }}>
          {item.samplePath}
        </div>
      </td>
      <td>
        {options.length > 0 ? (
          <select value={chosen ?? ''} onChange={(e) => props.onChoose(Number(e.target.value))}>
            {options.map((c) => (
              <option key={c.tmdbId} value={c.tmdbId}>
                {c.title} {c.year ? `(${c.year})` : ''}
              </option>
            ))}
          </select>
        ) : (
          <span style={{ color: 'var(--text-dim)' }}>{t('no match')}</span>
        )}{' '}
        <button onClick={() => setSearching((v) => !v)}>{searching ? t('Cancel') : t('Search…')}</button>
        {searching && (
          <form
            style={{ display: 'flex', gap: 6, marginTop: 6 }}
            onSubmit={(e) => {
              e.preventDefault()
              props.onSearch(query)
              setSearching(false)
            }}
          >
            <input value={query} onChange={(e) => setQuery(e.target.value)} />
            <button className="primary">{t('Go')}</button>
          </form>
        )}
        {item.error && <div className="error-text">{item.error}</div>}
      </td>
      <td>
        {item.inLibrary ? (
          <span className="badge downloaded">{t('already in library')}</span>
        ) : (
          <span className={`badge ${item.match === 'matched' ? 'downloaded' : item.match === 'ambiguous' ? 'conflict' : 'failed'}`}>
            {item.match === 'matched' ? t('matched') : item.match === 'ambiguous' ? t('check match') : t('unmatched')}
          </span>
        )}
      </td>
    </tr>
  )
}

function seasonRange(seasons: number[]): string {
  return seasons.length === 1 ? t('Season {n}', { n: seasons[0] }) : t('Seasons {from}–{to}', { from: seasons[0], to: seasons[seasons.length - 1] })
}
