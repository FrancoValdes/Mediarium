import { useEffect, useState } from 'react'
import { api } from '../api'
import Switch from './Switch'
import { useAutosaveSetting } from '../useAutosave'
import { plural, t, tx } from '../i18n'
import TipLabel from './TipLabel'

// How far back Usenet providers keep posts. Most keep between 1,000 and 5,000
// days; the list has the usual round numbers, and shows a saved number that is
// not on it.
const RETENTION_CHOICES = [0, 365, 730, 1000, 1500, 2000, 3000, 4000, 5000]

// How far back the Usenet provider keeps posts. Lives on the Usenet page.
export function UsenetRetentionCard() {
  const retention = useAutosaveSetting<number>(
    (s) => s.usenetRetentionDays ?? 0,
    (v) => ({ usenetRetentionDays: v }),
    (v) => (v === 0 ? t('Saved: Usenet posts are taken whatever their age.') : t('Saved: Usenet posts older than {n} days are left out.', { n: v })),
    0,
  )
  const busy = !retention.loaded || retention.saving
  const choices = RETENTION_CHOICES.includes(retention.value) ? RETENTION_CHOICES : [...RETENTION_CHOICES, retention.value].sort((a, b) => a - b)
  return (
    <section className="card">
      <h2><TipLabel help="downloads#usenet-retention">{t('How old a Usenet post may be')}</TipLabel></h2>
      <label className="inline-field">
        {tx('Leave out Usenet posts older than {days}', {
          days: (
            <select value={retention.value} disabled={busy} onChange={(e) => void retention.change(Number(e.target.value))}>
              {choices.map((n) => (
                <option key={n} value={n}>
                  {n === 0 ? t('no limit') : plural(n, '{n} day', '{n} days')}
                </option>
              ))}
            </select>
          ),
        })}
      </label>
      <p className="field-hint">
        {t("Set this to how many days your Usenet provider keeps posts (it's on the provider's site). Older posts are skipped by the automatic searches and marked in the list of releases, so Mediarium doesn't download something that is already gone. Torrents are not affected.")}
      </p>
    </section>
  )
}

// What Mediarium checks on a release before it downloads it, and what it does
// with one it has seen before. Every change saves by itself.
export default function DownloadChecksCard() {
  const executables = useAutosaveSetting<boolean>(
    (s) => s.rejectExecutables !== false,
    (v) => ({ rejectExecutables: v }),
    (v) => (v ? t('Saved: releases with programs or scripts in them are rejected.') : t('Saved: releases with programs or scripts in them are no longer rejected.')),
    true,
  )
  const duplicates = useAutosaveSetting<boolean>(
    (s) => s.skipDuplicates === true,
    (v) => ({ skipDuplicates: v }),
    (v) => (v ? t('Saved: a release that is already downloaded or in the queue is skipped.') : t('Saved: duplicate downloads are no longer skipped.')),
    false,
  )
  const precheck = useAutosaveSetting<boolean>(
    (s) => s.usenetPrecheck === true,
    (v) => ({ usenetPrecheck: v }),
    (v) => (v ? t('Saved: the articles are checked on your Usenet servers before a download starts.') : t('Saved: downloads start without checking the articles first.')),
    false,
  )
  const [extensions, setExtensions] = useState<string[]>([])
  useEffect(() => {
    api.getSettings().then((s) => setExtensions(s.unsafeExtensions ?? [])).catch(() => undefined)
  }, [])
  const busy = executables.saving || duplicates.saving || precheck.saving
  return (
    <div>
      <Switch
        checked={executables.value}
        onChange={(v) => void executables.change(v)}
        disabled={!executables.loaded || busy}
        label={t('Reject releases that contain programs or scripts')}
        tip={t('Releases that hide a .exe, .bat, .sh or similar file next to the video are refused and blocklisted, before and after unpacking. Nothing is ever run.')}
        learnMore="downloads#programs-and-scripts"
        description={
          extensions.length > 0
            ? t('Fake releases hide a program next to the video. A release whose file list shows one is rejected before it is downloaded, and a download is checked again after it is unpacked. The release goes on the blocklist and Mediarium looks for another one. Looks for: {list}.', { list: extensions.join(' ') })
            : t('Fake releases hide a program next to the video. A release whose file list shows one is rejected before it is downloaded, and a download is checked again after it is unpacked. The release goes on the blocklist and Mediarium looks for another one.')
        }
        showState
      />
      <Switch
        checked={duplicates.value}
        onChange={(v) => void duplicates.change(v)}
        disabled={!duplicates.loaded || busy}
        label={t('Skip a release that was already downloaded')}
        tip={t('Skips a download when the same NZB, or a release with the same name and size, is already in the queue or finished for that title.')}
        learnMore="downloads#skipping-duplicates"
        description={t("Does not download the same release twice for the same movie or episode. It counts as the same when the NZB holds the same articles, or the release has the same name and size, as a download that is in the queue or finished. A download that failed doesn't count, and neither does one whose file you deleted. Activity says when something was skipped.")}
        showState
      />
      <Switch
          checked={precheck.value}
          onChange={(v) => void precheck.change(v)}
          disabled={!precheck.loaded || busy}
          label={t('Check that the articles exist before downloading')}
          tip={t('Dead Usenet releases are dropped in seconds instead of after an hour of downloading. It sends a few requests per release, which some providers count.')}
          learnMore="downloads#checking-the-articles-first"
          description={t("Asks your Usenet servers about a sample of the articles first and drops a release that has too many missing for its PAR2 files to repair, before an hour of downloading is wasted. It sends a few requests, which some providers don't like.")}
          showState
        />
    </div>
  )
}
