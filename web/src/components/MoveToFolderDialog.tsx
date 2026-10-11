import { createPortal } from 'react-dom'
import { useEffect, useRef, useState } from 'react'
import { api, type BulkTitle, type MovePreview, type MoveStatus } from '../api'
import { formatBytes } from '../format'
import { plural, t } from '../i18n'
import { useFocusTrap } from '../useFocusTrap'
import { useToast } from './Toast'
import Icon from './Icon'

// The Library's "Move to folder" for the titles picked: choose the folder, and
// whether the files already in the library move with it. Moving files shows
// what would happen first, then runs in the background with its progress here.
export default function MoveToFolderDialog({
  kind,
  titles,
  folders,
  initial,
  onClose,
  onChanged,
}: {
  kind: 'movie' | 'tv'
  titles: BulkTitle[]
  folders: string[]
  initial: string
  onClose: () => void
  onChanged: () => void
}) {
  const toast = useToast()
  const [folder, setFolder] = useState(initial)
  const [withFiles, setWithFiles] = useState(false)
  const [preview, setPreview] = useState<MovePreview | null>(null)
  const [checking, setChecking] = useState(false)
  const [status, setStatus] = useState<MoveStatus | null>(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const box = useRef<HTMLDivElement>(null)
  useFocusTrap(box)

  const noun = kind === 'movie' ? t('movies') : t('shows')
  const what = kind === 'movie' ? plural(titles.length, '{n} movie', '{n} movies') : plural(titles.length, '{n} show', '{n} shows')
  const running = !!status?.running
  const finished = !!status && !status.running && !!status.finishedAt

  // A move that is already going on (this dialog was closed and opened again).
  useEffect(() => {
    api
      .moveFilesStatus()
      .then((s) => s.running && setStatus(s))
      .catch(() => undefined)
  }, [])

  // What moving the files would do, whenever the choice changes.
  useEffect(() => {
    if (!withFiles || running || finished) return
    let current = true
    setChecking(true)
    setError('')
    api
      .previewMoveFiles(titles, folder)
      .then((p) => current && setPreview(p))
      .catch((e) => current && setError(e instanceof Error ? e.message : String(e)))
      .finally(() => current && setChecking(false))
    return () => {
      current = false
    }
  }, [withFiles, folder, titles, running, finished])

  // Follow a move that is going on.
  useEffect(() => {
    if (!running) return
    const timer = setInterval(() => {
      api
        .moveFilesStatus()
        .then(setStatus)
        .catch(() => undefined)
    }, 800)
    return () => clearInterval(timer)
  }, [running])

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => e.key === 'Escape' && !busy && close()
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [busy, finished])

  function close() {
    if (finished) onChanged()
    onClose()
  }

  async function go() {
    setBusy(true)
    setError('')
    try {
      if (!withFiles) {
        const r = await api.bulkFolder(titles, folder)
        if (r.failed.length > 0) {
          setError(`${r.message} ${r.failed.map((f) => `${f.title ?? ''}: ${f.reason}`).join(' ')}`)
          onChanged()
        } else {
          toast.success(plural(r.updated, 'Done. The next files of {n} title will go to {folder}.', 'Done. The next files of {n} titles will go to {folder}.', { folder }))
          onChanged()
          onClose()
        }
        return
      }
      setStatus(await api.moveFiles(titles, folder))
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(false)
    }
  }

  async function stop() {
    try {
      setStatus(await api.cancelMoveFiles())
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    }
  }

  const nothingToMove = withFiles && preview !== null && preview.canMove === 0
  const goLabel = withFiles ? t('Move the files') : t('Use this folder')

  return createPortal(
    <div className="modal-backdrop" onMouseDown={(e) => e.target === e.currentTarget && !busy && close()}>
      <div ref={box} tabIndex={-1} className="modal" role="dialog" aria-modal="true" aria-label={t('Move to folder')}>
        <div className="modal-head">
          <div style={{ flex: 1, minWidth: 0 }}>
            <h2 style={{ margin: '0 0 4px' }}>{t('Move {what} to a folder', { what })}</h2>
          </div>
          <button className="icon-btn" onClick={close} disabled={busy} aria-label={t('Close')}>
            <Icon name="x" />
          </button>
        </div>

        <div className="modal-body" style={{ display: 'grid', gap: 14 }}>
          {status === null && (
            <>
              <label style={{ display: 'grid', gap: 4 }}>
                <span>{t('Folder')}</span>
                <select value={folder} onChange={(e) => setFolder(e.target.value)} disabled={busy}>
                  {folders.map((f, i) => (
                    <option key={f} value={f}>
                      {i === 0 ? t('{folder} (main)', { folder: f }) : f}
                    </option>
                  ))}
                </select>
              </label>
              <fieldset className="radio-group" style={{ border: 0, padding: 0, margin: 0, display: 'grid', gap: 8 }}>
                <label style={{ display: 'flex', gap: 8, alignItems: 'flex-start' }}>
                  <input type="radio" name="move-how" checked={!withFiles} onChange={() => setWithFiles(false)} disabled={busy} />
                  <span>
                    <strong>{t('Only use this folder for new files')}</strong>
                    <small className="field-hint" style={{ display: 'block' }}>
                      {t('The files already in your library stay where they are.')}
                    </small>
                  </span>
                </label>
                <label style={{ display: 'flex', gap: 8, alignItems: 'flex-start' }}>
                  <input type="radio" name="move-how" checked={withFiles} onChange={() => setWithFiles(true)} disabled={busy} />
                  <span>
                    <strong>{t('Also move the files that are already there')}</strong>
                    <small className="field-hint" style={{ display: 'block' }}>
                      {t('On the same drive this is instant. To another drive each file is copied and checked, and only then removed from the old place. Nothing is overwritten, and a {noun} that cannot be moved completely is left as it is.', { noun: kind === 'movie' ? t('movie') : t('show') })}
                    </small>
                  </span>
                </label>
              </fieldset>
              {withFiles && <Preview preview={preview} checking={checking} noun={noun} />}
            </>
          )}
          {status !== null && <Progress status={status} onStop={() => void stop()} />}
          {error && <p className="error-text">{error}</p>}
        </div>

        <div className="modal-foot">
          {status === null ? (
            <>
              <button onClick={close} disabled={busy}>
                {t('Cancel')}
              </button>
              <button className="primary" onClick={() => void go()} disabled={busy || checking || nothingToMove || !!preview?.problem && withFiles}>
                {busy ? t('Working…') : goLabel}
              </button>
            </>
          ) : (
            <button className={finished ? 'primary' : undefined} onClick={close}>
              {finished ? t('Done') : t('Close (it keeps going)')}
            </button>
          )}
        </div>
      </div>
    </div>,
    document.body,
  )
}

function Preview({ preview, checking, noun }: { preview: MovePreview | null; checking: boolean; noun: string }) {
  if (!preview) return <p className="field-hint">{checking ? t('Checking…') : ''}</p>
  const copying = preview.copyBytes > 0
  return (
    <div className="notice" style={{ display: 'grid', gap: 8 }} role="status">
      <strong>
        {preview.canMove === 0
          ? t('None of these {noun} can be moved.', { noun })
          : plural(preview.canMove, 'One title can be moved: {files}, {size}.', '{n} titles can be moved: {files}, {size}.', { files: plural(preview.files, '{n} file', '{n} files'), size: formatBytes(preview.bytes) })}
      </strong>
      {copying && (
        <span>
          {t('{size} of it is going to another drive, so it is copied and checked first. That takes a while; the move keeps going if you close this window.', { size: formatBytes(preview.copyBytes) })}
        </span>
      )}
      {preview.problem && <span className="error-text">{preview.problem}</span>}
      {preview.blocked > 0 && (
        <details>
          <summary>{plural(preview.blocked, 'One title will be left as it is', '{n} titles will be left as they are')}</summary>
          <ul style={{ margin: '6px 0 0', paddingLeft: 18 }}>
            {preview.titles
              .filter((x) => x.problem)
              .map((x) => (
                <li key={`${x.kind}-${x.id}`}>
                  <strong>{x.title || t('A title')}</strong>: {x.problem}
                </li>
              ))}
          </ul>
        </details>
      )}
      {preview.titles.some((x) => x.note) && (
        <ul style={{ margin: 0, paddingLeft: 18 }}>
          {preview.titles
            .filter((x) => x.note)
            .map((x) => (
              <li key={`${x.kind}-${x.id}`}>
                <strong>{x.title}</strong>: {x.note}
              </li>
            ))}
        </ul>
      )}
    </div>
  )
}

function Progress({ status, onStop }: { status: MoveStatus; onStop: () => void }) {
  const pct = status.bytesAll > 0 ? Math.min(100, (status.bytesDone / status.bytesAll) * 100) : status.titles > 0 ? (status.done / status.titles) * 100 : 0
  return (
    <div style={{ display: 'grid', gap: 10 }} role="status">
      {status.running ? (
        <>
          <div className="bar active">
            <span style={{ width: `${Math.max(3, pct)}%` }} />
          </div>
          <small className="field-hint">
            {t('{done} of {total} titles', { done: status.done, total: status.titles })}
            {status.current ? ` · ${status.current}` : ''}
            {status.bytesAll > 0 ? ` · ${formatBytes(status.bytesDone)} of ${formatBytes(status.bytesAll)} copied` : ''}
          </small>
          <div>
            <button onClick={onStop}>{t('Stop')}</button>
          </div>
        </>
      ) : (
        <strong>{status.message}</strong>
      )}
      {status.warnings.length > 0 && (
        <ul style={{ margin: 0, paddingLeft: 18 }}>
          {status.warnings.map((w, i) => (
            <li key={i}>{w}</li>
          ))}
        </ul>
      )}
      {status.problems.length > 0 && (
        <details open={!status.running}>
          <summary>{plural(status.problems.length, 'One title was left as it is', '{n} titles were left as they are')}</summary>
          <ul style={{ margin: '6px 0 0', paddingLeft: 18 }}>
            {status.problems.map((p, i) => (
              <li key={i}>
                <strong>{p.title || t('A title')}</strong>: {p.reason}
              </li>
            ))}
          </ul>
        </details>
      )}
    </div>
  )
}
