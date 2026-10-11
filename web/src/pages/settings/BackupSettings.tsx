import { useRef, useState } from 'react'
import BackupCard from '../../components/BackupCard'
import CloudBackupCard from '../../components/CloudBackupCard'
import Icon from '../../components/Icon'
import { useToast } from '../../components/Toast'
import { useConfirm } from '../../components/ConfirmProvider'
import { FieldError, useValidation } from '../../useValidation'
import { t, tx } from '../../i18n'
import { withBase } from '../../base'
import TipLabel from '../../components/TipLabel'

// Saved copies of everything, the cloud copies, and putting one back.
export default function BackupSettings() {
  const confirm = useConfirm()
  const toast = useToast()
  const [file, setFile] = useState<File | null>(null)
  const [passphrase, setPassphrase] = useState('')
  const [askPassphrase, setAskPassphrase] = useState(false)
  const [restoring, setRestoring] = useState(false)
  const [waiting, setWaiting] = useState(false)
  const input = useRef<HTMLInputElement>(null)
  const errors = {
    file: file
      ? /\.zip(\.enc)?$/i.test(file.name)
        ? null
        : t("That isn't a Mediarium backup. Choose the .zip you got from Download a backup, or a .zip.enc cloud copy.")
      : null,
  }
  const v = useValidation(errors)

  async function restore() {
    if (!file || !v.attempt()) return
    if (!(await confirm({ title: t('Restore this backup?'), body: <p>{t('It replaces your library, accounts and settings with the ones in the file. Your current data goes into a "before-restore" folder in your config folder, and Mediarium restarts.')}</p>, confirmLabel: t('Restore and restart'), danger: true }))) return
    setRestoring(true)
    try {
      const body = new FormData()
      if (passphrase) body.append('passphrase', passphrase)
      body.append('file', file)
      const res = await fetch(withBase('/api/system/restore'), { method: 'POST', body, credentials: 'include' })
      const text = await res.text()
      let data: { error?: string; needPassphrase?: boolean } | null = null
      try {
        data = text ? JSON.parse(text) : null
      } catch {
        // A proxy in front can answer with a web page instead of JSON.
      }
      if (data?.needPassphrase) setAskPassphrase(true)
      if (!res.ok) throw new Error(data?.error ?? t('The restore failed (error {status}). If the backup is big, make sure any proxy in front of Mediarium allows large uploads.', { status: String(res.status) }))
      toast.success(t('Backup accepted. Restarting…'))
      setWaiting(true)
      // Wait for the app to come back, then reload onto the restored data.
      const started = Date.now()
      const poll = setInterval(async () => {
        try {
          const r = await fetch(withBase('/api/version'), { cache: 'no-store' })
          if (r.ok && Date.now() - started > 3000) {
            clearInterval(poll)
            window.location.href = withBase('/')
          }
        } catch {
          // still restarting
        }
        if (Date.now() - started > 90000) {
          clearInterval(poll)
          setWaiting(false)
          setRestoring(false)
          toast.info(t("Mediarium didn't come back. Start it again and reload this page."))
        }
      }, 2000)
    } catch (e) {
      toast.error(e instanceof Error ? e.message : String(e))
      setRestoring(false)
    }
  }

  return (
    <div className="settings-stack">
      <BackupCard />
      <CloudBackupCard />

      <fieldset className="group alerts">
        <legend>
          <Icon name="refresh" size={14} /> <TipLabel help="backups-updates#backup-and-restore">{t('Restore')}</TipLabel>
        </legend>
        <p style={{ marginTop: 0 }}>
          {tx('Choose a backup file, or a cloud copy ({ext}) with its passphrase. Mediarium restarts to load it, and your current data moves to a {folder} folder in your config folder.', {
            ext: <code>.zip.enc</code>,
            folder: <code>before-restore</code>,
          })}
        </p>
        <div className="restore-row">
          <input ref={input} type="file" accept=".zip,.enc,application/zip" aria-label={t('Backup file to restore')} onChange={(e) => setFile(e.target.files?.[0] ?? null)} {...v.bind('file')} />
          {(askPassphrase || /\.enc$/i.test(file?.name ?? '')) && (
            <input type="password" value={passphrase} onChange={(e) => setPassphrase(e.target.value)} placeholder={t('Passphrase of the cloud copy')} aria-label={t('Passphrase of the cloud copy')} autoComplete="off" style={{ maxWidth: 280 }} />
          )}
          <button className="danger-ghost btn-with-icon" onClick={() => void restore()} disabled={!file || restoring}>
            <Icon name="refresh" size={15} /> {waiting ? t('Restarting…') : restoring ? t('Uploading…') : t('Restore this backup')}
          </button>
        </div>
        <FieldError v={v} name="file" />
      </fieldset>
    </div>
  )
}
