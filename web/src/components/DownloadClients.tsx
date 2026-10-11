import { useCallback, useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { api, type ClientTorrent, type ClientTorrentsAnswer, type DebridTorrent, type DebridTorrentsAnswer } from '../api'
import { formatBytes, timeAgo } from '../format'
import { t } from '../i18n'
import { etaText } from '../transferRate'
import { useLive } from '../useLive'
import ActionMenu from './ActionMenu'
import { useConfirm } from './ConfirmProvider'
import Icon from './Icon'
import { useToast } from './Toast'

// A torrent that has moved no data for this long is shown with a warning.
const IDLE_WARN_SECONDS = 30 * 60

// Activity > Download clients, below the built-in ones: what is in the
// external torrent client (qBittorrent, RDT-Client, Decypharr) and on the
// debrid account, with the controls next to each. Administrators only.
export default function DownloadClients() {
  const [client, setClient] = useState<ClientTorrentsAnswer | null>(null)
  const [debrid, setDebrid] = useState<DebridTorrentsAnswer | null>(null)
  const [scope, setScope] = useState<'mine' | 'all'>('mine')
  const [failed, setFailed] = useState('')

  const load = useCallback(() => {
    api.clientTorrents(scope).then((a) => {
      setClient(a)
      setFailed('')
    }).catch((e) => setFailed(e instanceof Error ? e.message : String(e)))
    api.debridTorrents().then(setDebrid).catch(() => undefined)
  }, [scope])
  useEffect(load, [load])
  useLive(load, 4000)

  if (failed) return <p className="error-text">{failed}</p>
  if (!client || !debrid) return <div className="skeleton" style={{ height: 160, borderRadius: 22 }} />
  if (!client.configured && !debrid.configured) {
    return (
      <p className="hint">
        {t('Use qBittorrent, RDT-Client, Decypharr or a debrid service as well? Connect it and it shows up here under the built-in ones.')}{' '}
        <Link to="/settings/torrents">{t('Set it up in Settings')}</Link>
      </p>
    )
  }
  return (
    <div className="download-clients">
      {client.configured && <ClientSection data={client} scope={scope} setScope={setScope} reload={load} />}
      {debrid.configured && <DebridSection data={debrid} reload={load} />}
    </div>
  )
}

function ClientSection({ data, scope, setScope, reload }: { data: ClientTorrentsAnswer; scope: 'mine' | 'all'; setScope: (s: 'mine' | 'all') => void; reload: () => void }) {
  const label = data.label ?? t('The download client')
  return (
    <section style={{ marginBottom: 28 }}>
      <div className="rail-head">
        <h2>{label}</h2>
        <span>{data.torrents.length === 1 ? t('1 torrent') : t('{n} torrents', { n: data.torrents.length })}</span>
        <div className="seg" role="group" aria-label={t('Which torrents to show')}>
          <button className={scope === 'mine' ? 'active' : ''} onClick={() => setScope('mine')} title={t('Only the torrents Mediarium added, the ones in the category "{name}"', { name: data.category ?? '' })}>
            {t('Added by Mediarium')}
          </button>
          <button className={scope === 'all' ? 'active' : ''} onClick={() => setScope('all')}>
            {t('Everything in {name}', { name: label })}
          </button>
        </div>
      </div>
      {data.error && <p className="error-text">{data.error}</p>}
      {!data.error && data.torrents.length === 0 && <p className="hint">{scope === 'mine' ? t('Nothing from Mediarium is in {name} right now.', { name: label }) : t('{name} has no torrents.', { name: label })}</p>}
      {data.torrents.map((tor) => (
        <TorrentRow key={tor.hash} tor={tor} label={label} reload={reload} />
      ))}
    </section>
  )
}

function TorrentRow({ tor, label, reload }: { tor: ClientTorrent; label: string; reload: () => void }) {
  const toast = useToast()
  const confirm = useConfirm()
  const [busy, setBusy] = useState(false)
  const active = tor.stage === 'downloading' || tor.stage === 'waiting' || tor.stage === 'checking'
  const stalled = (tor.stage === 'waiting' || tor.stage === 'downloading') && tor.idleSeconds >= IDLE_WARN_SECONDS

  async function run(action: 'pause' | 'resume' | 'recheck' | 'remove' | 'remove-files', done: string) {
    setBusy(true)
    try {
      await api.clientTorrentAction(tor.hash, action)
      toast.success(done)
      reload()
    } catch (e) {
      toast.error(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(false)
    }
  }

  async function remove(files: boolean) {
    const yes = await confirm({
      title: files ? t('Remove {name} and its files?', { name: tor.name }) : t('Remove {name}?', { name: tor.name }),
      body: <p>{tor.mine ? t('Mediarium put this one in {client}. Removing it there cancels that download; use "Give up and search again" if you want another release.', { client: label }) : files ? t('The torrent and the files it downloaded are deleted from {client}.', { client: label }) : t('The torrent is removed from {client}. The files stay on the disk.', { client: label })}</p>,
      confirmLabel: files ? t('Remove and delete files') : t('Remove'),
      danger: true,
    })
    if (yes) await run(files ? 'remove-files' : 'remove', t('Removed.'))
  }

  // For one of Mediarium's own: stop the download, blocklist the release and look for another, and clear the torrent out.
  async function searchAgain() {
    const yes = await confirm({
      title: t('Give up on this one and search again?'),
      body: <p>{t('The release goes on the blocklist, the torrent and its files are removed from {client}, and Mediarium looks for another release.', { client: label })}</p>,
      confirmLabel: t('Give up and search again'),
      danger: true,
    })
    if (!yes || !tor.queueId) return
    setBusy(true)
    try {
      await api.stopQueueItem(tor.queueId, true).catch(() => undefined)
      await api.clientTorrentAction(tor.hash, 'remove-files').catch(() => undefined)
      await api.blocklistQueueItem(tor.queueId)
      toast.success(t('Blocklisted. Looking for another release.'))
      reload()
    } catch (e) {
      toast.error(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(false)
    }
  }

  const speed = [tor.downSpeed > 0 ? `↓ ${formatBytes(tor.downSpeed)}/s` : '', tor.upSpeed > 0 ? `↑ ${formatBytes(tor.upSpeed)}/s` : ''].filter(Boolean).join(' · ')
  return (
    <div className={`qrow st-${tor.stage === 'downloading' ? 'downloading' : tor.stage === 'problem' ? 'failed' : tor.stage === 'paused' ? 'paused' : 'queued'} kind-movie`}>
      <div className="qthumb client-thumb" aria-hidden="true">
        <Icon name="download" size={22} />
      </div>
      <div className="qmain">
        <div className="qtitle">
          <span className="client-name" title={tor.name}>{tor.name}</span>
          <span className={`badge ${tor.stage === 'problem' ? 'failed' : tor.stage === 'downloading' ? 'downloading' : ''}`}>{tor.stageText}</span>
          {tor.mine && <span className="qproto">Mediarium</span>}
          {tor.category && !tor.mine && <span className="qproto">{tor.category}</span>}
        </div>
        {(active || tor.stage === 'paused') && (
          <div className="qprogress">
            <div className={`bar ${tor.stage === 'downloading' ? 'active' : ''} ${tor.stage === 'paused' ? 'paused' : ''}`}>
              <span style={{ width: `${Math.max(2, tor.progress)}%` }} />
            </div>
          </div>
        )}
        <div className="qspeed">
          {[
            speed,
            tor.etaSeconds > 0 && etaText(tor.etaSeconds) ? t('about {time} left', { time: etaText(tor.etaSeconds) }) : '',
            tor.stage === 'downloading' || tor.stage === 'waiting' ? t('{seeds} seeds and {peers} peers connected (the swarm has {swarmSeeds} and {swarmPeers})', { seeds: tor.seeds, peers: tor.peers, swarmSeeds: Math.max(0, tor.swarmSeeds), swarmPeers: Math.max(0, tor.swarmPeers) }) : '',
            tor.stage === 'seeding' || tor.stage === 'done' ? t('Shared {ratio} times', { ratio: tor.ratio.toFixed(2) }) : '',
          ]
            .filter(Boolean)
            .join(' · ')}
        </div>
        {stalled && <div className="qnote">{t('No data for {time}. It may be dead.', { time: etaText(tor.idleSeconds) })}</div>}
        <small className="qwhen">
          {tor.addedAt ? t('Added {when}', { when: timeAgo(tor.addedAt) }) : ''}
          {tor.sizeBytes ? `${tor.addedAt ? ' · ' : ''}${formatBytes(tor.sizeBytes)}` : ''}
        </small>
      </div>
      <div className="qmeter" aria-hidden="true">
        <b>
          {tor.progress.toFixed(0)}
          <small>%</small>
        </b>
      </div>
      <div className="row-actions qactions">
        {tor.canPause && (
          <button className="btn-sm btn-with-icon" disabled={busy} onClick={() => void run('pause', t('Paused.'))}>
            <Icon name="pause" size={15} /> {t('Pause')}
          </button>
        )}
        {tor.canResume && (
          <button className="primary btn-sm btn-with-icon" disabled={busy} onClick={() => void run('resume', t('Started again.'))}>
            <Icon name="play" size={15} /> {t('Resume')}
          </button>
        )}
        {tor.mine && tor.queueId && (tor.stage === 'waiting' || tor.stage === 'downloading' || tor.stage === 'problem') && (
          <button className="btn-sm btn-with-icon" disabled={busy} title={t('Blocklist this release and look for another one')} onClick={() => void searchAgain()}>
            <Icon name="ban" size={15} /> {t('Give up and search again')}
          </button>
        )}
        <ActionMenu
          label={t('More')}
          icon="sliders"
          disabled={busy}
          choices={[
            { id: 'recheck', label: t('Check the files again') },
            { id: 'remove', label: t('Remove, keep the files'), danger: true },
            { id: 'remove-files', label: t('Remove and delete the files'), danger: true },
          ]}
          onPick={(id) => {
            if (id === 'recheck') void run('recheck', t('Checking the files again.'))
            else void remove(id === 'remove-files')
          }}
        />
      </div>
    </div>
  )
}

function DebridSection({ data, reload }: { data: { label?: string; error?: string; torrents: DebridTorrent[] }; reload: () => void }) {
  const label = data.label ?? t('The debrid service')
  const toast = useToast()
  const confirm = useConfirm()
  const [busy, setBusy] = useState('')

  async function remove(d: DebridTorrent) {
    const yes = await confirm({ title: t('Remove {name} from {service}?', { name: d.name, service: label }), body: <p>{t('It is deleted from your account there. Files Mediarium already downloaded are not touched.')}</p>, confirmLabel: t('Remove'), danger: true })
    if (!yes) return
    setBusy(d.id)
    try {
      await api.debridRemove(d.id)
      toast.success(t('Removed.'))
      reload()
    } catch (e) {
      toast.error(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy('')
    }
  }

  return (
    <section>
      <div className="rail-head">
        <h2>{label}</h2>
        <span>{data.torrents.length === 1 ? t('1 torrent') : t('{n} torrents', { n: data.torrents.length })}</span>
      </div>
      {data.error && <p className="error-text">{data.error}</p>}
      {!data.error && data.torrents.length === 0 && <p className="hint">{t('{name} has no torrents.', { name: label })}</p>}
      {data.torrents.map((d) => (
        <div key={d.id} className={`qrow st-${d.stage === 'downloading' ? 'downloading' : d.stage === 'problem' ? 'failed' : 'queued'} kind-movie`}>
          <div className="qthumb client-thumb" aria-hidden="true">
            <Icon name="download" size={22} />
          </div>
          <div className="qmain">
            <div className="qtitle">
              <span className="client-name" title={d.name}>{d.name}</span>
              <span className={`badge ${d.stage === 'problem' ? 'failed' : d.stage === 'downloading' ? 'downloading' : ''}`}>{d.stageText}</span>
            </div>
            {d.stage !== 'ready' && d.stage !== 'problem' && (
              <div className="qprogress">
                <div className={`bar ${d.stage === 'downloading' ? 'active' : ''}`}>
                  <span style={{ width: `${Math.max(2, d.progress)}%` }} />
                </div>
              </div>
            )}
            <div className="qspeed">{[d.downSpeed > 0 ? `↓ ${formatBytes(d.downSpeed)}/s` : '', d.seeders >= 0 ? t('{n} seeders', { n: d.seeders }) : ''].filter(Boolean).join(' · ')}</div>
            <small className="qwhen">
              {d.addedAt ? t('Added {when}', { when: timeAgo(d.addedAt) }) : ''}
              {d.sizeBytes ? `${d.addedAt ? ' · ' : ''}${formatBytes(d.sizeBytes)}` : ''}
            </small>
          </div>
          <div className="qmeter" aria-hidden="true">
            <b>
              {d.progress.toFixed(0)}
              <small>%</small>
            </b>
          </div>
          <div className="row-actions qactions">
            <button className="icon-btn danger" title={t('Remove from {service}', { service: label })} aria-label={t('Remove from {service}', { service: label })} disabled={busy === d.id} onClick={() => void remove(d)}>
              <Icon name="trash" size={17} />
            </button>
          </div>
        </div>
      ))}
    </section>
  )
}
