import { Fragment, useCallback, useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { api, type BookFormat, type BookFound, type BookImportResult, type BookImportState } from '../api'
import { canFindByHand, matchBody, resultLine, suggestedQuery } from '../bookMatch'
import Icon from './Icon'
import { PosterFallback } from './PosterCard'
import { useToast } from './Toast'
import { t, tx, st as serverText } from '../i18n'

const STATUS_LABEL: Record<string, string> = { imported: t('Added'), already: t('Already there'), unmatched: t('Not found'), failed: t('Failed') }

// "Import": reads the ebooks or audiobooks folder for books that are already
// there and adds them to the library where they are. Nothing is moved.
export default function BookImport({ format, folder, onDone }: { format: BookFormat; folder: string; onDone: () => void }) {
  const toast = useToast()
  const [st, setSt] = useState<BookImportState | null>(null)
  const [starting, setStarting] = useState(false)
  const [showAll, setShowAll] = useState(false)
  const [finding, setFinding] = useState('') // the row whose search box is open
  const running = st?.phase === 'running'

  const poll = useCallback(() => api.bookImportStatus().then(setSt).catch(() => undefined), [])
  useEffect(() => {
    void poll()
  }, [poll])
  useEffect(() => {
    if (!running) return
    const timer = setInterval(() => {
      void api.bookImportStatus().then((s) => {
        setSt(s)
        if (s.phase !== 'running') onDone()
      })
    }, 1500)
    return () => clearInterval(timer)
  }, [running, onDone])

  async function start() {
    setStarting(true)
    try {
      setSt(await api.startBookImport(format))
    } catch (e) {
      toast.error(e instanceof Error ? e.message : String(e))
    } finally {
      setStarting(false)
    }
  }

  async function retry() {
    setStarting(true)
    try {
      setSt(await api.retryBookImport(format))
    } catch (e) {
      toast.error(e instanceof Error ? e.message : String(e))
    } finally {
      setStarting(false)
    }
  }

  const ebooks = format === 'ebook'
  const results = (st?.results ?? []).filter((r) => r.kind === format)
  const problems = results.filter((r) => r.status === 'unmatched' || r.status === 'failed')
  const listed = showAll ? results : problems
  const sum = st?.summary ?? {}
  return (
    <section className="card book-import">
      <div className="book-import-head">
        <div>
          <h2>{ebooks ? t('Import the ebooks you already have') : t('Import the audiobooks you already have')}</h2>
          <p className="hint">
            {tx('Mediarium reads {folder}, looks each book up on Open Library and adds it to your library where it is. Nothing is moved or renamed. Folders named Author/Title work best.', {
              folder: <code>{folder || (ebooks ? t('your ebooks folder') : t('your audiobooks folder'))}</code>,
            })}
          </p>
        </div>
        <button className="primary btn-with-icon" onClick={() => void start()} disabled={starting || running}>
          <Icon name="folder" size={16} /> {running ? t('Importing…') : st?.phase ? t('Import again') : t('Start import')}
        </button>
      </div>
      {running && (
        <div>
          <div className="bar" title={t('{done} of {total}', { done: st.done, total: st.total })}>
            <span style={{ width: `${st.total ? (st.done / st.total) * 100 : 0}%` }} />
          </div>
          <small className="hint">
            {t('{done} of {total} looked up…', { done: st.done, total: st.total })}
          </small>
        </div>
      )}
      {st?.phase === 'failed' && <p className="error-text">{st.error}</p>}
      {st?.phase === 'done' && (
        <p>
          {results.length === 0 ? (
            ebooks ? t('No ebooks found in the folder.') : t('No audiobooks found in the folder.')
          ) : sum.failed ? (
            tx('{added} added, {already} already in your library, {missing} not found, {failed} failed.', { added: <strong>{sum.imported ?? 0}</strong>, already: sum.already ?? 0, missing: sum.unmatched ?? 0, failed: sum.failed })
          ) : (
            tx('{added} added, {already} already in your library, {missing} not found.', { added: <strong>{sum.imported ?? 0}</strong>, already: sum.already ?? 0, missing: sum.unmatched ?? 0 })
          )}
        </p>
      )}
      {results.length > 0 && (
        <>
          <div className="row-actions">
            {problems.length > 0 && !running && (
              <button className="btn-sm" disabled={starting} onClick={() => void retry()} title={t('Looks again only at the books that were not found or could not be saved. It does not read the folders again.')}>
                {t('Run the {n} not found again', { n: problems.length })}
              </button>
            )}
            <button className="btn-sm" onClick={() => setShowAll((v) => !v)}>
              {showAll ? t('Show only the ones not found') : t('Show all {n}', { n: results.length })}
            </button>
          </div>
          {listed.length > 0 && (
            <div className="table-scroll">
              <table className="import-table">
                <thead>
                  <tr>
                    <th>{t('On disk')}</th>
                    <th>{t('Read as')}</th>
                    <th>{t('Result')}</th>
                  </tr>
                </thead>
                <tbody>
                  {listed.map((r) => (
                    <Fragment key={`${r.kind}-${r.path}`}>
                      <tr>
                        <td>
                          <code>{r.path}</code>
                        </td>
                        <td>
                          {r.title}
                          {r.author ? ` · ${r.author}` : ''}
                        </td>
                        <td title={serverText(r.message)}>
                          {r.bookId ? <Link to={`/book/${r.bookId}`}>{STATUS_LABEL[r.status] ?? r.status}</Link> : (STATUS_LABEL[r.status] ?? r.status)}
                          {r.message ? <small className="hint"> {serverText(r.message)}</small> : null}
                          {canFindByHand(r.status) && !running && (
                            <div>
                              <button className="btn-sm" onClick={() => setFinding(finding === `${r.kind}-${r.path}` ? '' : `${r.kind}-${r.path}`)}>
                                {t('Find it myself')}
                              </button>
                            </div>
                          )}
                        </td>
                      </tr>
                      {finding === `${r.kind}-${r.path}` && (
                        <tr>
                          <td colSpan={3}>
                            <FindByHand
                              row={r}
                              onClose={() => setFinding('')}
                              onMatched={(next) => {
                                setSt(next)
                                setFinding('')
                                onDone()
                              }}
                            />
                          </td>
                        </tr>
                      )}
                    </Fragment>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </>
      )}
    </section>
  )
}

// "Find it myself": search Open Library for a book the import did not find,
// pick it, and it is attached to that file.
function FindByHand({ row, onClose, onMatched }: { row: BookImportResult; onClose: () => void; onMatched: (st: BookImportState) => void }) {
  const toast = useToast()
  const [q, setQ] = useState(suggestedQuery(row))
  const [results, setResults] = useState<BookFound[] | null>(null)
  const [error, setError] = useState('')
  const [rename, setRename] = useState(false)
  const [busy, setBusy] = useState('')

  useEffect(() => {
    const term = q.trim()
    if (term.length < 2) {
      setResults(null)
      return
    }
    setError('')
    // A slow answer for an earlier search must not replace the one for what is typed now.
    let stale = false
    const timer = setTimeout(() => {
      api
        .searchBooks(term)
        .then((r) => !stale && setResults(r.slice(0, 8)))
        .catch((e) => !stale && setError(e instanceof Error ? e.message : String(e)))
    }, 350)
    return () => {
      stale = true
      clearTimeout(timer)
    }
  }, [q])

  async function pick(f: BookFound) {
    setBusy(f.key)
    try {
      onMatched(await api.matchBookImport(matchBody(row, f, rename)))
    } catch (e) {
      toast.error(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy('')
    }
  }

  return (
    <div className="book-add find-by-hand">
      <div className="book-add-head">
        <input type="search" autoFocus placeholder={t('Title and author, for example The Hobbit Tolkien')} value={q} onChange={(e) => setQ(e.target.value)} aria-label={t('Search Open Library')} />
        <label className="check-inline" title={t('Moves the file to where Mediarium puts a book it downloads, named after the book you pick.')}>
          <input type="checkbox" checked={rename} onChange={(e) => setRename(e.target.checked)} /> {t('Rename the file to match')}
        </label>
        <button className="btn-sm" onClick={onClose}>
          {t('Cancel')}
        </button>
      </div>
      {error && <p className="error-text">{error}</p>}
      {results !== null && results.length === 0 && <p className="hint">{t("No books found. Try the author's name, or fewer words.")}</p>}
      {results && results.length > 0 && (
        <div className="book-results">
          {results.map((f) => (
            <div key={f.key} className="book-result">
              <span className="book-thumb">{f.coverUrl ? <img src={f.coverUrl.replace('-M.jpg', '-S.jpg')} alt="" loading="lazy" /> : <PosterFallback />}</span>
              <span className="book-result-text" title={resultLine(f)}>
                <strong>{f.title}</strong>
                <small>{[f.author, f.year].filter(Boolean).join(' · ')}</small>
              </span>
              <button className="btn-sm primary" disabled={busy !== ''} onClick={() => void pick(f)}>
                {busy === f.key ? t('Matching…') : t('This one')}
              </button>
            </div>
          ))}
        </div>
      )}
    </div>
  )
}
