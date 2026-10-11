// Run with: npm test
import assert from 'node:assert/strict'
import { test } from 'node:test'
import { afterMarkedRead, appendPage, moreText, NO_FILTERS, pageRequests, pageSignature, problemQuery, type ProblemRow } from './problemsView.ts'

const row = (id: number, over: Partial<ProblemRow> = {}): ProblemRow => ({
  id,
  level: 'error',
  area: 'downloads',
  areaLabel: 'Downloads',
  code: 'download.unpack_failed',
  title: 'Could not unpack a download',
  message: 'Could not unpack a download',
  count: 1,
  firstAt: '2026-09-30T11:00:00Z',
  lastAt: '2026-09-30T11:00:00Z',
  read: false,
  known: true,
  ...over,
})
const rows = (from: number, to: number) => Array.from({ length: to - from + 1 }, (_, i) => row(from + i))

test('pageRequests splits the rows on screen into server-sized requests', () => {
  const cases: [number, { offset: number; limit: number }[]][] = [
    [0, []],
    [30, [{ offset: 0, limit: 30 }]],
    [100, [{ offset: 0, limit: 100 }]],
    [101, [{ offset: 0, limit: 100 }, { offset: 100, limit: 1 }]],
    [250, [{ offset: 0, limit: 100 }, { offset: 100, limit: 100 }, { offset: 200, limit: 50 }]],
  ]
  for (const [count, want] of cases) assert.deepEqual(pageRequests(count), want, `count ${count}`)
  assert.deepEqual(pageRequests(5, 2), [{ offset: 0, limit: 2 }, { offset: 2, limit: 2 }, { offset: 4, limit: 1 }])
})

test('appendPage adds a page and never shows a row twice', () => {
  const first = rows(1, 30)
  assert.equal(appendPage(first, rows(31, 60)).length, 60)
  // A problem that repeated meanwhile moved down by one: its row is in both pages.
  const merged = appendPage(first, rows(30, 59))
  assert.equal(merged.length, 59)
  assert.deepEqual(
    merged.map((r) => r.id),
    Array.from({ length: 59 }, (_, i) => i + 1),
  )
  assert.equal(appendPage(first, []).length, 30)
})

test('pageSignature changes when a row repeats, is read, or the total moves', () => {
  const base = rows(1, 3)
  const sig = pageSignature(base, 10)
  assert.equal(pageSignature(rows(1, 3), 10), sig)
  assert.notEqual(pageSignature(base, 11), sig)
  assert.notEqual(pageSignature([base[0], base[1], row(3, { count: 2 })], 10), sig)
  assert.notEqual(pageSignature([base[0], base[1], row(3, { read: true })], 10), sig)
  assert.notEqual(pageSignature([base[0], base[1], row(3, { lastAt: '2026-09-30T11:05:00Z' })], 10), sig)
  assert.notEqual(pageSignature([row(9), ...base], 10), sig)
})

test('afterMarkedRead keeps the rows where they are', () => {
  const list = rows(1, 5)
  const one = afterMarkedRead(list, 412, { ids: [2] }, false)
  assert.deepEqual(one.rows.map((r) => [r.id, r.read]), [[1, false], [2, true], [3, false], [4, false], [5, false]])
  assert.equal(one.total, 412)

  const all = afterMarkedRead(list, 412, { all: true }, false)
  assert.ok(all.rows.every((r) => r.read))
  assert.equal(all.total, 412)

  // With only unread showing, a row marked read leaves, and the total follows.
  const gone = afterMarkedRead(list, 412, { ids: [2, 4] }, true)
  assert.deepEqual(gone.rows.map((r) => r.id), [1, 3, 5])
  assert.equal(gone.total, 410)

  // Everything that matched is read, including the rows not loaded yet.
  const none = afterMarkedRead(list, 412, { all: true }, true)
  assert.deepEqual(none.rows, [])
  assert.equal(none.total, 0)

  // A row that was read already is not counted twice.
  const mixed = [row(1), row(2, { read: true })]
  assert.equal(afterMarkedRead(mixed, 5, { ids: [1, 2] }, true).total, 4)
})

test('moreText says what the next click adds', () => {
  assert.equal(moreText(30, 412), 'Show 30 more')
  assert.equal(moreText(390, 412), 'Show 22 more')
  assert.equal(moreText(412, 412), '')
})

test('the page and the file share one query, cut at the same moment', () => {
  const now = new Date('2026-09-30T12:00:00Z')
  const f = { ...NO_FILTERS, level: 'error' as const, range: 'day' as const, unreadOnly: true, text: ' disk ' }
  const q = problemQuery(f, { limit: 30, offset: 60 }, now)
  const params = new URLSearchParams(q)
  assert.equal(params.get('level'), 'error')
  assert.equal(params.get('from'), '2026-09-29T12:00:00.000Z')
  assert.equal(params.get('q'), 'disk')
  assert.equal(params.get('unread'), '1')
  assert.equal(params.get('limit'), '30')
  assert.equal(params.get('offset'), '60')
  // The file asks for the same rows without a page.
  assert.equal(new URLSearchParams(problemQuery(f, {}, now)).get('limit'), null)
})
