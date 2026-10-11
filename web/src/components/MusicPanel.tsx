import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { Link } from 'react-router-dom'
import { api, isAdmin, type TrackLyrics } from '../api'
import { useAuth } from '../AuthContext'
import { activeLine, parseLrc } from '../lrc'
import { t, plural } from '../i18n'
import { clock } from '../musicAudio'
import { MAX_CROSSFADE, type QueuedTrack } from '../musicQueue'
import Icon from './Icon'
import { PlaylistPicker, Popover } from './MusicMenu'
import { useMusicPlayer, type PanelTab } from './musicPlayerContext'
import Switch from './Switch'
import { useToast } from './Toast'
import '../musicpanel.css'

// The panel above the player bar: what is queued, the words of the song, and
// the settings of the player.
export default function NowPlayingPanel() {
  const p = useMusicPlayer()
  if (!p || !p.panel) return null
  const tabs: { id: PanelTab; label: string }[] = [
    { id: 'queue', label: t('Queue') },
    { id: 'lyrics', label: t('Lyrics') },
    { id: 'settings', label: t('Settings') },
  ]
  return (
    <section className="np-panel" aria-label={t('Now playing')}>
      <div className="np-head">
        <div className="seg" role="tablist">
          {tabs.map((tab) => (
            <button key={tab.id} role="tab" aria-selected={p.panel === tab.id} className={p.panel === tab.id ? 'active' : ''} onClick={() => p.setPanel(tab.id)}>
              {tab.label}
            </button>
          ))}
        </div>
        <button type="button" className="mp-btn" onClick={() => p.setPanel(null)} aria-label={t('Close this panel')} title={t('Close this panel')}>
          <Icon name="chevron-down" size={18} />
        </button>
      </div>
      <div className="np-body">
        {p.panel === 'queue' && <QueueTab />}
        {p.panel === 'lyrics' && <LyricsTab />}
        {p.panel === 'settings' && <SettingsTab />}
      </div>
    </section>
  )
}

// ---- Queue -------------------------------------------------------------------------

function totalLength(queue: QueuedTrack[]): string {
  const s = Math.round(queue.reduce((n, q) => n + q.lengthMs, 0) / 1000)
  const h = Math.floor(s / 3600)
  const m = Math.floor((s % 3600) / 60)
  return h > 0 ? t('{h} h {m} min', { h, m }) : t('{m} min', { m })
}

function QueueTab() {
  const p = useMusicPlayer()
  const [dragFrom, setDragFrom] = useState<number | null>(null)
  const [dragOver, setDragOver] = useState<number | null>(null)
  const [saveAnchor, setSaveAnchor] = useState<HTMLElement | null>(null)
  const list = useRef<HTMLOListElement>(null)
  const index = p?.index ?? -1
  const uid = p?.current?.uid

  // Bring the track that is playing into view when the panel opens or the track changes.
  useEffect(() => {
    list.current?.querySelector('li.now')?.scrollIntoView({ block: 'nearest' })
  }, [uid])

  const closeSave = useCallback(() => setSaveAnchor(null), [])
  if (!p) return null
  const q = p.queue
  if (q.length === 0) return <p className="np-empty">{t('The queue is empty.')}</p>

  const drop = (to: number) => {
    if (dragFrom !== null && dragFrom !== to) p.moveInQueue(dragFrom, to)
    setDragFrom(null)
    setDragOver(null)
  }

  return (
    <div className="np-queue">
      <div className="np-queue-bar">
        <span className="np-count">
          {plural(q.length, '{n} track', '{n} tracks')} · {totalLength(q)}
        </span>
        <span className="spacer" />
        <button type="button" className="btn-sm btn-with-icon" onClick={(e) => setSaveAnchor(saveAnchor ? null : e.currentTarget)} aria-haspopup="dialog" aria-expanded={!!saveAnchor}>
          <Icon name="playlist-add" size={14} /> {t('Save as playlist')}
        </button>
        <button type="button" className="btn-sm btn-with-icon" onClick={() => p.clearQueue(true)} disabled={q.length <= 1} title={t('Remove everything except the track that is playing')}>
          <Icon name="trash" size={14} /> {t('Clear')}
        </button>
      </div>
      {saveAnchor && (
        <Popover anchor={saveAnchor} onClose={closeSave} label={t('Save the queue as a playlist')}>
          <PlaylistPicker selection={{ trackIds: q.map((x) => x.id) }} onDone={closeSave} />
        </Popover>
      )}
      <ol className="np-list" ref={list} aria-label={t('Queue')}>
        {q.map((tr, i) => (
          <li
            key={tr.uid}
            className={`${i === index ? 'now' : ''}${dragFrom === i ? ' dragging' : ''}${dragOver === i && dragFrom !== null && dragFrom !== i ? (dragFrom < i ? ' over-after' : ' over-before') : ''}`}
            draggable
            onDragStart={(e) => {
              setDragFrom(i)
              e.dataTransfer.effectAllowed = 'move'
              e.dataTransfer.setData('text/plain', String(i))
            }}
            onDragOver={(e) => {
              if (dragFrom === null) return
              e.preventDefault()
              e.dataTransfer.dropEffect = 'move'
              if (dragOver !== i) setDragOver(i)
            }}
            onDrop={(e) => {
              e.preventDefault()
              drop(i)
            }}
            onDragEnd={() => {
              setDragFrom(null)
              setDragOver(null)
            }}
          >
            <span className="np-grip" aria-hidden="true" title={t('Drag to move')}>
              <Icon name="grip" size={16} />
            </span>
            <button type="button" className="np-main" onClick={() => p.jumpTo(i)} title={t('Play {title}', { title: tr.title })}>
              <span className="np-n">{i === index ? <Icon name={p.playing ? 'play' : 'pause'} size={12} /> : i + 1}</span>
              <span className="np-t">
                <strong>{tr.title}</strong>
                <small>{[tr.artist, tr.album].filter(Boolean).join(' · ')}</small>
              </span>
              <span className="np-d">{clock(tr.lengthMs / 1000)}</span>
            </button>
            <span className="np-acts">
              <button type="button" className="mp-btn" onClick={() => p.moveInQueue(i, i - 1)} disabled={i === 0} aria-label={t('Move {title} up', { title: tr.title })} title={t('Move up')}>
                <Icon name="chevron-up" size={15} />
              </button>
              <button type="button" className="mp-btn" onClick={() => p.moveInQueue(i, i + 1)} disabled={i === q.length - 1} aria-label={t('Move {title} down', { title: tr.title })} title={t('Move down')}>
                <Icon name="chevron-down" size={15} />
              </button>
              <button type="button" className="mp-btn" onClick={() => p.removeFromQueue(i)} aria-label={t('Remove {title} from the queue', { title: tr.title })} title={t('Remove from the queue')}>
                <Icon name="x" size={15} />
              </button>
            </span>
          </li>
        ))}
      </ol>
    </div>
  )
}

// ---- Lyrics -------------------------------------------------------------------------

type LyricsState = { id: number; status: 'loading' | 'ready' | 'error'; data?: TrackLyrics; error?: string }

const sourceName = (source: string): string =>
  source === 'sidecar' ? t('From a .lrc file next to the track') : source === 'file' ? t('From the tags in the file') : t('From LRCLIB')

function LyricsTab() {
  const p = useMusicPlayer()
  const id = p?.current?.id ?? 0
  const [state, setState] = useState<LyricsState>({ id: 0, status: 'loading' })
  const view = useRef<HTMLDivElement>(null)
  const touched = useRef(0) // when the reader last scrolled by hand

  useEffect(() => {
    if (!id) return
    let live = true
    api
      .trackLyrics(id)
      .then((data) => live && setState({ id, status: 'ready', data }))
      .catch((e) => live && setState({ id, status: 'error', error: e instanceof Error ? e.message : String(e) }))
    return () => {
      live = false
    }
  }, [id])

  const data = state.id === id ? state.data : undefined
  const parsed = useMemo(() => {
    if (!data) return null
    if (data.synced) {
      const s = parseLrc(data.synced)
      if (s.synced) return s
    }
    const plain = data.plain || (data.synced ? parseLrc(data.synced).lines.map((l) => l.text).join('\n') : '')
    return plain ? { lines: plain.split(/\r?\n/).map((text) => ({ time: 0, text })), synced: false } : null
  }, [data])

  const time = p?.time ?? 0
  const active = parsed?.synced ? activeLine(parsed.lines, time) : -1

  // Keep the line being sung in the middle, unless the reader is scrolling.
  useEffect(() => {
    if (active < 0 || Date.now() - touched.current < 4000) return
    const el = view.current?.querySelector<HTMLElement>(`[data-line="${active}"]`)
    if (!el) return
    const reduce = window.matchMedia('(prefers-reduced-motion: reduce)').matches
    el.scrollIntoView({ block: 'center', behavior: reduce ? 'auto' : 'smooth' })
  }, [active])

  if (!p?.current) return null
  if (state.id !== id || state.status === 'loading') return <div className="skeleton" style={{ height: 120 }} />
  if (state.status === 'error') return <p className="np-empty">{state.error}</p>
  if (!data || !parsed) {
    return (
      <div className="np-empty">
        {data?.instrumental ? <p>{t('This track has no words.')}</p> : <p>{t('No lyrics found for this track.')}</p>}
        {!data?.instrumental && (
          <small>
            {data && !data.online
              ? t('Looking them up online is switched off. You can put a .lrc file with the same name next to the track.')
              : t('You can put a .lrc file with the same name next to the track, or add the words to its tags.')}
          </small>
        )}
      </div>
    )
  }
  return (
    <div className="np-lyrics" ref={view} onWheel={() => (touched.current = Date.now())} onTouchMove={() => (touched.current = Date.now())}>
      <ol className={parsed.synced ? 'synced' : 'plain'}>
        {parsed.lines.map((l, i) => (
          <li key={i} data-line={i} className={i === active ? 'now' : i < active ? 'past' : ''}>
            {parsed.synced ? (
              <button type="button" onClick={() => p.seek(l.time)} title={t('Jump to {time}', { time: clock(l.time) })}>
                {l.text || '♪'}
              </button>
            ) : (
              l.text || ' '
            )}
          </li>
        ))}
      </ol>
      {data?.source && <p className="np-source">{sourceName(data.source)}</p>}
    </div>
  )
}

// ---- Settings -------------------------------------------------------------------------

function SettingsTab() {
  const p = useMusicPlayer()
  const { user } = useAuth()
  const toast = useToast()
  const admin = isAdmin(user)
  const [online, setOnline] = useState<boolean | null>(null)
  useEffect(() => {
    let live = true
    api
      .musicPlayerSettings()
      .then((s) => live && setOnline(s.lyricsOnline))
      .catch(() => undefined)
    return () => {
      live = false
    }
  }, [])
  if (!p) return null
  const s = p.settings

  async function setLyricsOnline(v: boolean) {
    try {
      const r = await api.setMusicPlayerSettings(v)
      setOnline(r.lyricsOnline)
      toast.success(r.lyricsOnline ? t('Lyrics are looked up online when a track has none.') : t('Lyrics are no longer looked up online.'))
    } catch (e) {
      toast.error(e instanceof Error ? e.message : String(e))
    }
  }

  return (
    <div className="np-settings">
      <div className="np-field">
        <label htmlFor="np-crossfade">
          {t('Crossfade')}
          <output>{s.crossfade === 0 ? t('Off') : t('{n} s', { n: s.crossfade })}</output>
        </label>
        <input
          id="np-crossfade"
          type="range"
          min={0}
          max={MAX_CROSSFADE}
          step={1}
          value={s.crossfade}
          onChange={(e) => p.setSettings({ crossfade: Number(e.target.value) })}
          style={{ ['--p' as string]: `${(s.crossfade / MAX_CROSSFADE) * 100}%` }}
        />
        <small>{t('Fades one track into the next. Tracks of the same album always follow each other without a fade.')}</small>
      </div>
      <div className="np-field">
        <label htmlFor="np-gain">{t('Even out the volume (ReplayGain)')}</label>
        <select id="np-gain" value={s.replayGain} onChange={(e) => p.setSettings({ replayGain: e.target.value as 'off' | 'track' | 'album' })}>
          <option value="off">{t('Off')}</option>
          <option value="track">{t('By track')}</option>
          <option value="album">{t('By album')}</option>
        </select>
        <small>{t('Uses the ReplayGain tags in the files. A file without them plays as it is.')}</small>
      </div>
      <p className="np-note">{t('These two are kept on this device. On an iPhone or iPad they may stop working while the screen is locked.')}</p>
      <div className="np-field">
        <span className="np-label">{t('ListenBrainz')}</span>
        <small>
          {p.scrobbling?.connected && p.scrobbling.enabled ? t('Your plays are sent to ListenBrainz.') : p.scrobbling?.connected ? t('Connected, but switched off.') : t('Not connected.')}{' '}
          <Link to="/settings/services#listenbrainz">{t('Set it up in your profile')}</Link>
        </small>
      </div>
      {admin && online !== null && (
        <Switch
          checked={online}
          onChange={(v) => void setLyricsOnline(v)}
          label={t('Look up lyrics online')}
          description={t('When a track has no lyrics of its own, the title, artist and album are sent to lrclib.net, once per track. This is for everyone on this server.')}
        />
      )}
    </div>
  )
}
