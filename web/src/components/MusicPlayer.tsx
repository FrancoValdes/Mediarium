import { useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from 'react'
import { api, type TrackGainInfo } from '../api'
import { useModules } from '../ModulesContext'
import { t } from '../i18n'
import { AudioEngine } from '../musicEngine'
import { clock, crossfadeFor, gainFactor, ListenTracker } from '../musicAudio'
import {
  addToEnd,
  clearQueue as clearQueueState,
  DEFAULT_SETTINGS,
  moveTrack,
  parseQueue,
  parseSettings,
  planNext,
  playNext,
  QUEUE_STORAGE_KEY,
  removeTrack,
  serializeQueue,
  SETTINGS_STORAGE_KEY,
  withUids,
  type PlayerSettings,
  type PlayerTrack,
  type QueuedTrack,
  type QueueState,
  type Repeat,
} from '../musicQueue'
import Icon from './Icon'
import MusicMenu from './MusicMenu'
import NowPlayingPanel from './MusicPanel'
import { albumCoverUrl, PlayerContext, trackUrl, useMusicPlayer, type PanelTab, type PlayerApi } from './musicPlayerContext'
import { useToast } from './Toast'
import '../player.css'
import { withBase } from '../base'

// The music player: one pair of audio elements for the whole app (see
// musicEngine.ts), so a song keeps playing while you move between pages, and a
// bar at the bottom to control it. Tracks come from the music folder through
// /api/music/tracks/{id}/play, which supports seeking.

export { albumCoverUrl, trackUrl, useMusicPlayer }
export type { PlayerTrack, QueuedTrack }

// Moving playback between the app window and the pop-out window.
const CHANNEL = 'mediarium-music'
const HANDOFF = 'mediarium-music-handoff'

interface Snapshot {
  queue: PlayerTrack[]
  index: number
  time: number
  playing: boolean
  shuffle: boolean
  repeat: Repeat
  at?: number // when the app window handed it over
}

const VOLUME_KEY = 'mediarium-music-volume'

function readVolume(): number {
  try {
    const v = Number(localStorage.getItem(VOLUME_KEY))
    return Number.isFinite(v) && v > 0 && v <= 1 ? v : 0.9
  } catch {
    return 0.9
  }
}

function readSettings(): PlayerSettings {
  try {
    return parseSettings(localStorage.getItem(SETTINGS_STORAGE_KEY))
  } catch {
    return DEFAULT_SETTINGS
  }
}

// The queue kept on this device, read once when the player starts.
function readSavedQueue() {
  try {
    return parseQueue(localStorage.getItem(QUEUE_STORAGE_KEY))
  } catch {
    return null
  }
}

interface EngineHandlers {
  time: (position: number, duration: number) => void
  playing: (isPlaying: boolean) => void
  duration: (seconds: number) => void
  advanced: () => void
  ended: () => void
  failed: () => void
}

// How long a track waits for its ReplayGain tags before it starts without them.
const GAIN_WAIT_MS = 500

export function MusicPlayerProvider({ children, popout = false }: { children: ReactNode; popout?: boolean }) {
  const { on: moduleOn, loaded: modulesLoaded } = useModules()
  const [saved] = useState(() => (popout ? null : readSavedQueue()))
  const [queue, setQueue] = useState<QueuedTrack[]>(() => (saved ? withUids(saved.queue) : []))
  const [index, setIndex] = useState(saved ? saved.index : -1)
  const [nonce, setNonce] = useState(0) // changes when a track has to be loaded again, so the same one can start again
  const [playing, setPlaying] = useState(false)
  const [time, setTime] = useState(saved ? saved.time : 0)
  const [duration, setDuration] = useState(0)
  const [volume, setVolumeState] = useState(readVolume)
  const [shuffle, setShuffle] = useState(saved ? saved.shuffle : false)
  const [repeat, setRepeat] = useState<Repeat>(saved ? saved.repeat : 'off')
  const [problem, setProblem] = useState('')
  const [settings, setSettingsState] = useState<PlayerSettings>(readSettings)
  const [panel, setPanelState] = useState<PanelTab | null>(null)
  const [scrobbling, setScrobbling] = useState<{ connected: boolean; enabled: boolean } | null>(null)
  const current = index >= 0 && index < queue.length ? queue[index] : null
  const skipTimer = useRef<number | undefined>(undefined)
  const startAt = useRef(saved ? saved.time : 0) // where in the track to start (a hand-over, or the queue kept from last time)
  const wantPlay = useRef(!saved) // whether a newly loaded track starts at once
  const channel = useRef<BroadcastChannel | null>(null)
  const [taken, setTaken] = useState(false)

  // The newest state, for the engine's callbacks (which outlive any one render).
  const latest = useRef({ queue, index, shuffle, repeat, settings, nonce, current })
  latest.current = { queue, index, shuffle, repeat, settings, nonce, current }
  const planned = useRef(-1) // the place that plays after this one, as already loaded by the engine
  const loadedKey = useRef('') // the track and start the engine is playing now
  const gains = useRef(new Map<number, Promise<TrackGainInfo | null>>())
  const tracker = useRef(new ListenTracker())
  const scrobbleOn = useRef<Promise<boolean> | null>(null)
  const events = useRef<EngineHandlers>({
    time: () => undefined,
    playing: () => undefined,
    duration: () => undefined,
    advanced: () => undefined,
    ended: () => undefined,
    failed: () => undefined,
  })
  const [engine] = useState(
    () =>
      new AudioEngine({
        time: (p, d) => events.current.time(p, d),
        playing: (b) => events.current.playing(b),
        duration: (d) => events.current.duration(d),
        advanced: () => events.current.advanced(),
        ended: () => events.current.ended(),
        failed: () => events.current.failed(),
      }),
  )

  // ---- ListenBrainz -----------------------------------------------------------------------

  const refreshScrobbling = useCallback(() => {
    scrobbleOn.current = null
    api
      .listenBrainz()
      .then((s) => setScrobbling({ connected: s.connected, enabled: s.enabled }))
      .catch(() => setScrobbling(null))
  }, [])

  // Whether plays are sent: asked once, the first time something plays.
  const scrobbleEnabled = useCallback((): Promise<boolean> => {
    if (!scrobbleOn.current) {
      scrobbleOn.current = api
        .listenBrainz()
        .then((s) => {
          setScrobbling({ connected: s.connected, enabled: s.enabled })
          return s.connected && s.enabled
        })
        .catch(() => false)
    }
    return scrobbleOn.current
  }, [])

  // ---- ReplayGain -----------------------------------------------------------------------------

  const gainInfo = useCallback((id: number): Promise<TrackGainInfo | null> => {
    let p = gains.current.get(id)
    if (!p) {
      if (gains.current.size > 300) gains.current.clear()
      p = api.trackGain(id).catch(() => null)
      gains.current.set(id, p)
    }
    return p
  }, [])

  // The factor for a track under the chosen mode; waits only a moment for the tags.
  const factorFor = useCallback(
    async (id: number, wait: number): Promise<number> => {
      const mode = latest.current.settings.replayGain
      if (mode === 'off') return 1
      const info = await Promise.race([gainInfo(id), new Promise<null>((r) => window.setTimeout(() => r(null), wait))])
      return gainFactor(mode, info)
    },
    [gainInfo],
  )

  // ---- Loading and playing -----------------------------------------------------------------------

  // The graph for ReplayGain and crossfades is only built when one is on.
  useEffect(() => {
    engine.setProcessing(settings.replayGain !== 'off' || settings.crossfade > 0)
  }, [engine, settings.replayGain, settings.crossfade])

  // Load and play the current track. A track the engine already moved on to by
  // itself (gapless) has its key set and is left alone.
  useEffect(() => {
    if (!current) return
    const key = `${current.uid}:${nonce}`
    if (loadedKey.current === key) return
    let cancelled = false
    const id = current.id
    const lengthMs = current.lengthMs
    const go = (gain: number) => {
      loadedKey.current = key
      setProblem('')
      setTime(startAt.current)
      setDuration(lengthMs / 1000)
      const from = startAt.current
      const autoplay = wantPlay.current
      startAt.current = 0
      wantPlay.current = true
      engine.load(trackUrl(id), { startAt: from, autoplay, gain })
      tracker.current.start(lengthMs / 1000, Date.now())
      if (autoplay) void announce(id)
    }
    if (latest.current.settings.replayGain === 'off') {
      go(1) // straight away, inside the click that started it
    } else {
      void factorFor(id, GAIN_WAIT_MS).then((gain) => {
        if (!cancelled) go(gain)
      })
    }
    return () => {
      cancelled = true
    }
    // current?.uid and nonce are what start a track; the object itself changes with the queue.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [current?.uid, nonce])

  // Tell ListenBrainz what is playing now.
  async function announce(id: number) {
    if (await scrobbleEnabled()) void api.scrobblePlaying(id).catch(() => undefined)
  }

  // Load the track after this one ahead of time, so it follows without a gap.
  useEffect(() => {
    engine.holdNext() // until the new plan is ready, nothing can start from the old one
    const n = planNext(queue.length, index, shuffle, repeat)
    planned.current = n
    if (n < 0 || !current) {
      engine.setNext(null)
      return
    }
    const after = queue[n]
    let cancelled = false
    void (async () => {
      const gain = await factorFor(after.id, 3000)
      if (cancelled) return
      engine.setNext({ url: trackUrl(after.id), gain, fade: crossfadeFor(settings.crossfade, current, after) })
    })()
    return () => {
      cancelled = true
    }
    // The queue, place, modes and settings are what decide it.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [queue, index, shuffle, repeat, settings.crossfade, settings.replayGain, nonce])

  // Switching ReplayGain on or off changes the track that is playing too.
  useEffect(() => {
    const c = latest.current.current
    if (!c || !loadedKey.current) return
    let cancelled = false
    void factorFor(c.id, 3000).then((f) => {
      if (!cancelled) engine.setCurrentGain(f)
    })
    return () => {
      cancelled = true
    }
  }, [engine, factorFor, settings.replayGain])

  useEffect(() => {
    engine.setVolume(volume)
    try {
      localStorage.setItem(VOLUME_KEY, String(volume))
    } catch {
      // Private windows may refuse; the volume then starts at the default again.
    }
  }, [engine, volume])

  useEffect(() => {
    if (popout) return
    document.body.classList.toggle('has-player', !!current)
    return () => document.body.classList.remove('has-player')
  }, [current, popout])

  // What the engine tells us.
  events.current.time = (position: number, dur: number) => {
    setTime(position)
    if (dur > 0) {
      setDuration(dur)
      tracker.current.setDuration(dur)
    }
    if (tracker.current.tick(position)) {
      const c = latest.current.current
      if (c) {
        const at = tracker.current.startedAtSeconds
        void scrobbleEnabled().then((ok) => {
          if (ok) void api.scrobbleListen(c.id, at).catch(() => undefined)
        })
      }
    }
  }
  events.current.playing = (b: boolean) => setPlaying(b)
  events.current.duration = (d: number) => {
    if (d > 0) setDuration(d)
  }
  events.current.advanced = () => {
    const n = planned.current
    const s = latest.current
    if (n < 0 || n >= s.queue.length) return
    // The engine is already playing it: remember that, so the load effect leaves it alone.
    loadedKey.current = `${s.queue[n].uid}:${s.nonce}`
    setProblem('')
    setTime(0)
    setDuration(s.queue[n].lengthMs / 1000)
    tracker.current.start(s.queue[n].lengthMs / 1000, Date.now())
    void announce(s.queue[n].id)
    setIndex(n)
  }
  // The track ended with nothing loaded ahead: on to the next, or stop.
  events.current.ended = () => {
    const s = latest.current
    const n = planNext(s.queue.length, s.index, s.shuffle, s.repeat)
    if (n < 0) {
      setPlaying(false)
      return
    }
    setIndex(n)
    setNonce((x) => x + 1)
  }
  events.current.failed = () => {
    setPlaying(false)
    setProblem(t("This track can't be played here. The file may be in a format your browser doesn't support."))
    window.clearTimeout(skipTimer.current)
    // On to the next one after a moment, so a bad file doesn't stop an album.
    skipTimer.current = window.setTimeout(() => {
      const s = latest.current
      const n = planNext(s.queue.length, s.index, s.shuffle, s.repeat === 'one' ? 'off' : s.repeat)
      if (n >= 0 && n !== s.index) {
        setIndex(n)
        setNonce((x) => x + 1)
      }
    }, 2200)
  }

  // ---- Keeping the queue between visits -------------------------------------------------------------

  useEffect(() => {
    if (popout) return
    const save = () => {
      const s = latest.current
      try {
        if (s.queue.length === 0) localStorage.removeItem(QUEUE_STORAGE_KEY)
        else localStorage.setItem(QUEUE_STORAGE_KEY, serializeQueue({ queue: s.queue, index: s.index, time: engine.position, shuffle: s.shuffle, repeat: s.repeat }))
      } catch {
        // A private window may refuse; the queue is then not kept.
      }
    }
    save()
    const timer = window.setInterval(save, 5000)
    window.addEventListener('pagehide', save)
    return () => {
      window.clearInterval(timer)
      window.removeEventListener('pagehide', save)
    }
  }, [popout, engine, queue, index, shuffle, repeat])

  // The music module was switched off: whatever was kept is let go.
  useEffect(() => {
    if (!popout && modulesLoaded && !moduleOn('music') && latest.current.queue.length > 0) {
      engine.stop()
      setQueue([])
      setIndex(-1)
      setPlaying(false)
      loadedKey.current = ''
    }
  }, [popout, modulesLoaded, moduleOn, engine])

  // ---- Queue ---------------------------------------------------------------------------------------

  // Applies an edit of the queue. When the track that was playing is gone, the
  // one that took its place starts (or the player stops when none is left).
  const commit = useCallback(
    (s: QueueState & { currentRemoved?: boolean }, startNow = false) => {
      latest.current = { ...latest.current, queue: s.queue, index: s.index }
      setQueue(s.queue)
      setIndex(s.index)
      if (s.queue.length === 0) {
        engine.stop()
        loadedKey.current = ''
        setPlaying(false)
        setProblem('')
        if ('mediaSession' in navigator) navigator.mediaSession.metadata = null
      } else if (s.currentRemoved || startNow) {
        wantPlay.current = startNow || engine.position > 0 || !engine.paused
        loadedKey.current = ''
        setNonce((x) => x + 1)
      }
    },
    [engine],
  )

  const playQueue = useCallback(
    (tracks: PlayerTrack[], start = 0) => {
      if (tracks.length === 0) return
      channel.current?.postMessage({ type: 'stop' })
      wantPlay.current = true
      startAt.current = 0
      setQueue(withUids(tracks))
      setIndex(Math.min(Math.max(start, 0), tracks.length - 1))
      setNonce((x) => x + 1)
    },
    [],
  )

  const playNextTracks = useCallback(
    (tracks: PlayerTrack[]) => {
      const s = latest.current
      const empty = s.queue.length === 0
      if (empty) channel.current?.postMessage({ type: 'stop' })
      commit(playNext({ queue: s.queue, index: s.index }, tracks), empty)
    },
    [commit],
  )

  const addToQueue = useCallback(
    (tracks: PlayerTrack[]) => {
      const s = latest.current
      const empty = s.queue.length === 0
      if (empty) channel.current?.postMessage({ type: 'stop' })
      commit(addToEnd({ queue: s.queue, index: s.index }, tracks), empty)
    },
    [commit],
  )

  const moveInQueue = useCallback(
    (from: number, to: number) => {
      const s = latest.current
      commit(moveTrack({ queue: s.queue, index: s.index }, from, to))
    },
    [commit],
  )

  const removeFromQueue = useCallback(
    (at: number) => {
      const s = latest.current
      commit(removeTrack({ queue: s.queue, index: s.index }, at))
    },
    [commit],
  )

  const clearQueue = useCallback(
    (keepCurrent: boolean) => {
      const s = latest.current
      commit(clearQueueState({ queue: s.queue, index: s.index }, keepCurrent))
    },
    [commit],
  )

  const advance = useCallback(() => {
    const s = latest.current
    if (s.queue.length === 0) return
    // The button always moves on, even with "repeat this track" on.
    let n = planNext(s.queue.length, s.index, s.shuffle, s.repeat === 'one' ? 'all' : s.repeat)
    if (n < 0) n = 0
    setIndex(n)
    setNonce((x) => x + 1)
  }, [])

  const snapshot = useCallback(
    (): Snapshot => ({ queue, index, time: engine.position, playing: !engine.paused, shuffle, repeat }),
    [queue, index, shuffle, repeat, engine],
  )

  const applySnapshot = useCallback((s: Snapshot) => {
    if (!s || !Array.isArray(s.queue) || s.queue.length === 0) return
    setTaken(false)
    setQueue(withUids(s.queue))
    setIndex(Math.min(Math.max(s.index, 0), s.queue.length - 1))
    setShuffle(!!s.shuffle)
    setRepeat(s.repeat === 'all' || s.repeat === 'one' ? s.repeat : 'off')
    startAt.current = s.time > 1 ? s.time : 0
    wantPlay.current = !!s.playing
    loadedKey.current = ''
    setNonce((x) => x + 1)
  }, [])

  // The two windows talk through a channel: a new queue started in the app
  // window stops the pop-out, and the pop-out hands its queue back.
  useEffect(() => {
    if (typeof BroadcastChannel === 'undefined') return
    const ch = new BroadcastChannel(CHANNEL)
    channel.current = ch
    ch.onmessage = (e: MessageEvent) => {
      const m = e.data as { type?: string; snapshot?: Snapshot } | null
      if (popout && m?.type === 'stop') {
        engine.pause()
        try {
          sessionStorage.removeItem(HANDOFF)
        } catch {
          // Nothing was kept.
        }
        setTaken(true)
      } else if (!popout && m?.type === 'return' && m.snapshot) {
        applySnapshot(m.snapshot)
      }
    }
    return () => {
      ch.close()
      channel.current = null
    }
  }, [popout, applySnapshot, engine])

  // A pop-out window picks up what the app window handed over, or what it was
  // playing before it was reloaded (kept for this window only).
  useEffect(() => {
    if (!popout) return
    try {
      // What this window kept for itself wins; otherwise what the app window
      // just handed over (only if it is fresh, so an old one never restarts music).
      let raw = sessionStorage.getItem(HANDOFF)
      if (!raw) {
        const handed = localStorage.getItem(HANDOFF)
        if (handed && Date.now() - ((JSON.parse(handed) as Snapshot).at ?? 0) < 20000) raw = handed
      }
      if (raw) applySnapshot(JSON.parse(raw) as Snapshot)
    } catch {
      // Nothing to pick up; the window says so.
    }
  }, [popout, applySnapshot])
  const latestSnapshot = useRef<() => Snapshot>(() => ({ queue: [], index: -1, time: 0, playing: false, shuffle: false, repeat: 'off' }))
  latestSnapshot.current = snapshot
  useEffect(() => {
    if (!popout) return
    const save = () => {
      try {
        const s = latestSnapshot.current()
        if (s.queue.length > 0) sessionStorage.setItem(HANDOFF, JSON.stringify(s))
      } catch {
        // A private window may refuse; a reload then starts empty.
      }
    }
    const timer = window.setInterval(save, 3000)
    window.addEventListener('pagehide', save)
    return () => {
      window.clearInterval(timer)
      window.removeEventListener('pagehide', save)
    }
  }, [popout])

  const toggle = useCallback(() => {
    if (!latest.current.current) return
    if (engine.paused) engine.play().catch(() => setPlaying(false))
    else engine.pause()
  }, [engine])

  const prev = useCallback(() => {
    const s = latest.current
    if (engine.position > 3 || s.index <= 0) {
      engine.seek(0)
      tracker.current.seeked()
      return
    }
    setIndex(s.index - 1)
    setNonce((x) => x + 1)
  }, [engine])

  const jumpTo = useCallback((i: number) => {
    setIndex(i)
    setNonce((x) => x + 1)
  }, [])

  const popOut = useCallback((): boolean => {
    if (!latest.current.current) return false
    // What is playing is written first, so the new window finds it as it opens.
    try {
      localStorage.setItem(HANDOFF, JSON.stringify({ ...snapshot(), at: Date.now() }))
    } catch {
      return false
    }
    window.setTimeout(() => {
      try {
        localStorage.removeItem(HANDOFF)
      } catch {
        // Nothing to clean up.
      }
    }, 30000)
    const w = window.open(withBase('/music-player'), 'mediarium-music', 'popup=yes,width=420,height=700')
    if (!w) {
      localStorage.removeItem(HANDOFF)
      return false
    }
    engine.stop()
    loadedKey.current = ''
    setQueue([])
    setIndex(-1)
    setPlaying(false)
    if ('mediaSession' in navigator) navigator.mediaSession.metadata = null
    w.focus()
    return true
  }, [snapshot, engine])

  const handBack = useCallback(() => {
    channel.current?.postMessage({ type: 'return', snapshot: snapshot() })
    engine.stop()
    try {
      sessionStorage.removeItem(HANDOFF)
    } catch {
      // Nothing was kept.
    }
    window.close()
  }, [snapshot, engine])

  const seek = useCallback(
    (seconds: number) => {
      if (!Number.isFinite(seconds)) return
      engine.seek(seconds)
      tracker.current.seeked()
      setTime(seconds)
    },
    [engine],
  )

  const close = useCallback(() => {
    engine.stop()
    window.clearTimeout(skipTimer.current)
    loadedKey.current = ''
    setQueue([])
    setIndex(-1)
    setPlaying(false)
    setProblem('')
    setPanelState(null)
    if ('mediaSession' in navigator) navigator.mediaSession.metadata = null
  }, [engine])

  const setSettings = useCallback((patch: Partial<PlayerSettings>) => {
    setSettingsState((cur) => {
      const next = { ...cur, ...patch }
      try {
        localStorage.setItem(SETTINGS_STORAGE_KEY, JSON.stringify(next))
      } catch {
        // Kept for this visit only.
      }
      return next
    })
  }, [])

  const setPanel = useCallback((tab: PanelTab | null) => setPanelState(tab), [])

  // ---- The lock screen, headphones and the browser's own media keys ------------------------------------

  useEffect(() => {
    if (!('mediaSession' in navigator) || !current) return
    navigator.mediaSession.metadata = new MediaMetadata({
      title: current.title,
      artist: current.artist,
      album: current.album,
      artwork: [{ src: albumCoverUrl(current.albumId), sizes: '500x500' }],
    })
  }, [current])
  useEffect(() => {
    if (!('mediaSession' in navigator) || !current) return
    navigator.mediaSession.playbackState = playing ? 'playing' : 'paused'
  }, [playing, current])
  useEffect(() => {
    if (!('mediaSession' in navigator) || !current || !(duration > 0)) return
    try {
      navigator.mediaSession.setPositionState({ duration, position: Math.min(Math.max(time, 0), duration), playbackRate: 1 })
    } catch {
      // Some browsers refuse odd values; the lock screen then just has no bar.
    }
    // Updated when the track, its length or the play state changes, and on seeks (time jumps), not on every tick.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [current?.uid, duration, playing])
  useEffect(() => {
    if (!('mediaSession' in navigator)) return
    const set = (action: MediaSessionAction, handler: MediaSessionActionHandler | null) => {
      try {
        navigator.mediaSession.setActionHandler(action, handler)
      } catch {
        // An action this browser doesn't know is skipped.
      }
    }
    set('play', () => void engine.play().catch(() => setPlaying(false)))
    set('pause', () => engine.pause())
    set('previoustrack', () => prev())
    set('nexttrack', () => advance())
    set('seekto', (d) => d.seekTime != null && seek(d.seekTime))
    set('seekbackward', (d) => seek(Math.max(0, engine.position - (d.seekOffset ?? 10))))
    set('seekforward', (d) => seek(engine.position + (d.seekOffset ?? 10)))
    return () => {
      for (const a of ['play', 'pause', 'previoustrack', 'nexttrack', 'seekto', 'seekbackward', 'seekforward'] as const) set(a, null)
    }
  }, [engine, prev, advance, seek])

  const api_ = useMemo<PlayerApi>(
    () => ({
      current,
      queue,
      index,
      playing,
      time,
      duration,
      volume,
      shuffle,
      repeat,
      problem,
      playQueue,
      playNextTracks,
      addToQueue,
      moveInQueue,
      removeFromQueue,
      clearQueue,
      toggle,
      next: advance,
      prev,
      seek,
      setVolume: setVolumeState,
      setShuffle,
      cycleRepeat: () => setRepeat((r) => (r === 'off' ? 'all' : r === 'all' ? 'one' : 'off')),
      close,
      jumpTo,
      popOut,
      handBack,
      popout,
      taken,
      settings,
      setSettings,
      volumeWorks: engine.volumeWorks || engine.graphActive,
      panel,
      setPanel,
      scrobbling,
      refreshScrobbling,
    }),
    [current, queue, index, playing, time, duration, volume, shuffle, repeat, problem, playQueue, playNextTracks, addToQueue, moveInQueue, removeFromQueue, clearQueue, toggle, advance, prev, seek, close, jumpTo, popOut, handBack, popout, taken, settings, setSettings, engine, panel, setPanel, scrobbling, refreshScrobbling],
  )

  return <PlayerContext.Provider value={api_}>{children}</PlayerContext.Provider>
}

// The cover, or a pink note while the album has none.
function Cover({ albumId }: { albumId: number }) {
  const [failed, setFailed] = useState<number | null>(null)
  if (failed === albumId) {
    return (
      <span className="mp-cover mp-cover-none" aria-hidden="true">
        <Icon name="music" size={22} />
      </span>
    )
  }
  return <img className="mp-cover" src={albumCoverUrl(albumId)} alt="" width={52} height={52} onError={() => setFailed(albumId)} />
}

// The bar at the bottom of the page while something is queued, with the panel
// (queue, lyrics, settings) above it when that is open.
export function MiniPlayer() {
  const p = useMusicPlayer()
  const toast = useToast()
  if (!p || !p.current) return null
  const c = p.current
  const total = p.duration || c.lengthMs / 1000
  const pct = total > 0 ? Math.min(100, (p.time / total) * 100) : 0
  const repeatLabel = p.repeat === 'one' ? t('Repeat this track') : p.repeat === 'all' ? t('Repeat the queue') : t('Repeat')
  const toggleTab = (tab: PanelTab) => p.setPanel(p.panel === tab ? null : tab)
  return (
    <div className="mp-dock">
      {p.panel && <NowPlayingPanel />}
      <section className="mini-player" aria-label={t('Music player')}>
        <Cover albumId={c.albumId} />
        <div className="mp-info">
          <strong title={c.title}>{c.title}</strong>
          <span title={`${c.artist} · ${c.album}`}>{p.problem || [c.artist, c.album].filter(Boolean).join(' · ')}</span>
        </div>
        <div className="mp-controls">
          <button type="button" className="mp-btn" onClick={p.prev} aria-label={t('Previous track')} title={t('Previous track')}>
            <Icon name="skip-prev" size={18} />
          </button>
          <button type="button" className="mp-btn mp-play" onClick={p.toggle} aria-label={p.playing ? t('Pause') : t('Play')} title={p.playing ? t('Pause') : t('Play')}>
            <Icon name={p.playing ? 'pause' : 'play'} size={20} />
          </button>
          <button type="button" className="mp-btn" onClick={p.next} aria-label={t('Next track')} title={t('Next track')}>
            <Icon name="skip-next" size={18} />
          </button>
        </div>
        <div className="mp-seek">
          <span className="mp-time">{clock(p.time)}</span>
          <input
            type="range"
            min={0}
            max={Math.max(total, 1)}
            step={1}
            value={Math.min(p.time, Math.max(total, 1))}
            onChange={(e) => p.seek(Number(e.target.value))}
            aria-label={t('Seek')}
            style={{ ['--p' as string]: `${pct}%` }}
          />
          <span className="mp-time">{clock(total)}</span>
        </div>
        <div className="mp-extra">
          <button type="button" className={`mp-btn${p.shuffle ? ' on' : ''}`} onClick={() => p.setShuffle(!p.shuffle)} aria-pressed={p.shuffle} aria-label={t('Shuffle')} title={t('Shuffle')}>
            <Icon name="shuffle" size={17} />
          </button>
          <button type="button" className={`mp-btn${p.repeat !== 'off' ? ' on' : ''}`} onClick={p.cycleRepeat} aria-label={repeatLabel} title={repeatLabel}>
            <Icon name="repeat" size={17} />
            {p.repeat === 'one' && <b className="mp-one">1</b>}
          </button>
          <MusicMenu target={{ kind: 'track', track: c }} label={t('Add {title} to a playlist', { title: c.title })} className="mp-btn" direct />
          <button type="button" className={`mp-btn${p.panel === 'queue' ? ' on' : ''}`} onClick={() => toggleTab('queue')} aria-pressed={p.panel === 'queue'} aria-label={t('Queue')} title={t('Queue')}>
            <Icon name="list" size={17} />
          </button>
          <button type="button" className={`mp-btn${p.panel === 'lyrics' ? ' on' : ''}`} onClick={() => toggleTab('lyrics')} aria-pressed={p.panel === 'lyrics'} aria-label={t('Lyrics')} title={t('Lyrics')}>
            <Icon name="lyrics" size={17} />
          </button>
          {p.volumeWorks && (
            <label className="mp-vol" title={t('Volume')}>
              <Icon name={p.volume === 0 ? 'volume-off' : 'volume'} size={17} />
              <input type="range" min={0} max={1} step={0.05} value={p.volume} onChange={(e) => p.setVolume(Number(e.target.value))} aria-label={t('Volume')} style={{ ['--p' as string]: `${p.volume * 100}%` }} />
            </label>
          )}
          <button type="button" className={`mp-btn${p.panel === 'settings' ? ' on' : ''}`} onClick={() => toggleTab('settings')} aria-pressed={p.panel === 'settings'} aria-label={t('Player settings')} title={t('Player settings')}>
            <Icon name="sliders" size={16} />
          </button>
          <button
            type="button"
            className="mp-btn"
            onClick={() => {
              if (!p.popOut()) toast.error(t("The browser didn't open the window. Allow pop-ups for this site and try again."))
            }}
            aria-label={t('Open the player in its own window')}
            title={t('Open the player in its own window')}
          >
            <Icon name="external" size={16} />
          </button>
          <button type="button" className="mp-btn" onClick={p.close} aria-label={t('Close the player')} title={t('Close the player')}>
            <Icon name="x" size={17} />
          </button>
        </div>
      </section>
    </div>
  )
}
