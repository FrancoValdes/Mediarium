// Run with: npm test (uses node's built-in test runner, no extra packages).
import assert from 'node:assert/strict'
import { test } from 'node:test'
import type { SubtitleSourceState, SubtitleSourceStatus } from './api.ts'
import { anySourceOn, anySourceSetUp, isSearchedSource, noSourceInQuota, noSourceReason, sourceById } from './subtitleSources.ts'

const row = (id: SubtitleSourceStatus['id'], state: SubtitleSourceState): SubtitleSourceStatus => ({ id, state, enabled: state !== 'off', ready: state !== 'needs_key' })
const list = (os: SubtitleSourceState, subdl: SubtitleSourceState, addons: SubtitleSourceState) => [row('opensubtitles', os), row('subdl', subdl), row('addons', addons)]

const cases: [string, SubtitleSourceStatus[], boolean, boolean, 'none_set_up' | 'all_off' | null][] = [
  ['nothing set up', list('needs_key', 'needs_key', 'needs_key'), false, false, 'none_set_up'],
  ['only OpenSubtitles', list('on', 'needs_key', 'needs_key'), true, true, null],
  ['only SubDL', list('needs_key', 'on', 'needs_key'), true, true, null],
  ['only add-ons', list('needs_key', 'needs_key', 'on'), true, true, null],
  ['all three', list('on', 'on', 'on'), true, true, null],
  ['OpenSubtitles out of downloads still counts', list('limit', 'off', 'off'), true, true, null],
  ['everything set up is switched off', list('off', 'off', 'off'), false, true, 'all_off'],
  ['one set up and off, the others without a key', list('off', 'needs_key', 'needs_key'), false, true, 'all_off'],
]

for (const [name, sources, on, setUp, reason] of cases) {
  test(`sources: ${name}`, () => {
    assert.equal(anySourceOn(sources), on)
    assert.equal(anySourceSetUp(sources), setUp)
    assert.equal(noSourceReason(sources), reason)
  })
}

test('sources: no opinion before the list has loaded', () => {
  assert.equal(anySourceOn(undefined), false)
  assert.equal(noSourceReason(undefined), null)
  assert.equal(noSourceReason([]), null)
})

test('sources: says whether one source is searched', () => {
  const sources = list('limit', 'off', 'needs_key')
  assert.equal(isSearchedSource(sources, 'opensubtitles'), true)
  assert.equal(isSearchedSource(sources, 'subdl'), false)
  assert.equal(isSearchedSource(sources, 'addons'), false)
  assert.equal(isSearchedSource(undefined, 'subdl'), false)
})

test('sources: finds a source by id', () => {
  assert.equal(sourceById(list('on', 'off', 'needs_key'), 'subdl')?.state, 'off')
  assert.equal(sourceById(undefined, 'subdl'), undefined)
})

test('sources: only an explicit "no source on" from the server blocks the buttons', () => {
  assert.equal(noSourceInQuota(null), false)
  assert.equal(noSourceInQuota({}), false) // an older server that does not say
  assert.equal(noSourceInQuota({ hasSource: true }), false)
  assert.equal(noSourceInQuota({ hasSource: false }), true)
})
