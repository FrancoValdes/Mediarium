import { useEffect, useMemo, useState, type ReactNode } from 'react'
import { Link } from 'react-router-dom'
import { api, type DownloadsStatus, type QueueItem } from '../api'
import { formatBytes } from '../format'
import { plural, t } from '../i18n'
import { RUNNING, sortQueue } from '../queueView'
import { useLive } from '../useLive'
import { useUrlState } from '../useUrlState'
import Icon, { type IconName } from './Icon'
import HelpLink from './HelpLink'
import { useToast } from './Toast'
import TorrentOptionsPanel from './TorrentOptionsPanel'

type Kind = 'usenet' | 'torrent'

type Show = 'both' | 'usenet' | 'torrent'
const SHOW_KEY = 'mediarium-clients-view'

// The view last chosen on this device, until the address says otherwise.
function savedShow(): Show {
  try {
    const v = localStorage.getItem(SHOW_KEY)
    return v === 'usenet' || v === 'torrent' ? v : 'both'
  } catch {
    return 'both'
  }
}

// Activity > Download clients: the Usenet downloader and the torrent client
// that are part of Mediarium, as two cards you can show side by side or one at
// a time. They look and work like the external clients: what each is doing
// right now, with the same controls on every download. Children (the clients
// you connected and the debrid account) go in the torrent card, since that is
// where torrents end up.
export default function BuiltInClients({ items, renderRows, onChanged, children }: { items: QueueItem[]; renderRows: (list: QueueItem[]) => ReactNode; onChanged: () => void | Promise<void>; children?: ReactNode }) {
  const [status, setStatus] = useState<DownloadsStatus | null>(null)
  useEffect(() => {
    api.downloadsStatus().then(setStatus).catch(() => undefined)
  }, [])
  useLive(() => api.downloadsStatus().then(setStatus).catch(() => undefined), 10000)
  const [ui, patch] = useUrlState({ show: savedShow() as string })
  const show: Show = ui.show === 'usenet' || ui.show === 'torrent' ? ui.show : 'both'
  const choose = (v: Show) => {
    patch({ show: v })
    try {
      localStorage.setItem(SHOW_KEY, v)
    } catch {
      /* the choice just isn't remembered */
    }
  }
  const views: [Show, string][] = [
    ['both', t('Side by side')],
    ['usenet', t('Usenet')],
    ['torrent', t('Torrents')],
  ]
  return (
    <div className="download-clients">
      <div className="seg client-switch" role="group" aria-label={t('Which downloaders to show')}>
        {views.map(([id, label]) => (
          <button key={id} className={show === id ? 'active' : ''} aria-pressed={show === id} onClick={() => choose(id)}>
            {label}
          </button>
        ))}
      </div>
      <div className={`client-columns show-${show}`}>
        {show !== 'torrent' && (
          <div className="client-card">
            <ClientSection kind="usenet" status={status} items={items} renderRows={renderRows} onChanged={onChanged} />
          </div>
        )}
        {show !== 'usenet' && (
          <div className="client-card">
            <ClientSection kind="torrent" status={status} items={items} renderRows={renderRows} onChanged={onChanged} />
            {children}
          </div>
        )}
      </div>
    </div>
  )
}

function ClientSection({ kind, status, items, renderRows, onChanged }: { kind: Kind; status: DownloadsStatus | null; items: QueueItem[]; renderRows: (list: QueueItem[]) => ReactNode; onChanged: () => void | Promise<void> }) {
  const toast = useToast()
  const [busy, setBusy] = useState<'pause' | 'resume' | null>(null)
  // What is open in this downloader: running, waiting, paused, failed or stopped. Finished ones are in History.
  // When torrents go to a connected client or a debrid service, they show in that one's section instead.
  const elsewhere = kind === 'torrent' && !!status && status.torrent.via !== undefined && status.torrent.via !== 'builtin'
  const mine = useMemo(() => (elsewhere ? [] : sortQueue(items.filter((q) => q.protocol === kind && q.status !== 'completed'))), [items, kind, elsewhere])
  const running = mine.filter((q) => q.status === 'downloading' || q.status === 'importing').length
  const inLine = mine.filter((q) => q.status === 'queued').length
  const paused = mine.filter((q) => q.status === 'paused').length
  const problems = mine.filter((q) => q.status === 'failed' || q.status === 'stopped' || q.status === 'conflict').length
  const toGo = mine.filter((q) => q.status === 'downloading').reduce((sum, q) => sum + (q.sizeBytes * (100 - q.progressPct)) / 100, 0)
  const canPause = mine.some((q) => RUNNING.has(q.status) && !q.pending)

  const usenet = kind === 'usenet'
  const icon: IconName = usenet ? 'server' : 'magnet'
  const name = usenet ? t('Usenet downloader') : t('Torrent client')
  let state: { text: string; tone: 'downloaded' | 'missing' | '' } | null = null
  let note: ReactNode = null
  if (status) {
    if (usenet) {
      state = status.usenet.ready ? { text: t('ready'), tone: 'downloaded' } : { text: t('needs your provider'), tone: 'missing' }
      if (!status.usenet.ready) note = <>{t("It only needs your Usenet provider's login.")} <Link to="/settings/usenet">{t('Add it in Settings')}</Link></>
    } else if (elsewhere) {
      state = { text: t('not in use'), tone: '' }
      note = t('Torrents go to {name} while it is connected, so they show in its section below.', { name: status.torrent.viaLabel ?? '' })
    } else if (status.torrent.state === 'disabled' || status.torrent.enabled === false) {
      state = { text: t('off'), tone: '' }
      note = <>{t('The torrent client is switched off.')} <Link to="/settings/torrents">{t('Turn it on in Settings')}</Link></>
    } else if (!status.torrent.ready) {
      state = { text: t('waiting for the VPN'), tone: 'missing' }
      note = <>{t('Torrents wait until the VPN is connected.')} <Link to="/settings/vpn">{t('VPN settings')}</Link></>
    } else {
      state = { text: t('ready'), tone: 'downloaded' }
    }
  }

  async function pauseAll() {
    setBusy('pause')
    try {
      const r = await api.pauseAllQueue(kind)
      toast.success(r.paused === 0 ? t('Nothing was running.') : plural(r.paused, 'Paused {n} download.', 'Paused {n} downloads.'))
      await onChanged()
    } catch (e) {
      toast.error(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(null)
    }
  }

  async function resumeAll() {
    setBusy('resume')
    try {
      const r = await api.resumeAllQueue(kind)
      if (r.skipped > 0) toast.error(`${t('{resumed} resumed, {skipped} could not start.', { resumed: r.resumed, skipped: r.skipped })} ${r.problem ?? ''}`.trim())
      else toast.success(r.resumed === 0 ? t('Nothing was paused.') : plural(r.resumed, 'Resumed {n} download.', 'Resumed {n} downloads.'))
      await onChanged()
    } catch (e) {
      toast.error(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(null)
    }
  }

  const summary = [
    running > 0 ? t('{n} downloading', { n: running }) : '',
    inLine > 0 ? t('{n} waiting in line', { n: inLine }) : '',
    paused > 0 ? t('{n} paused', { n: paused }) : '',
    problems > 0 ? t('{n} with a problem', { n: problems }) : '',
    toGo > 0 ? t('{size} to go', { size: formatBytes(toGo) }) : '',
  ].filter(Boolean)

  return (
    <section className="builtin-client">
      <div className="rail-head">
        <h2 className="client-title">
          <Icon name={icon} size={18} /> {name}
          <HelpLink
            topic="activity#download-clients"
            text={
              usenet
                ? t("The Usenet downloader built into Mediarium. All it needs is your Usenet provider's login. Pause all and Resume all here only touch Usenet downloads.")
                : t('The torrent client built into Mediarium. If you connect qBittorrent, RDT-Client, Decypharr or a debrid account, torrents go there instead and show in that section. Pause all and Resume all here only touch torrents.')
            }
          />
        </h2>
        <span className="badge downloaded">{t('built in')}</span>
        {state && <span className={`badge ${state.tone}`}>{state.text}</span>}
        {!elsewhere && <span className="client-summary">{summary.length > 0 ? summary.join(' · ') : t('Nothing downloading')}</span>}
        {(canPause || paused > 0) && (
          <div className="client-bulk">
            {canPause && (
              <button className="btn-sm btn-with-icon" disabled={busy !== null} onClick={() => void pauseAll()}>
                <Icon name="pause" size={15} /> {busy === 'pause' ? t('Pausing…') : t('Pause all')}
              </button>
            )}
            {paused > 0 && (
              <button className="btn-sm btn-with-icon" disabled={busy !== null} onClick={() => void resumeAll()}>
                <Icon name="play" size={15} /> {busy === 'resume' ? t('Resuming…') : t('Resume all')}
              </button>
            )}
          </div>
        )}
      </div>
      {note && <p className="hint">{note}</p>}
      {mine.length === 0 && !note && <p className="hint">{usenet ? t('Nothing is downloading from Usenet right now.') : t('Nothing is downloading as a torrent right now.')}</p>}
      {renderRows(mine)}
      {kind === 'torrent' && !elsewhere && !!status && status.torrent.state !== 'disabled' && <TorrentOptionsPanel listenPort={status.torrent.listenPort} />}
    </section>
  )
}
