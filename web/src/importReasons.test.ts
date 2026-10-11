import assert from 'node:assert/strict'
import { test } from 'node:test'
import { importReason } from './importReasons.ts'

// With no language chosen the words come out as written in English, so these
// check that every part is recognised and put back together in order.
test('the Prowlarr line from a real report keeps both parts', () => {
  const reason = `Mediarium's site list has no "indexer", so it stays on Prowlarr: added as Prowlarr's own feed for it, with Prowlarr's API key; switched off in Prowlarr, so added switched off`
  assert.equal(
    importReason(reason),
    'Mediarium’s site list has no “indexer”, so it stays on Prowlarr: added as Prowlarr’s own feed for it, with Prowlarr’s API key; switched off in Prowlarr, so added switched off',
  )
})

test('library lines', () => {
  assert.equal(importReason('already in your library'), 'already in your library')
  assert.equal(importReason('folder not found at /movies/X (2020)'), 'folder not found at /movies/X (2020)')
  assert.equal(importReason('no TMDB id: TMDB has no show with TVDB id 123'), 'no TMDB id: TMDB has no show with TVDB id 123')
  assert.equal(importReason('3 episode file(s) are linked where they are'), '3 episode file(s) are linked where they are')
})

test('a part it does not know is kept as written, in place', () => {
  assert.equal(importReason('added as the site itself, not through Jackett; Jackett reports an error for it: boom'), 'added as the site itself, not through Jackett; Jackett reports an error for it: boom')
  assert.equal(importReason('something new the server says'), 'something new the server says')
  assert.equal(importReason(''), '')
})
