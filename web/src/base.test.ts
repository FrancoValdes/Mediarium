import assert from 'node:assert/strict'
import { test } from 'node:test'
import { cleanBase, prefixed, rebasePictures, unprefixed, BASE_PATH, withBase } from './base.ts'

test('cleanBase keeps a path prefix and drops anything else', () => {
  assert.equal(cleanBase('/mediarium'), '/mediarium')
  assert.equal(cleanBase(' /mediarium/ '), '/mediarium')
  assert.equal(cleanBase('/apps/media///'), '/apps/media')
  assert.equal(cleanBase(''), '')
  assert.equal(cleanBase('/'), '')
  assert.equal(cleanBase(null), '')
  assert.equal(cleanBase(undefined), '')
  assert.equal(cleanBase('mediarium'), '')
  assert.equal(cleanBase('https://evil.example/x'), '')
})

test('prefixed adds the prefix to addresses on this server only', () => {
  assert.equal(prefixed('/m', '/api/version'), '/m/api/version')
  assert.equal(prefixed('/m', '/'), '/m/')
  assert.equal(prefixed('/m', '/push-sw.js'), '/m/push-sw.js')
  assert.equal(prefixed('/m', 'relative/path'), 'relative/path')
  assert.equal(prefixed('/m', '//cdn.example.com/x'), '//cdn.example.com/x')
  assert.equal(prefixed('/m', 'https://example.com/x'), 'https://example.com/x')
  assert.equal(prefixed('', '/api/version'), '/api/version')
})

test('unprefixed takes the prefix off again', () => {
  assert.equal(unprefixed('/m', '/m/api/version'), '/api/version')
  assert.equal(unprefixed('/m', '/m'), '/')
  assert.equal(unprefixed('/m', '/m2/x'), '/m2/x')
  assert.equal(unprefixed('/m', '/other'), '/other')
  assert.equal(unprefixed('', '/api/version'), '/api/version')
})

test('rebasePictures puts the prefix on picture addresses from this server', () => {
  const data = {
    coverUrl: '/api/book-covers/12-M.jpg',
    posterUrl: 'https://image.tmdb.org/t/p/w342/x.jpg',
    title: '/api/not-a-picture',
    items: [{ imageUrl: '/api/music/artists/3/cover' }, { coverUrl: '/api/books/4/file-cover', entries: [{ coverUrl: '/api/music/covers/release-group/abc' }] }],
    empty: null,
    noCover: '',
  }
  const out = rebasePictures('/m', data)
  assert.equal(out, data)
  assert.equal(out.coverUrl, '/m/api/book-covers/12-M.jpg')
  assert.equal(out.posterUrl, 'https://image.tmdb.org/t/p/w342/x.jpg')
  assert.equal(out.title, '/api/not-a-picture')
  assert.equal(out.items[0].imageUrl, '/m/api/music/artists/3/cover')
  assert.equal(out.items[1].coverUrl, '/m/api/books/4/file-cover')
  assert.equal(out.items[1].entries?.[0].coverUrl, '/m/api/music/covers/release-group/abc')
  assert.deepEqual(rebasePictures('', { coverUrl: '/api/x' }), { coverUrl: '/api/x' })
  assert.equal(rebasePictures('/m', null), null)
  assert.deepEqual(rebasePictures('/m', [{ posterUrl: '/api/p' }]), [{ posterUrl: '/m/api/p' }])
})

test('with no page around there is no prefix', () => {
  assert.equal(BASE_PATH, '')
  assert.equal(withBase('/api/x'), '/api/x')
})
