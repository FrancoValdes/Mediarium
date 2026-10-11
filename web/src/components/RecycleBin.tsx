import { useCallback, useEffect, useState } from 'react'
import { api, type TrashList } from '../api'
import { formatBytes, timeAgo } from '../format'
import { useConfirm } from './ConfirmProvider'
import { plural, t } from '../i18n'
import Icon from './Icon'
import HelpLink from './HelpLink'
import { useToast } from './Toast'

// Activity > Recycle bin: files of titles removed "with their files", kept a
// few days so a slip can be undone. Putting files back does not add the
// title to the library again; Import (or adding it) does that.
export default function RecycleBin() {
  const [bin, setBin] = useState<TrashList | null>(null)
  const [busy, setBusy] = useState('')
  const confirm = useConfirm()
  const toast = useToast()

  const load = useCallback(() => {
    api
      .listTrash()
      .then(setBin)
      .catch((e) => toast.error(e instanceof Error ? e.message : String(e)))
  }, [toast])
  useEffect(load, [load])

  async function run(key: string, fn: () => Promise<unknown>, done: string) {
    setBusy(key)
    try {
      await fn()
      toast.success(done)
      load()
    } catch (e) {
      toast.error(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy('')
    }
  }

  if (!bin) return null
  const days = bin.days
  return (
    <>
      <p style={{ color: 'var(--text-dim)' }}>
        {days > 0
          ? plural(days, 'When you remove a title with its files, the files wait here for {n} day before they are deleted for good. Put them back if it was a mistake.', 'When you remove a title with its files, the files wait here for {n} days before they are deleted for good. Put them back if it was a mistake.')
          : t('The recycle bin is off: removed files are deleted straight away. Switch it on under Settings > System > Server.')}
        <HelpLink topic="downloads#the-recycle-bin" text={t('Put back moves the files into their old folder. It does not add the title to your library again; use Import for that. Delete now removes them for good.')} />
      </p>
      {bin.items.length === 0 ? (
        <div className="empty-state">
          <Icon name="trash" size={44} />
          <p>{t('The recycle bin is empty.')}</p>
        </div>
      ) : (
        <>
          <div style={{ marginBottom: 12 }}>
            <button
              className="btn-sm btn-danger"
              disabled={busy !== ''}
              onClick={async () => {
                if (!(await confirm({ title: t('Empty the recycle bin?'), body: <p>{t('Every file in it is deleted for good. This cannot be undone.')}</p>, confirmLabel: t('Empty it'), danger: true }))) return
                await run('all', () => api.emptyTrash(), t('The recycle bin is empty.'))
              }}
            >
              {t('Empty the recycle bin')}
            </button>
          </div>
          <table>
            <thead>
              <tr>
                <th>{t('Removed')}</th>
                <th>{t('Size')}</th>
                <th>{t('When')}</th>
                <th />
              </tr>
            </thead>
            <tbody>
              {bin.items.map((it) => {
                const key = `${it.kind}/${it.id}`
                return (
                  <tr key={key}>
                    <td>
                      <strong>{it.label}</strong>
                      <div style={{ color: 'var(--text-dim)', fontSize: '0.82rem', wordBreak: 'break-all' }}>{it.paths[0]}</div>
                    </td>
                    <td>
                      {formatBytes(it.size)}
                      <div style={{ color: 'var(--text-dim)', fontSize: '0.82rem' }}>{plural(it.files, '{n} file', '{n} files')}</div>
                    </td>
                    <td>
                      {timeAgo(it.deletedAt)}
                      {it.expiresAt && <div style={{ color: 'var(--text-dim)', fontSize: '0.82rem' }}>{t('deleted for good {when}', { when: inDays(it.expiresAt) })}</div>}
                    </td>
                    <td className="row-actions">
                      <button
                        className="btn-sm btn-with-icon"
                        disabled={busy !== ''}
                        onClick={() => void run(key, () => api.restoreTrash(it.kind, it.id), t('{name} is back in its folder. Use Import in the Library to add it to your library again.', { name: it.label }))}
                      >
                        <Icon name="refresh" size={14} /> {t('Put back')}
                      </button>
                      <button
                        className="btn-sm"
                        disabled={busy !== ''}
                        onClick={async () => {
                          if (!(await confirm({ title: t('Delete {name} for good?', { name: it.label }), body: <p>{t('The files are deleted now. This cannot be undone.')}</p>, confirmLabel: t('Delete'), danger: true }))) return
                          await run(key, () => api.deleteTrash(it.kind, it.id), t('Deleted for good.'))
                        }}
                      >
                        {t('Delete now')}
                      </button>
                    </td>
                  </tr>
                )
              })}
            </tbody>
          </table>
        </>
      )}
    </>
  )
}

function inDays(iso: string): string {
  const days = Math.ceil((new Date(iso).getTime() - Date.now()) / 86400000)
  if (days <= 0) return t('today')
  if (days === 1) return t('tomorrow')
  return plural(days, 'in {n} day', 'in {n} days')
}
