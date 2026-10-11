import { useCallback, useEffect, useState } from 'react'
import { api, type CloudBackup, type CloudBackupForm, type CloudCopy } from '../api'
import { DOCS_URL } from '../docs'
import { formatBytes, timeAgo } from '../format'
import Icon from './Icon'
import Switch from './Switch'
import TestButton from './TestButton'
import { useToast } from './Toast'
import { plural, t } from '../i18n'
import TipLabel from './TipLabel'

const blank: CloudBackupForm = {
  enabled: false,
  kind: 's3',
  endpoint: '',
  region: '',
  bucket: '',
  prefix: 'mediarium/',
  accessKey: '',
  secretKey: '',
  pathStyle: true,
  url: '',
  username: '',
  password: '',
  passphrase: '',
  keep: 14,
}

// Settings > System: a copy of each nightly backup in S3-style storage or a
// WebDAV folder, locked with a passphrase before it leaves the server. The
// page never gets the saved keys, password or passphrase back; empty fields
// keep them.
export default function CloudBackupCard() {
  const toast = useToast()
  const [saved, setSaved] = useState<CloudBackup | null>(null)
  const [form, setForm] = useState<CloudBackupForm>(blank)
  const [repeat, setRepeat] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [copies, setCopies] = useState<CloudCopy[] | null>(null)

  const load = useCallback(() => {
    api
      .cloudBackup()
      .then((c) => {
        setSaved(c)
        setForm({ ...blank, ...c, kind: c.kind || 's3', prefix: c.kind ? c.prefix : blank.prefix, pathStyle: c.kind ? c.pathStyle : blank.pathStyle, secretKey: '', password: '', passphrase: '' })
        setRepeat('')
      })
      .catch((e) => setError(e instanceof Error ? e.message : String(e)))
  }, [])
  useEffect(load, [load])

  const set = <K extends keyof CloudBackupForm>(k: K, val: CloudBackupForm[K]) => setForm((f) => ({ ...f, [k]: val }))

  function passphraseProblem(): string {
    if (!form.passphrase) return saved?.hasPassphrase ? '' : t('Choose a passphrase.')
    if ([...form.passphrase].length < 10) return t('The passphrase needs at least 10 characters.')
    if (form.passphrase !== repeat) return t("The two passphrases don't match.")
    return ''
  }

  async function save(enabled = form.enabled) {
    const problem = enabled ? passphraseProblem() : form.passphrase && form.passphrase !== repeat ? t("The two passphrases don't match.") : ''
    if (problem) {
      setError(problem)
      return
    }
    setBusy(true)
    setError('')
    try {
      const c = await api.putCloudBackup({ ...form, enabled })
      setSaved(c)
      setForm((f) => ({ ...f, enabled: c.enabled, secretKey: '', password: '', passphrase: '' }))
      setRepeat('')
      toast.success(c.enabled ? t('Saved. Each nightly backup is copied to the cloud.') : t('Saved. Cloud copies are off.'))
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
      if (enabled !== form.enabled) setForm((f) => ({ ...f, enabled: !enabled }))
    } finally {
      setBusy(false)
    }
  }

  async function test() {
    const problem = passphraseProblem()
    if (problem) return { ok: false, message: problem }
    await api.testCloudBackup(form)
    return { ok: true, message: t('Mediarium can save, list and delete files there.') }
  }

  async function copyNow() {
    setBusy(true)
    setError('')
    try {
      const st = await api.uploadCloudBackup()
      toast.success(t('Copied to the cloud ({size}).', { size: formatBytes(st.size) }))
      load()
      if (copies) showCopies()
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
      load()
    } finally {
      setBusy(false)
    }
  }

  function showCopies() {
    api
      .cloudBackupList()
      .then(setCopies)
      .catch((e) => setError(e instanceof Error ? e.message : String(e)))
  }

  const last = saved?.last
  const kept = saved?.hasSecretKey ? t('Saved. Leave empty to keep it') : ''
  return (
    <fieldset className="group">
      <legend>
        <Icon name="cloud" size={14} /> <TipLabel help="backups-updates#cloud-backups">{t('Cloud backups')}</TipLabel>
      </legend>
      <p style={{ color: 'var(--text-dim)', marginTop: 0 }}>
        {t("A copy of each nightly backup somewhere off this server, so a dead disk or a stolen NAS doesn't take your setup with it. Works with Backblaze B2, Wasabi, Cloudflare R2, Amazon S3, MinIO and other S3-style storage, or a WebDAV folder (Nextcloud, ownCloud, most NAS boxes). Only settings and the database are copied, never media. Each copy is locked with your passphrase before it leaves, so the storage company can't read it.")}{' '}
        <a href={`${DOCS_URL}/INSTALL.md#cloud-backups`} target="_blank" rel="noreferrer">
          {t('How to set it up')}
        </a>
      </p>
      {saved && (
        <Switch
          checked={form.enabled}
          onChange={(on) => {
            set('enabled', on)
            void save(on)
          }}
          label={t('Copy the nightly backup to the cloud')}
          description={saved.enabled ? t('The newest {n} copies are kept there.', { n: saved.keep }) : t('Fill in where the copies go and a passphrase, then switch this on.')}
          showState
          disabled={busy}
        />
      )}
      <div className="grid-form" style={{ marginTop: 14, gridTemplateColumns: 'repeat(auto-fit, minmax(min(100%, 210px), 1fr))' }}>
        <label>
          {t('Where')}
          <select value={form.kind} onChange={(e) => set('kind', e.target.value as CloudBackupForm['kind'])}>
            <option value="s3">{t('S3-style storage (B2, Wasabi, R2, S3, MinIO)')}</option>
            <option value="webdav">{t('WebDAV folder (Nextcloud, NAS)')}</option>
          </select>
        </label>
        <label>
          {t('Keep the newest')}
          <select value={form.keep} onChange={(e) => set('keep', Number(e.target.value))}>
            {[...new Set([7, 14, 30, 60, 90, form.keep])].sort((a, b) => a - b).map((n) => (
              <option key={n} value={n}>
                {plural(n, '{n} copy', '{n} copies')}
              </option>
            ))}
          </select>
        </label>
        {form.kind === 's3' ? (
          <>
            <label>
              {t('Endpoint')}
              <input value={form.endpoint} onChange={(e) => set('endpoint', e.target.value)} placeholder="https://s3.eu-central-003.backblazeb2.com" autoComplete="off" spellCheck={false} />
            </label>
            <label>
              <TipLabel text={t('The region name of your storage, shown at your provider. Leave it empty for us-east-1, or type auto for Cloudflare R2.')}>{t('Region')}</TipLabel>
              <input value={form.region} onChange={(e) => set('region', e.target.value)} placeholder={t('eu-central-003 (empty for us-east-1)')} autoComplete="off" spellCheck={false} />
            </label>
            <label>
              <TipLabel text={t('The container you made at your storage provider to hold the copies. It must already exist.')}>{t('Bucket')}</TipLabel>
              <input value={form.bucket} onChange={(e) => set('bucket', e.target.value)} placeholder="my-backups" autoComplete="off" spellCheck={false} />
            </label>
            <label>
              {t('Folder in the bucket')}
              <input value={form.prefix} onChange={(e) => set('prefix', e.target.value)} placeholder="mediarium/" autoComplete="off" spellCheck={false} />
            </label>
            <label>
              <TipLabel text={t('Made at your storage provider, with permission to write to the bucket. The secret key that goes with it is stored encrypted.')}>{t('Access key (key ID)')}</TipLabel>
              <input value={form.accessKey} onChange={(e) => set('accessKey', e.target.value)} autoComplete="off" spellCheck={false} />
            </label>
            <label>
              {t('Secret key')}
              <input type="password" value={form.secretKey} onChange={(e) => set('secretKey', e.target.value)} placeholder={kept} autoComplete="new-password" />
            </label>
            <label className="check-hint">
              <input type="checkbox" checked={form.pathStyle} onChange={(e) => set('pathStyle', e.target.checked)} />
              <span>
                {t('Bucket name in the path')}
                <small>{t('Leave this on for anything but Amazon S3 itself.')}</small>
              </span>
            </label>
          </>
        ) : (
          <>
            <label>
              {t('Folder address')}
              <input value={form.url} onChange={(e) => set('url', e.target.value)} placeholder="https://cloud.example.com/remote.php/dav/files/you/Mediarium/" autoComplete="off" spellCheck={false} />
            </label>
            <label>
              {t('User name')}
              <input value={form.username} onChange={(e) => set('username', e.target.value)} autoComplete="off" spellCheck={false} />
            </label>
            <label>
              {t('Password')}
              <input type="password" value={form.password} onChange={(e) => set('password', e.target.value)} placeholder={saved?.hasPassword ? t('Saved. Leave empty to keep it') : t('An app password if your server has them')} autoComplete="new-password" />
            </label>
          </>
        )}
        <label>
          <TipLabel text={t('Locks each copy on your server before it leaves. Write it down somewhere else: without it nobody can open a cloud copy, you included.')}>{saved?.hasPassphrase ? t('New passphrase') : t('Passphrase')}</TipLabel>
          <input type="password" value={form.passphrase} onChange={(e) => set('passphrase', e.target.value)} placeholder={saved?.hasPassphrase ? t('Saved. Leave empty to keep it') : t('At least 10 characters')} autoComplete="new-password" />
        </label>
        <label>
          {t('Passphrase again')}
          <input type="password" value={repeat} onChange={(e) => setRepeat(e.target.value)} disabled={!form.passphrase} autoComplete="new-password" />
        </label>
        <div className="notice notice-info">
          {t('Write the passphrase down somewhere safe, away from this server. You need it to restore a cloud copy, and without it nobody can open one, not even you. Changing it only affects copies made from now on.')}
        </div>
        <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap', alignItems: 'center' }}>
          <button className="primary" disabled={busy} onClick={() => void save()}>
            {t('Save')}
          </button>
          <TestButton label={t('Test')} run={test} disabled={busy} />
          <button className="btn-with-icon" disabled={busy || !saved?.hasPassphrase} onClick={() => void copyNow()}>
            <Icon name="cloud" size={15} /> {busy ? t('Working…') : t('Copy a backup now')}
          </button>
        </div>
        {error && <p className="error-text">{error}</p>}
      </div>
      {last?.at && (
        <p className="field-hint" style={{ marginTop: 12 }}>
          {last.problem ? t('The last copy {when} failed: {problem}', { when: timeAgo(last.at), problem: last.problem }) : t('Last copied {when} ({size}).', { when: timeAgo(last.at), size: formatBytes(last.size ?? 0) })}{' '}
          {copies === null && (
            <button className="btn-sm" onClick={showCopies}>
              {t("Show what's there")}
            </button>
          )}
        </p>
      )}
      {copies && (
        <>
          <p className="field-hint">
            {copies.length ? plural(copies.length, '{n} copy in the cloud.', '{n} copies in the cloud.') : t('No copies in the cloud yet.')} {t('To restore one, download it from your storage and choose it under Restore with the passphrase.')}
          </p>
          {copies.length > 0 && (
            <ul className="file-list">
              {copies.map((c) => (
                <li key={c.name}>
                  <span className="file-ico">
                    <Icon name="cloud" size={16} />
                  </span>
                  <div className="file-name">
                    <span>{c.name}</span>
                    <small>{formatBytes(c.size)}</small>
                  </div>
                </li>
              ))}
            </ul>
          )}
        </>
      )}
    </fieldset>
  )
}
