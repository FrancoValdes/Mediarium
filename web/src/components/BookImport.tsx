import { useCallback, useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { api, type BookFormat, type BookImportState } from '../api'
import Icon from './Icon'
import { useToast } from './Toast'
import { t, tx } from '../i18n'

const STATUS_LABEL: Record<string, string> = { imported: t('Added'), already: t('Already there'), unmatched: t('Not found'), failed: t('Failed') }

// "Import": reads the ebooks or audiobooks folder for books that are already
// there and adds them to the library where they are. Nothing is moved.
export default function BookImport({ format, folder, onDone }: { format: BookFormat; folder: string; onDone: () => void }) {
  const toast = useToast()
  const [st, setSt] = useState<BookImportState | null>(null)
  const [starting, setStarting] = useState(false)
  const [showAll, setShowAll] = useState(false)
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
                    <tr key={`${r.kind}-${r.path}`}>
                      <td>
                        <code>{r.path}</code>
                      </td>
                      <td>
                        {r.title}
                        {r.author ? ` · ${r.author}` : ''}
                      </td>
                      <td title={r.message}>
                        {r.bookId ? <Link to={`/book/${r.bookId}`}>{STATUS_LABEL[r.status] ?? r.status}</Link> : (STATUS_LABEL[r.status] ?? r.status)}
                        {r.message && r.status !== 'imported' ? <small className="hint"> {r.message}</small> : null}
                      </td>
                    </tr>
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
