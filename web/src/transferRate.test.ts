// Run with: npm test (uses node's built-in test runner, no extra packages).
import assert from 'node:assert/strict'
import { test } from 'node:test'
import { etaText, nextSample } from './transferRate.ts'

test('the speed comes from two samples and is smoothed afterwards', () => {
  let s = nextSample(null, 0, 0)
  assert.equal(s.rate, 0)
  s = nextSample(s, 10_000_000, 2000)
  assert.equal(s.rate, 5_000_000)
  s = nextSample(s, 30_000_000, 4000) // 10 MB/s now, 5 before
  assert.equal(Math.round(s.rate), 7_000_000)
})

test('a refresh a moment later changes nothing, and going backwards starts over', () => {
  const s = nextSample(nextSample(null, 0, 0), 4_000_000, 2000)
  assert.equal(nextSample(s, 4_500_000, 2300), s)
  assert.equal(nextSample(s, 1_000_000, 9000).rate, 0)
})

test('a download that sat still for a minute has no speed', () => {
  const s = nextSample(nextSample(null, 0, 0), 4_000_000, 2000)
  assert.equal(nextSample(s, 4_000_000, 70_000).rate, 0)
  assert.equal(nextSample(s, 4_000_000, 20_000).rate, s.rate)
})

test('time left is written the way people say it', () => {
  assert.equal(etaText(0), '')
  assert.equal(etaText(NaN), '')
  assert.equal(etaText(45), '45 s')
  assert.equal(etaText(300), '5 min')
  assert.equal(etaText(7200), '2 h')
  assert.equal(etaText(5400 + 600), '1 h 40 min')
})
