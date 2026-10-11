import { DEFAULT_PERMISSIONS, type Permission, type Permissions } from '../api'
import { useModules } from '../ModulesContext'
import { t } from '../i18n'
import TipLabel from './TipLabel'

// What a basic account may do, chosen when adding or editing it. Everything
// is on by default, which is what a basic account could always do.

const ROWS: { key: Permission; label: string; hint: string }[] = [
  { key: 'addDirect', label: t('Add titles themselves'), hint: t('Off: what they add becomes a request you approve or decline under Activity > Requests.') },
  { key: 'releases', label: t('Pick releases'), hint: t('Search your indexers and choose what to download.') },
  { key: 'manage', label: t('Search now and monitoring'), hint: t('Start searches, change what is monitored and tags, follow authors and series.') },
  { key: 'retry', label: t('Retry downloads'), hint: t('Try a failed or stopped download again.') },
  { key: 'subtitles', label: t('Subtitles'), hint: t('Find and download subtitles.') },
  { key: 'play', label: t('Play, read and listen'), hint: t('Play videos, preview files, and read and listen in Mediarium Books.') },
]

export default function PermissionsChoice({ value, onChange, disabled }: { value: Permissions | undefined; onChange: (p: Permissions) => void; disabled?: boolean }) {
  const { on } = useModules()
  const p = value ?? DEFAULT_PERMISSIONS
  const set = (k: Permission, v: boolean) => onChange({ ...p, [k]: v })
  const kinds: { key: Permission; label: string; show: boolean }[] = [
    { key: 'movies', label: t('Movies'), show: on('movies') },
    { key: 'tv', label: t('TV shows'), show: on('tv') },
    { key: 'music', label: t('Music'), show: on('music') },
    { key: 'books', label: t('Books'), show: on('ebooks') || on('audiobooks') },
  ]
  return (
    <div className="perm-choice">
      <span className="field-title"><TipLabel help="accounts#permissions-for-basic-users">{t('Permissions')}</TipLabel></span>
      <div className="perm-kinds" role="group" aria-label={t('What they can add or ask for')}>
        <span className="field-note">{t('They can add or ask for:')}</span>
        {kinds
          .filter((k) => k.show)
          .map((k) => (
            <label key={k.key} className="perm-kind">
              <input type="checkbox" checked={p[k.key]} disabled={disabled} onChange={(e) => set(k.key, e.target.checked)} /> {k.label}
            </label>
          ))}
      </div>
      <div className="perm-rows">
        {ROWS.map((r) => (
          <label key={r.key} className="perm-row">
            <input type="checkbox" checked={p[r.key]} disabled={disabled} onChange={(e) => set(r.key, e.target.checked)} />
            <span>
              <strong>{r.label}</strong>
              <small>{r.hint}</small>
            </span>
          </label>
        ))}
      </div>
    </div>
  )
}
