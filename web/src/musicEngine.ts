// The sound side of the music player: two audio elements that take turns, so the
// next track is already loaded when the current one ends (near-gapless), and
// an optional Web Audio graph for ReplayGain and crossfades.
//
// How it works, and why it is built this way:
//
// * Two <audio> elements ("decks"). While one plays, the other loads the next
//   track. When the playing one is about to end, the other is started and the
//   first is stopped. A file is streamed, not decoded into memory, so a long
//   FLAC costs nothing. The start of the second deck is not sample-exact, so
//   a gap of a few milliseconds is possible; the other way (decoding whole
//   files into buffers and scheduling them on the audio clock) would hold
//   hundreds of MB per track.
// * The Web Audio graph (element -> gain for ReplayGain -> gain for the fade ->
//   master volume -> speakers) is only built when ReplayGain or a crossfade is
//   switched on. Once an element is connected to a graph its sound only comes
//   out of the graph. Safari on iPhone and iPad has had bugs where Web Audio
//   goes quiet when the screen locks, and ignores element.volume altogether, so
//   a plain element is the safer default there.
// * Crossfade needs the graph (two gains moving at once). Without it the
//   player still plays gapless.

import { fadeCurves } from './musicAudio.ts'

export interface EngineEvents {
  time(position: number, duration: number): void
  playing(isPlaying: boolean): void
  duration(seconds: number): void
  // The engine moved on to the track that was loaded ahead of time.
  advanced(): void
  // The track ended and nothing was loaded ahead.
  ended(): void
  // The current track can't be played.
  failed(): void
}

export interface LoadOptions {
  startAt?: number
  autoplay?: boolean
  gain?: number
}

// What is loaded ahead: its address, its ReplayGain factor, and how many
// seconds it overlaps the end of the current track (0 = straight after).
export interface NextTrack {
  url: string
  gain: number
  fade: number
}

interface Deck {
  el: HTMLAudioElement
  url: string
  gain: number
  failed: boolean
  rg: GainNode | null
  fade: GainNode | null
}

// How early before the end of a track the next deck is started when there is
// no crossfade, in seconds. Starting an element takes a few milliseconds.
const GAPLESS_LEAD = 0.03

type AudioContextCtor = typeof AudioContext

function contextClass(): AudioContextCtor | null {
  if (typeof window === 'undefined') return null
  const w = window as unknown as { AudioContext?: AudioContextCtor; webkitAudioContext?: AudioContextCtor }
  return w.AudioContext ?? w.webkitAudioContext ?? null
}

export class AudioEngine {
  private decks: Deck[]
  private active = 0
  private ctx: AudioContext | null = null
  private master: GainNode | null = null
  private volume = 0.9
  private next: NextTrack | null = null
  private deferredNext: { value: NextTrack | null } | null = null
  private fading = false
  private fadeTimer: number | undefined
  private armTimer: number | undefined
  private startAt = 0
  private wantsPlay = false
  private handoffTimer: number | undefined
  readonly volumeWorks: boolean

  private events: EngineEvents

  constructor(events: EngineEvents) {
    this.events = events
    this.decks = [this.makeDeck(), this.makeDeck()]
    this.volumeWorks = this.testVolume()
    if (typeof document !== 'undefined') {
      document.addEventListener('visibilitychange', () => {
        if (document.visibilityState === 'visible') this.resumeContext()
      })
    }
  }

  private testVolume(): boolean {
    try {
      const a = new Audio()
      a.volume = 0.5
      return a.volume === 0.5
    } catch {
      return true
    }
  }

  private makeDeck(): Deck {
    const el = new Audio()
    el.preload = 'auto'
    const deck: Deck = { el, url: '', gain: 1, failed: false, rg: null, fade: null }
    const mine = () => this.decks[this.active] === deck
    el.addEventListener('timeupdate', () => {
      if (!mine()) return
      this.events.time(el.currentTime, Number.isFinite(el.duration) ? el.duration : 0)
      this.arm()
    })
    el.addEventListener('playing', () => {
      if (mine()) {
        this.events.playing(true)
        this.arm()
      }
    })
    el.addEventListener('play', () => {
      if (mine()) this.events.playing(true)
    })
    el.addEventListener('pause', () => {
      if (mine()) this.events.playing(false)
    })
    el.addEventListener('seeked', () => {
      if (mine()) this.arm()
    })
    el.addEventListener('durationchange', () => {
      if (mine() && Number.isFinite(el.duration)) {
        this.events.duration(el.duration)
        this.arm()
      }
    })
    el.addEventListener('loadedmetadata', () => {
      if (!mine()) return
      if (Number.isFinite(el.duration)) this.events.duration(el.duration)
      if (this.startAt > 0) {
        el.currentTime = this.startAt
        this.startAt = 0
      }
    })
    el.addEventListener('canplay', () => {
      if (!mine()) this.arm() // the track loaded ahead is ready
    })
    el.addEventListener('ended', () => {
      if (!mine()) return
      if (this.nextReady()) this.transition()
      else this.events.ended()
    })
    el.addEventListener('error', () => {
      if (mine()) {
        this.events.playing(false)
        this.events.failed()
      } else if (el.getAttribute('src')) {
        deck.failed = true // the track loaded ahead won't play; the normal path will say so when its turn comes
      }
    })
    return deck
  }

  private get cur(): Deck {
    return this.decks[this.active]
  }

  private get idle(): Deck {
    return this.decks[1 - this.active]
  }

  // ---- The graph ---------------------------------------------------------------------

  // setProcessing says whether ReplayGain or a crossfade is wanted. The graph
  // is built the first time it is, and stays.
  setProcessing(on: boolean): void {
    if (on) this.ensureGraph()
  }

  get graphActive(): boolean {
    return this.ctx !== null
  }

  private ensureGraph(): boolean {
    if (this.ctx) return true
    const Ctor = contextClass()
    if (!Ctor) return false
    try {
      const ctx = new Ctor()
      const master = ctx.createGain()
      master.gain.value = this.volume
      master.connect(ctx.destination)
      for (const d of this.decks) {
        const src = ctx.createMediaElementSource(d.el)
        const rg = ctx.createGain()
        const fade = ctx.createGain()
        rg.gain.value = d.gain
        src.connect(rg)
        rg.connect(fade)
        fade.connect(master)
        d.rg = rg
        d.fade = fade
        d.el.volume = 1
      }
      this.ctx = ctx
      this.master = master
      return true
    } catch {
      this.ctx = null
      return false
    }
  }

  private resumeContext(): void {
    const c = this.ctx
    if (c && c.state !== 'running' && this.wantsPlay) void c.resume().catch(() => undefined)
  }

  // ---- Volume and gain -------------------------------------------------------------------

  setVolume(v: number): void {
    this.volume = Math.min(Math.max(v, 0), 1)
    if (this.master && this.ctx) {
      this.master.gain.setTargetAtTime(this.volume, this.ctx.currentTime, 0.01)
    } else {
      for (const d of this.decks) this.syncElementVolume(d)
    }
  }

  // Without a graph the best that can be done for a ReplayGain cut is the
  // element's own volume (a boost is not possible there).
  private syncElementVolume(d: Deck): void {
    if (d.rg) return
    try {
      d.el.volume = Math.min(Math.max(this.volume * Math.min(d.gain, 1), 0), 1)
    } catch {
      // A browser that refuses (iPhone) plays at the hardware volume.
    }
  }

  private setDeckGain(d: Deck, factor: number): void {
    d.gain = factor
    if (d.rg && this.ctx) d.rg.gain.setTargetAtTime(factor, this.ctx.currentTime, 0.015)
    else this.syncElementVolume(d)
  }

  // setCurrentGain changes the ReplayGain factor of the track that is playing.
  setCurrentGain(factor: number): void {
    this.setDeckGain(this.cur, factor)
  }

  // ---- Playing ---------------------------------------------------------------------------------

  // load starts a track on the playing deck, ending any crossfade in progress.
  load(url: string, opts: LoadOptions = {}): void {
    this.endFade()
    window.clearTimeout(this.armTimer)
    const d = this.cur
    d.failed = false
    d.url = url
    this.startAt = opts.startAt && opts.startAt > 0 ? opts.startAt : 0
    this.wantsPlay = opts.autoplay !== false
    if (d.fade) d.fade.gain.value = 1
    this.setDeckGain(d, opts.gain ?? 1)
    d.el.src = url
    d.el.load()
    // A browser that wants a click first refuses; the play button then still works.
    if (this.wantsPlay) this.play().catch(() => undefined)
  }

  async play(): Promise<void> {
    this.wantsPlay = true
    if (this.ctx && this.ctx.state !== 'running') {
      try {
        await this.ctx.resume()
      } catch {
        // The browser may refuse until a click; the element still plays.
      }
    }
    try {
      await this.cur.el.play()
    } catch (e) {
      // A load that was replaced by another one is not a problem.
      if (e instanceof DOMException && e.name === 'AbortError') return
      this.events.playing(false)
      throw e
    }
  }

  pause(): void {
    this.wantsPlay = false
    this.endFade()
    window.clearTimeout(this.armTimer)
    this.cur.el.pause()
  }

  get paused(): boolean {
    return this.cur.el.paused
  }

  get position(): number {
    return this.cur.el.currentTime
  }

  seek(seconds: number): void {
    if (!Number.isFinite(seconds)) return
    this.endFade()
    this.cur.el.currentTime = Math.max(0, seconds)
    this.arm()
  }

  stop(): void {
    this.wantsPlay = false
    this.endFade()
    window.clearTimeout(this.armTimer)
    for (const d of this.decks) {
      d.el.pause()
      d.el.removeAttribute('src')
      d.el.load()
      d.url = ''
      d.failed = false
    }
    this.next = null
    this.deferredNext = null
  }

  // ---- The track after this one -----------------------------------------------------------

  // setNext loads the next track on the idle deck ahead of time (null: none).
  setNext(n: NextTrack | null): void {
    if (this.fading) {
      this.deferredNext = { value: n }
      return
    }
    this.next = n
    const d = this.idle
    if (n === null) {
      if (d.url) {
        d.el.pause()
        d.el.removeAttribute('src')
        d.el.load()
        d.url = ''
      }
      d.failed = false
      return
    }
    if (d.url !== n.url) {
      d.failed = false
      d.url = n.url
      d.el.src = n.url
      d.el.load()
    }
    this.setDeckGain(d, n.gain)
    this.arm()
  }

  // holdNext stops the loaded-ahead track from starting by itself, without
  // unloading it. It is used while the plan for what comes next is being made
  // again; setNext brings it back (and reuses the loaded track if it is the same).
  holdNext(): void {
    if (this.fading) return
    this.next = null
    window.clearTimeout(this.armTimer)
  }

  private nextReady(): boolean {
    const n = this.next
    const d = this.idle
    return !!n && d.url === n.url && !d.failed && d.el.readyState >= 3
  }

  // arm works out when the next track has to start and sets a timer for it.
  // Timers of a tab that is playing sound are not slowed down by the browser.
  private arm(): void {
    window.clearTimeout(this.armTimer)
    const n = this.next
    const a = this.cur.el
    if (!n || this.fading || a.paused || !this.nextReady()) return
    const dur = a.duration
    if (!Number.isFinite(dur) || dur <= 0) return
    const lead = n.fade > 0 && this.ctx ? n.fade : GAPLESS_LEAD
    const wait = (dur - a.currentTime - lead) * 1000
    if (wait <= 15) {
      this.transition()
      return
    }
    this.armTimer = window.setTimeout(() => this.arm(), Math.min(wait, 500))
  }

  private transition(): void {
    const n = this.next
    if (!n || this.fading) return
    window.clearTimeout(this.armTimer)
    const from = this.cur
    const to = this.idle
    const len = n.fade > 0 && this.ctx && from.fade && to.fade ? n.fade : 0
    this.next = null
    this.active = 1 - this.active
    this.startAt = 0
    to.el.currentTime = 0
    if (len > 0 && this.ctx && from.fade && to.fade) {
      const now = this.ctx.currentTime
      const { fadeIn, fadeOut } = fadeCurves(64)
      for (const [g, curve] of [[to.fade.gain, fadeIn], [from.fade.gain, fadeOut]] as const) {
        g.cancelScheduledValues(now)
        g.setValueAtTime(curve[0], now)
        g.setValueCurveAtTime(curve, now, len)
      }
      this.fading = true
      this.fadeTimer = window.setTimeout(() => this.endFade(), len * 1000 + 200)
      void to.el.play().catch(() => undefined)
    } else {
      const started = () => {
        window.clearTimeout(this.handoffTimer)
        from.el.pause()
        from.url = ''
        // The track after the new one may have been asked for while this deck was still busy.
        if (this.next && !this.fading) this.setNext(this.next)
      }
      to.el.addEventListener('playing', started, { once: true })
      this.handoffTimer = window.setTimeout(started, 150)
      void to.el.play().catch(() => undefined)
    }
    this.events.advanced()
  }

  // endFade finishes a crossfade now: the old deck stops, and the new one is at
  // full level.
  private endFade(): void {
    window.clearTimeout(this.fadeTimer)
    if (!this.fading) return
    this.fading = false
    const old = this.idle
    old.el.pause()
    for (const d of this.decks) {
      if (d.fade && this.ctx) {
        d.fade.gain.cancelScheduledValues(this.ctx.currentTime)
        d.fade.gain.value = 1
      }
    }
    old.el.removeAttribute('src')
    old.el.load()
    old.url = ''
    if (this.deferredNext) {
      const v = this.deferredNext.value
      this.deferredNext = null
      this.setNext(v)
    }
  }
}
