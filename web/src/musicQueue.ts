// The music player's queue as plain functions, so the rules (play next, move,
// remove, what comes after this track, what is kept between visits) can be
// tested without a browser. Nothing in here touches the page.

export interface PlayerTrack {
  id: number
  title: string
  artist: string
  album: string
  albumId: number
  lengthMs: number
}

// A place in the queue. The same song can be queued twice, so every place has
// an id of its own.
export interface QueuedTrack extends PlayerTrack {
  uid: string
}

export type Repeat = 'off' | 'all' | 'one'

let counter = 0

export function newUid(): string {
  counter += 1
  return `q${Date.now().toString(36)}${counter.toString(36)}`
}

export function withUids(tracks: PlayerTrack[]): QueuedTrack[] {
  return tracks.map((t) => ({ ...t, uid: newUid() }))
}

export interface QueueState {
  queue: QueuedTrack[]
  index: number // -1 when nothing is queued
}

const clamp = (n: number, lo: number, hi: number) => Math.min(Math.max(n, lo), hi)

// Put tracks right after the one playing (after the ones already added that
// way, in the order given, is the caller's business: this always goes straight
// after the current track).
export function playNext(s: QueueState, tracks: PlayerTrack[]): QueueState {
  if (tracks.length === 0) return s
  if (s.queue.length === 0 || s.index < 0) return { queue: withUids(tracks), index: 0 }
  const at = s.index + 1
  return { queue: [...s.queue.slice(0, at), ...withUids(tracks), ...s.queue.slice(at)], index: s.index }
}

export function addToEnd(s: QueueState, tracks: PlayerTrack[]): QueueState {
  if (tracks.length === 0) return s
  if (s.queue.length === 0 || s.index < 0) return { queue: withUids(tracks), index: 0 }
  return { queue: [...s.queue, ...withUids(tracks)], index: s.index }
}

// Move the track at `from` to position `to` (both places in the queue as it is
// now). The track that is playing keeps playing, wherever it ends up.
export function moveTrack(s: QueueState, from: number, to: number): QueueState {
  const n = s.queue.length
  if (from < 0 || from >= n || to < 0 || to >= n || from === to) return s
  const queue = s.queue.slice()
  const [item] = queue.splice(from, 1)
  queue.splice(to, 0, item)
  let index = s.index
  if (s.index === from) index = to
  else if (from < s.index && to >= s.index) index = s.index - 1
  else if (from > s.index && to <= s.index) index = s.index + 1
  return { queue, index }
}

export interface RemoveResult extends QueueState {
  // The track that was playing was removed: the player has to load whatever is
  // at `index` now (or stop when the queue is empty).
  currentRemoved: boolean
}

export function removeTrack(s: QueueState, at: number): RemoveResult {
  if (at < 0 || at >= s.queue.length) return { ...s, currentRemoved: false }
  const queue = s.queue.filter((_, i) => i !== at)
  if (queue.length === 0) return { queue, index: -1, currentRemoved: s.index === at }
  if (at < s.index) return { queue, index: s.index - 1, currentRemoved: false }
  if (at > s.index) return { queue, index: s.index, currentRemoved: false }
  return { queue, index: Math.min(at, queue.length - 1), currentRemoved: true }
}

// Empty the queue, or empty it except for the track that is playing.
export function clearQueue(s: QueueState, keepCurrent: boolean): QueueState {
  if (keepCurrent && s.index >= 0 && s.index < s.queue.length) return { queue: [s.queue[s.index]], index: 0 }
  return { queue: [], index: -1 }
}

// A number in [0, 1) from the browser's secure generator (a shuffle doesn't need
// one, but nothing here should depend on a weak one either).
function secureRandom(): number {
  const a = new Uint32Array(1)
  globalThis.crypto.getRandomValues(a)
  return a[0] / 4294967296
}

// Which place plays after the current one: -1 for none (the end of the queue
// with repeat off). Shuffle picks any other place at random; `random` gives a
// number in [0, 1) so tests can steer it.
export function planNext(queueLength: number, index: number, shuffle: boolean, repeat: Repeat, random: () => number = secureRandom): number {
  if (queueLength === 0 || index < 0) return -1
  if (repeat === 'one') return index
  if (shuffle && queueLength > 1) {
    let n = Math.floor(random() * (queueLength - 1))
    if (n >= index) n += 1 // skips the current place without retrying
    return clamp(n, 0, queueLength - 1)
  }
  if (index + 1 < queueLength) return index + 1
  return repeat === 'all' ? 0 : -1
}

// ---- Kept between visits ----------------------------------------------------

export const QUEUE_STORAGE_KEY = 'mediarium-music-queue'
const MAX_KEPT = 1000

export interface SavedQueue {
  queue: PlayerTrack[]
  index: number
  time: number
  shuffle: boolean
  repeat: Repeat
}

export function serializeQueue(s: SavedQueue): string {
  const queue = s.queue.slice(0, MAX_KEPT).map(({ id, title, artist, album, albumId, lengthMs }) => ({ id, title, artist, album, albumId, lengthMs }))
  const cut = s.queue.length > MAX_KEPT && s.index >= MAX_KEPT
  return JSON.stringify({ v: 1, queue, index: cut ? -1 : s.index, time: Math.max(0, Math.floor(s.time)), shuffle: s.shuffle, repeat: s.repeat })
}

const isNum = (x: unknown): x is number => typeof x === 'number' && Number.isFinite(x)
const isStr = (x: unknown): x is string => typeof x === 'string'

// Reads what serializeQueue wrote. Anything that doesn't look right gives null,
// and tracks with missing fields are dropped, so damaged data never breaks the
// player.
export function parseQueue(raw: string | null): SavedQueue | null {
  if (!raw) return null
  let data: unknown
  try {
    data = JSON.parse(raw)
  } catch {
    return null
  }
  if (!data || typeof data !== 'object') return null
  const d = data as Record<string, unknown>
  if (d.v !== 1 || !Array.isArray(d.queue)) return null
  const keep: PlayerTrack[] = []
  const saved = isNum(d.index) ? d.index : -1
  let index = 0 // where the track that was playing ended up; the first one when it is gone
  d.queue.slice(0, MAX_KEPT).forEach((x, i) => {
    const t = x as Record<string, unknown> | null
    if (t && isNum(t.id) && t.id > 0 && isStr(t.title) && isNum(t.albumId)) {
      keep.push({ id: t.id, title: t.title, artist: isStr(t.artist) ? t.artist : '', album: isStr(t.album) ? t.album : '', albumId: t.albumId, lengthMs: isNum(t.lengthMs) ? t.lengthMs : 0 })
      if (i === saved) index = keep.length - 1
    }
  })
  if (keep.length === 0) return null
  return {
    queue: keep,
    index,
    time: isNum(d.time) && d.time > 0 ? d.time : 0,
    shuffle: d.shuffle === true,
    repeat: d.repeat === 'all' || d.repeat === 'one' ? d.repeat : 'off',
  }
}

// ---- Settings kept on this device ----------------------------------------------

export type GainMode = 'off' | 'track' | 'album'

export interface PlayerSettings {
  crossfade: number // seconds, 0 = off
  replayGain: GainMode
}

export const DEFAULT_SETTINGS: PlayerSettings = { crossfade: 0, replayGain: 'off' }
export const MAX_CROSSFADE = 12
export const SETTINGS_STORAGE_KEY = 'mediarium-music-settings'

export function parseSettings(raw: string | null): PlayerSettings {
  if (!raw) return DEFAULT_SETTINGS
  try {
    const d = JSON.parse(raw) as Record<string, unknown>
    const cf = isNum(d.crossfade) ? Math.round(clamp(d.crossfade, 0, MAX_CROSSFADE)) : 0
    const rg: GainMode = d.replayGain === 'track' || d.replayGain === 'album' ? d.replayGain : 'off'
    return { crossfade: cf, replayGain: rg }
  } catch {
    return DEFAULT_SETTINGS
  }
}
