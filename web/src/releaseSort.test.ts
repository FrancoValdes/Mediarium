// Run with: npm test (uses node's built-in test runner, no extra packages).
import assert from 'node:assert/strict'
import { test } from 'node:test'
import { filterReleases, indexersIn, qualitiesIn, resolutionOf, sortReleases, sortSubtitles, type SortableRelease } from './releaseSort.ts'

const base = { protocol: 'usenet', sizeBytes: 0 }
const list: SortableRelease[] = [
  { ...base, title: 'Film.2024.1080p.WEB-DL', indexerName: 'Beta', resolution: '1080p', sizeBytes: 8e9 },
  { ...base, title: 'Film.2024.2160p.BluRay', indexerName: 'Alpha', resolution: '2160p', sizeBytes: 40e9 },
  { ...base, title: 'Film.2024.720p.HDTV', indexerName: 'Beta', resolution: '720p', sizeBytes: 2e9, protocol: 'torrent', seeders: 50 },
  { ...base, title: 'Film.2024.CAMRip', indexerName: 'Alpha', sizeBytes: 1e9, protocol: 'torrent', seeders: 3 },
]

test('quality sorts by resolution, biggest first, and keeps ties in order', () => {
  assert.deepEqual(sortReleases(list, 'quality', -1).map((r) => resolutionOf(r)), [2160, 1080, 720, 0])
  assert.deepEqual(sortReleases(list, 'quality', 1).map((r) => resolutionOf(r)), [0, 720, 1080, 2160])
  const same = [list[0], { ...list[0], title: 'Second' }]
  assert.deepEqual(sortReleases(same, 'quality', -1).map((r) => r.title), [list[0].title, 'Second'])
})

test('size, seeders and names sort the way people read them', () => {
  assert.deepEqual(sortReleases(list, 'size', -1).map((r) => r.sizeBytes), [40e9, 8e9, 2e9, 1e9])
  // Usenet releases have no seeders and go last when the most seeded come first.
  assert.deepEqual(sortReleases(list, 'seeders', -1).map((r) => r.seeders ?? null), [50, 3, null, null])
  assert.equal(sortReleases(list, 'indexer', 1)[0].indexerName, 'Alpha')
  assert.equal(sortReleases(list, null, 1), list)
})

test('filters combine: indexer, quality and words in the title', () => {
  assert.equal(filterReleases(list, { text: '', indexer: 'Beta', quality: '' }).length, 2)
  assert.equal(filterReleases(list, { text: '', indexer: '', quality: '2160p' }).length, 1)
  assert.equal(filterReleases(list, { text: '', indexer: '', quality: 'other' }).length, 1)
  assert.equal(filterReleases(list, { text: 'film 1080P', indexer: '', quality: '' }).length, 1)
  assert.equal(filterReleases(list, { text: 'nothing like this', indexer: '', quality: '' }).length, 0)
})

test('the filter choices come from the results', () => {
  assert.deepEqual(indexersIn(list), ['Alpha', 'Beta'])
  assert.deepEqual(qualitiesIn(list), ['2160p', '1080p', '720p', 'other'])
})

test('subtitles sort by fit, rating and name; add-on and SubDL ratings count as none', () => {
  const subs = [
    { release: 'b', rating: 8, score: 70 },
    { release: 'a', rating: 5, score: 90, source: 'subdl' },
    { release: 'c', rating: 9, score: 10 },
  ]
  assert.deepEqual(sortSubtitles(subs, 'fit', -1).map((s) => s.release), ['a', 'b', 'c'])
  assert.deepEqual(sortSubtitles(subs, 'rating', -1).map((s) => s.release), ['c', 'b', 'a'])
  assert.deepEqual(sortSubtitles(subs, 'release', 1).map((s) => s.release), ['a', 'b', 'c'])
})
