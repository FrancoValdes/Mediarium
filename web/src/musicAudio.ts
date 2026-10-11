// The maths behind the player's sound: ReplayGain, crossfades and when a play
// counts as a listen. Plain functions with no browser in them, so they can be
// tested.

import type { GainMode } from './musicQueue.ts'

// The time of a track as m:ss.
export function clock(seconds: number): string {
  if (!Number.isFinite(seconds) || seconds < 0) return '0:00'
  const s = Math.floor(seconds)
  return `${Math.floor(s / 60)}:${String(s % 60).padStart(2, '0')}`
}

// ---- ReplayGain ---------------------------------------------------------------

// What a track's tags say (the server sends null for what is missing).
export interface TrackGain {
  trackGain: number | null
  trackPeak: number | null
  albumGain: number | null
  albumPeak: number | null
}

export const NO_GAIN: TrackGain = { trackGain: null, trackPeak: null, albumGain: null, albumPeak: null }

const MAX_BOOST = 4 // +12 dB, whatever a tag claims

// gainFactor is the number the track's samples are multiplied by.
//
// With "track" the track gain is used and the album gain only when there is no
// track gain; with "album" the other way round. Peaks come from the same
// source as the gain they go with. The factor is then held back so the loudest
// sample cannot go past full scale: factor * peak <= 1. A peak that isn't
// known counts as full scale, so a track without one is never made louder.
// Nothing is changed for a file without ReplayGain tags.
export function gainFactor(mode: GainMode, g: TrackGain | null | undefined): number {
  if (mode === 'off' || !g) return 1
  const useAlbum = mode === 'album'
  let db: number | null
  let peak: number | null
  if (useAlbum && g.albumGain !== null) {
    db = g.albumGain
    peak = g.albumPeak
  } else if (g.trackGain !== null) {
    db = g.trackGain
    peak = g.trackPeak
  } else if (g.albumGain !== null) {
    db = g.albumGain
    peak = g.albumPeak
  } else {
    return 1
  }
  if (!Number.isFinite(db)) return 1
  let factor = 10 ** (db / 20)
  const p = peak !== null && Number.isFinite(peak) && peak > 0 ? peak : 1
  if (factor * p > 1) factor = 1 / p
  return Math.min(Math.max(factor, 0), MAX_BOOST)
}

// ---- Crossfade ------------------------------------------------------------------

export interface FadeTrack {
  albumId: number
  lengthMs: number
}

// crossfadeFor is how many seconds the end of one track and the start of the
// next overlap. Tracks of the same album run straight into each other (live
// albums and mixes are made to), and a short track never gives up more than
// 40% of itself.
export function crossfadeFor(setting: number, current: FadeTrack | null, next: FadeTrack | null): number {
  if (!(setting > 0) || !current || !next) return 0
  if (current.albumId === next.albumId) return 0
  let s = setting
  for (const t of [current, next]) {
    if (t.lengthMs > 0) s = Math.min(s, (t.lengthMs / 1000) * 0.4)
  }
  return s >= 0.5 ? s : 0
}

// fadeCurves are the two gain curves of an equal-power crossfade: the sum of
// their squares is 1 all the way through, so the loudness stays even instead of
// dipping in the middle. Both have `steps` points from the start to the end.
export function fadeCurves(steps = 64): { fadeIn: Float32Array; fadeOut: Float32Array } {
  const n = Math.max(2, Math.floor(steps))
  const fadeIn = new Float32Array(n)
  const fadeOut = new Float32Array(n)
  for (let i = 0; i < n; i++) {
    const x = i / (n - 1)
    fadeIn[i] = Math.sin((x * Math.PI) / 2)
    fadeOut[i] = Math.cos((x * Math.PI) / 2)
  }
  return { fadeIn, fadeOut }
}

// ---- Counting a listen ------------------------------------------------------------

// The rule of ListenBrainz: a play counts once half the track or four minutes
// of it has been heard, whichever is less.
export function listenThreshold(durationSec: number): number {
  if (!(durationSec > 0)) return 240
  return Math.min(durationSec / 2, 240)
}

// ListenTracker adds up how long a track has really been heard. Jumping around
// in the track does not count as listening: only steps of normal playback do.
export class ListenTracker {
  private last = -1
  private heard = 0
  private duration = 0
  private startedAt = 0
  counted = false

  start(durationSec: number, nowMs: number): void {
    this.last = -1
    this.heard = 0
    this.duration = durationSec
    this.startedAt = nowMs
    this.counted = false
  }

  setDuration(durationSec: number): void {
    if (durationSec > 0) this.duration = durationSec
  }

  // tick is called with the position of the track as it plays. It returns true
  // once, at the moment the play starts to count.
  tick(position: number): boolean {
    if (this.last >= 0) {
      const step = position - this.last
      if (step > 0 && step <= 2.5) this.heard += step
    }
    this.last = position
    if (!this.counted && this.heard >= listenThreshold(this.duration)) {
      this.counted = true
      return true
    }
    return false
  }

  // seeked forgets the last position, so a jump is not counted as time heard.
  seeked(): void {
    this.last = -1
  }

  get heardSeconds(): number {
    return this.heard
  }

  // startedAtSeconds is the Unix time the track started, as ListenBrainz wants it.
  get startedAtSeconds(): number {
    return Math.floor(this.startedAt / 1000)
  }
}
