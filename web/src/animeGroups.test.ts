// Run with: npm test (uses node's built-in test runner, no extra packages).
import assert from 'node:assert/strict'
import { test } from 'node:test'
import { ANIME_GROUP_TERMS, addAnimeTerms } from './animeGroups.ts'

test('the anime terms are added to an empty box, one per line', () => {
  const out = addAnimeTerms('')
  assert.equal(out.split('\n').length, ANIME_GROUP_TERMS.length)
  assert.ok(out.startsWith('SubsPlease = 40'))
})

test('terms already there are kept as they are, with their own score', () => {
  const out = addAnimeTerms('x265 = 50\nsubsplease = 100\n')
  const lines = out.split('\n')
  assert.equal(lines[0], 'x265 = 50')
  assert.equal(lines[1], 'subsplease = 100')
  assert.equal(lines.filter((l) => l.toLowerCase().startsWith('subsplease')).length, 1)
  assert.equal(lines.length, 2 + ANIME_GROUP_TERMS.length - 1)
})

test('pressing the button twice changes nothing the second time', () => {
  const once = addAnimeTerms('REPACK = 10')
  assert.equal(addAnimeTerms(once), once)
})

test('every term fits the limits the server sets', () => {
  for (const { term, score } of ANIME_GROUP_TERMS) {
    assert.ok(`${term} = ${score}`.length <= 60)
    assert.ok(!term.includes('|'))
    assert.ok(Number.isInteger(score) && Math.abs(score) <= 10000)
  }
})
