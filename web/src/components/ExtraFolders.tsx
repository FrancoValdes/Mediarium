import { useEffect, useState } from 'react'
import { api } from '../api'
import FolderStatus from './FolderStatus'
import Icon from './Icon'
import { useToast } from './Toast'
import { t } from '../i18n'

// Settings > Library > Folders and file names: more movie or TV folders next
// to the main one, for another drive or a folder kept apart (kids, concerts,
// documentaries). When there are some, adding a title asks which folder to
// keep it in, and a title's page can move it to another for new files. Saved
// with its own button.
export default function ExtraFolders({ kind }: { kind: 'movies' | 'tv' }) {
  const toast = useToast()
  const key = kind === 'movies' ? 'moviesExtraPaths' : 'tvExtraPaths'
  const [saved, setSaved] = useState<string[] | null>(null)
  const [rows, setRows] = useState<string[]>([])
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')

  useEffect(() => {
    api
      .getSettings()
      .then((s) => {
        const list = (s[key] as string[] | undefined) ?? []
        setSaved(list)
        setRows(list)
      })
      .catch(() => setSaved([]))
  }, [key])

  const cleaned = rows.map((r) => r.trim()).filter(Boolean)
  const changed = saved !== null && cleaned.join('\n') !== saved.join('\n')

  async function save() {
    setBusy(true)
    setError('')
    try {
      const s = await api.putSettings({ [key]: cleaned })
      const list = (s[key] as string[] | undefined) ?? cleaned
      setSaved(list)
      setRows(list)
      toast.success(list.length === 0 ? (kind === 'movies' ? t('Saved: only the main movies folder is used.') : t('Saved: only the main TV folder is used.')) : kind === 'movies' ? t('Saved: movies can also go in {n} more folders.', { n: list.length }) : t('Saved: shows can also go in {n} more folders.', { n: list.length }))
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="path-col">
      <strong>{kind === 'movies' ? t('More movie folders') : t('More TV folders')}</strong>
      {rows.map((row, i) => (
        <div key={i} className="extra-folder">
          <div className="extra-folder-row">
            <input
              value={row}
              placeholder={kind === 'movies' ? '/movies-kids' : '/tv-kids'}
              spellCheck={false}
              aria-label={kind === 'movies' ? t('A movie folder') : t('A TV folder')}
              onChange={(e) => setRows((r) => r.map((x, j) => (j === i ? e.target.value : x)))}
            />
            <button className="icon-btn" onClick={() => setRows((r) => r.filter((_, j) => j !== i))} aria-label={t('Remove this folder from the list')} title={t('Remove this folder from the list')}>
              <Icon name="trash" size={15} />
            </button>
          </div>
          {row.trim() && <FolderStatus path={row.trim()} />}
        </div>
      ))}
      <div className="row-actions">
        <button className="btn-sm btn-with-icon" onClick={() => setRows((r) => [...r, ''])}>
          <Icon name="plus" size={14} /> {t('Add a folder')}
        </button>
        {changed && (
          <button className="btn-sm primary" disabled={busy} onClick={() => void save()}>
            {busy ? t('Saving…') : t('Save folders')}
          </button>
        )}
      </div>
      {error && <p className="field-error" role="alert">{error}</p>}
      <small className="path-note">{kind === 'movies' ? t('For example /movies-kids or /mnt/disk2/movies. When you add a movie you choose which folder it goes in; the main folder is the default.') : t('For example /tv-kids or /mnt/disk2/tv. When you add a show you choose which folder it goes in; the main folder is the default.')}</small>
    </div>
  )
}
