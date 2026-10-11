import { useCallback, useEffect, useState, type ReactNode } from 'react'
import { api, ApiError, type CleanupReport } from '../api'
import { formatBytes, timeAgo } from '../format'
import { firstError, numberRange } from '../validate'
import { useConfirm } from './ConfirmProvider'
import Icon from './Icon'
import Loading from './Loading'
import Switch from './Switch'
import { useToast } from './Toast'
import { plural, t, tx } from '../i18n'
import TipLabel from './TipLabel'

const REASON: Record<string, string> = {
  orphaned: t('Not part of any download'),
  'imported-leftover': t('Left over after importing'),
  'seeding-finished': t('Torrent finished seeding'),
  'empty-folder': t('Empty folder'),
}

// A number of days, saved with its own button, because a typo in a number
// box should not save halfway through. Used for how long history is kept and
// how long the recycle bin keeps removed files. sentence puts the number box
// inside its sentence, so each language can word it.
function DaysField({ days, sentence, hint, max, blankError, save: store, onSaved }: { days: number; sentence: (box: ReactNode) => ReactNode; hint: string; max: number; blankError: string; save: (n: number) => Promise<string>; onSaved: () => void }) {
  const toast = useToast()
  const [text, setText] = useState(String(days))
  const [saving, setSaving] = useState(false)
  useEffect(() => setText(String(days)), [days])
  const error = firstError(text.trim() === '' && blankError, numberRange(text, t('The number of days'), { min: 0, max }))
  const changed = text.trim() !== String(days)

  async function save() {
    if (error) return
    setSaving(true)
    try {
      toast.success(await store(Number(text)))
      onSaved()
    } catch (e) {
      toast.error(t('Not saved: {error}', { error: e instanceof Error ? e.message : String(e) }))
    } finally {
      setSaving(false)
    }
  }

  return (
    <div style={{ marginTop: 12 }}>
      <label style={{ display: 'flex', flexWrap: 'wrap', alignItems: 'center', gap: 8 }}>
        {sentence(
          <input
            inputMode="numeric"
            value={text}
            onChange={(e) => setText(e.target.value)}
            onKeyDown={(e) => e.key === 'Enter' && changed && void save()}
            aria-invalid={error ? true : undefined}
            style={{ width: 90 }}
          />,
        )}
        <button onClick={() => void save()} disabled={saving || !changed || !!error}>
          {saving ? t('Saving…') : t('Save')}
        </button>
      </label>
      <small style={{ color: error ? 'var(--danger)' : 'var(--text-dim)' }}>{error ?? hint}</small>
    </div>
  )
}

// System → Clean up: what can be removed from the downloads area (never the
// library), with a button to do it now and a switch to do it daily.
export default function CleanupCard() {
  const toast = useToast()
  const confirm = useConfirm()
  const [report, setReport] = useState<CleanupReport | null>(null)
  const [missing, setMissing] = useState(false)
  const [running, setRunning] = useState(false)

  const load = useCallback(() => {
    api
      .cleanupReport()
      .then(setReport)
      .catch((e) => {
        if (e instanceof ApiError && e.status === 404) setMissing(true)
      })
  }, [])
  useEffect(load, [load])

  if (missing) return null

  async function runNow() {
    if (!report) return
    if (
      !(await confirm({
        title: t('Free {size}?', { size: formatBytes(report.reclaimableBytes) }),
        body: <p>{plural(report.items.length, 'Removes the {n} item listed from the downloads folder. Nothing in your movie or TV library is touched.', 'Removes the {n} items listed from the downloads folder. Nothing in your movie or TV library is touched.')}</p>,
        confirmLabel: t('Clean up now'),
        danger: true,
      }))
    )
      return
    setRunning(true)
    try {
      const r = await api.runCleanup()
      toast.success(t('Cleaned up: {size} freed.', { size: formatBytes(r.removedBytes ?? 0) }))
      load()
    } catch (e) {
      toast.error(e instanceof Error ? e.message : String(e))
    } finally {
      setRunning(false)
    }
  }

  async function setAuto(auto: boolean) {
    try {
      await api.putSettings({ cleanupAuto: auto })
      toast.success(auto ? t('Daily clean-up is on.') : t('Daily clean-up is off.'))
      load()
    } catch (e) {
      toast.error(e instanceof Error ? e.message : String(e))
    }
  }

  return (
    <fieldset className="group usenet span-all">
      <legend>
        <Icon name="trash" size={14} /> <TipLabel help="downloads#clean-up">{t('Clean up')}</TipLabel>
      </legend>
      <p style={{ marginTop: 0 }}>
        {t('Leftovers in the downloads folder: files from failed or finished downloads, torrents that are done seeding, and empty folders. Your movie and TV library is never touched.')}
      </p>
      {report === null ? (
        <Loading height={80} />
      ) : (
        <>
          <div className="cleanup-head">
            <div>
              {report.reclaimableBytes > 0 ? (
                tx('{size} can be freed', { size: <strong className="cleanup-size">{formatBytes(report.reclaimableBytes)}</strong> })
              ) : (
                <strong className="cleanup-size">{t('Nothing to clean up right now')}</strong>
              )}
              {report.lastRunAt && <small> · {t('last cleaned {when}', { when: timeAgo(report.lastRunAt) })}</small>}
            </div>
            <button className="primary btn-with-icon" onClick={() => void runNow()} disabled={running || report.items.length === 0}>
              <Icon name="trash" size={15} /> {running ? t('Cleaning…') : t('Clean up now')}
            </button>
          </div>
          {report.items.length > 0 && (
            <ul className="file-list cleanup-list">
              {report.items.slice(0, 50).map((it) => (
                <li key={it.path}>
                  <span className="file-ico">
                    <Icon name="folder" size={16} />
                  </span>
                  <div className="file-name">
                    <span title={it.path}>{it.path}</span>
                    <small>
                      {REASON[it.reason] ?? it.reason} · {formatBytes(it.sizeBytes)}
                    </small>
                  </div>
                </li>
              ))}
            </ul>
          )}
          <DaysField
            days={report.historyRetentionDays ?? 90}
            sentence={(box) => tx('Keep finished downloads and activity for {box} days', { box })}
            hint={t('Use 0 to keep everything. Older entries are removed by the daily clean-up.')}
            max={36500}
            blankError={t('Enter a number of days, or 0 to keep everything.')}
            save={async (n) => {
              const saved = await api.putSettings({ historyRetentionDays: n })
              const d = saved.historyRetentionDays ?? n
              return d === 0 ? t('Saved: finished downloads and activity are kept forever.') : plural(d, 'Saved: finished downloads and activity are kept for {n} day.', 'Saved: finished downloads and activity are kept for {n} days.')
            }}
            onSaved={load}
          />
          <DaysField
            days={report.trashDays ?? 7}
            sentence={(box) => tx("Keep removed titles' files in the recycle bin for {box} days", { box })}
            hint={t('When you remove a title with its files, they wait in Activity > Recycle bin this long. Use 0 to delete them straight away.')}
            max={365}
            blankError={t('Enter a number of days, or 0 to delete them straight away.')}
            save={async (n) => {
              const saved = await api.putSettings({ trashDays: n })
              const d = saved.trashDays ?? n
              return d === 0 ? t('Saved: removed files are deleted straight away.') : plural(d, 'Saved: removed files stay in the recycle bin for {n} day.', 'Saved: removed files stay in the recycle bin for {n} days.')
            }}
            onSaved={load}
          />
          <div style={{ marginTop: 12 }}>
            <Switch checked={report.auto} onChange={(v) => void setAuto(v)} label={t('Clean up automatically once a day')} description={t('Removes leftovers like the ones above and skips anything still downloading or seeding.')} showState />
          </div>
        </>
      )}
    </fieldset>
  )
}
