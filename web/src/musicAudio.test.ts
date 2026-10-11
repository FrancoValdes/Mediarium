// Run with: npm test
import assert from 'node:assert/strict'
import { test } from 'node:test'
import { crossfadeFor, fadeCurves, gainFactor, ListenTracker, listenThreshold, NO_GAIN, type TrackGain } from './musicAudio.ts'

const near = (a: number, b: number, eps = 1e-6) => assert.ok(Math.abs(a - b) < eps, `${a} is not ${b}`)
const gain = (over: Partial<TrackGain>): TrackGain => ({ ...NO_GAIN, ...over })

test('ReplayGain is left alone when it is off or the tags are missing', () => {
  assert.equal(gainFactor('off', gain({ trackGain: -6 })), 1)
  assert.equal(gainFactor('track', null), 1)
  assert.equal(gainFactor('track', NO_GAIN), 1)
  assert.equal(gainFactor('album', NO_GAIN), 1)
})

test('track mode uses the track gain, album mode the album gain, each falling back to the other', () => {
  const g = gain({ trackGain: -6, trackPeak: 0.5, albumGain: -3, albumPeak: 0.5 })
  near(gainFactor('track', g), 10 ** (-6 / 20))
  near(gainFactor('album', g), 10 ** (-3 / 20))
  near(gainFactor('album', gain({ trackGain: -6 })), 10 ** (-6 / 20))
  near(gainFactor('track', gain({ albumGain: -4 })), 10 ** (-4 / 20))
})

test('quiet tracks are lifted, but never past full scale', () => {
  // +6 dB is x2; a peak of 0.4 leaves room for it ...
  near(gainFactor('track', gain({ trackGain: 6, trackPeak: 0.4 })), 10 ** (6 / 20))
  // ... a peak of 0.9 does not: held back to 1/0.9.
  near(gainFactor('track', gain({ trackGain: 6, trackPeak: 0.9 })), 1 / 0.9)
  // A peak of 1.0 or more allows no boost at all.
  near(gainFactor('track', gain({ trackGain: 3, trackPeak: 1 })), 1)
  near(gainFactor('track', gain({ trackGain: 3, trackPeak: 1.2 })), 1 / 1.2)
  // No peak known: treated as full scale, so a boost is refused.
  near(gainFactor('track', gain({ trackGain: 5 })), 1)
  // Cutting never needs a peak.
  near(gainFactor('track', gain({ trackGain: -9 })), 10 ** (-9 / 20))
})

test('loud tracks are turned down and the result is always usable', () => {
  assert.ok(gainFactor('track', gain({ trackGain: -60 })) > 0)
  assert.ok(gainFactor('track', gain({ trackGain: 80, trackPeak: 0.0001 })) <= 4, 'a silly tag cannot blow up the volume')
  assert.equal(gainFactor('track', gain({ trackGain: Number.NaN })), 1)
  assert.equal(gainFactor('track', gain({ trackGain: -6, trackPeak: -2 })) > 0, true)
})

test('crossfade length', () => {
  const a = { albumId: 1, lengthMs: 240000 }
  const b = { albumId: 2, lengthMs: 200000 }
  assert.equal(crossfadeFor(0, a, b), 0, 'off')
  assert.equal(crossfadeFor(6, a, b), 6)
  assert.equal(crossfadeFor(6, a, { albumId: 1, lengthMs: 200000 }), 0, 'the same album plays on without a fade')
  assert.equal(crossfadeFor(6, a, null), 0, 'nothing after it')
  assert.equal(crossfadeFor(6, null, b), 0)
  near(crossfadeFor(12, a, { albumId: 3, lengthMs: 10000 }), 4) // 40% of a 10 s track
  assert.equal(crossfadeFor(6, a, { albumId: 3, lengthMs: 1000 }), 0, 'too short to fade')
  assert.equal(crossfadeFor(6, a, { albumId: 3, lengthMs: 0 }), 6, 'unknown length is not a limit')
})

test('crossfade curves keep the loudness even', () => {
  const { fadeIn, fadeOut } = fadeCurves(65)
  assert.equal(fadeIn.length, 65)
  near(fadeIn[0], 0)
  near(fadeOut[0], 1)
  near(fadeIn[64], 1)
  near(fadeOut[64], 0, 1e-6)
  for (let i = 0; i < 65; i++) near(fadeIn[i] ** 2 + fadeOut[i] ** 2, 1, 1e-5)
  for (let i = 1; i < 65; i++) assert.ok(fadeIn[i] >= fadeIn[i - 1] && fadeOut[i] <= fadeOut[i - 1])
  assert.equal(fadeCurves(0).fadeIn.length, 2, 'at least two points')
})

test('when a play counts as a listen', () => {
  assert.equal(listenThreshold(200), 100)
  assert.equal(listenThreshold(1000), 240)
  assert.equal(listenThreshold(480), 240)
  assert.equal(listenThreshold(0), 240, 'unknown length: four minutes')
  assert.equal(listenThreshold(Number.NaN), 240)
})

test('the listen tracker counts time played, not jumps', () => {
  const t = new ListenTracker()
  t.start(100, 1_700_000_000_000)
  assert.equal(t.startedAtSeconds, 1_700_000_000)
  // Four ticks a second of normal playback up to 40 s.
  let counted = 0
  for (let s = 0; s <= 40; s += 0.25) if (t.tick(s)) counted += 1
  near(t.heardSeconds, 40)
  assert.equal(counted, 0)
  // Skipping ahead to 90 s is not listening.
  t.seeked()
  t.tick(90)
  near(t.heardSeconds, 40)
  // Ten more seconds reach half of 100 s: counts once, only once.
  const fired: number[] = []
  for (let s = 90.25; s <= 100; s += 0.25) if (t.tick(s)) fired.push(s)
  assert.equal(fired.length, 1)
  near(fired[0], 100, 0.3)
  assert.equal(t.counted, true)
  assert.equal(t.tick(100.25), false)
})

test('a seek without telling the tracker is still not counted, and a pause is not either', () => {
  const t = new ListenTracker()
  t.start(60, 0)
  t.tick(0)
  t.tick(0.25)
  t.tick(50) // a jump of 50 s in one tick
  near(t.heardSeconds, 0.25)
  t.tick(40) // backwards
  near(t.heardSeconds, 0.25)
  t.setDuration(0) // a bad duration is ignored
  t.setDuration(30)
  assert.equal(t.tick(40.5) || t.tick(41), false)
})

test('starting again resets the tracker', () => {
  const t = new ListenTracker()
  t.start(10, 0)
  for (let s = 0; s <= 6; s += 0.5) t.tick(s)
  assert.equal(t.counted, true)
  t.start(10, 5000)
  assert.equal(t.counted, false)
  assert.equal(t.heardSeconds, 0)
})
