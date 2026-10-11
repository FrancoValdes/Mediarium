import assert from 'node:assert/strict'
import { test } from 'node:test'
import { MIN_SHOWN, OVERSCAN, spaces, visibleRows } from './windowRange.ts'

test('at the top the first rows are shown, up to a screen and the extra beyond it', () => {
  const { first, last } = visibleRows(0, 800, 400, 500)
  assert.equal(first, 0)
  assert.equal(last, Math.ceil((800 + OVERSCAN) / 400))
})

test('scrolled down, rows on both sides of the screen are kept', () => {
  const { first, last } = visibleRows(40000, 800, 400, 500)
  assert.equal(first, Math.floor((40000 - OVERSCAN) / 400))
  assert.equal(last, Math.ceil((40000 + 800 + OVERSCAN) / 400))
  assert.ok(first < 100 && last > 100, 'the row under the screen (row 100) is in the window')
})

test('the window never goes past the list', () => {
  const end = visibleRows(1_000_000, 800, 400, 50)
  assert.deepEqual(end, { first: 50, last: 50 })
  const near = visibleRows(19000, 800, 400, 50)
  assert.equal(near.last, 50)
})

test('a few rows are always shown, even when the height guess is far too big', () => {
  const { first, last } = visibleRows(0, 800, 5000, 100)
  assert.equal(first, 0)
  assert.equal(last, MIN_SHOWN)
})

test('the empty space adds up to the whole list', () => {
  const rows = 430
  const stride = 436
  const gap = 16
  const { first, last } = visibleRows(60000, 900, stride, rows)
  const { before, after } = spaces(first, last, rows, stride, gap)
  // the shown rows take `stride` each too, except that the very last row has no gap below it
  const total = before + (last - first) * stride + after
  assert.equal(total, rows * stride - gap)
})

test('there is no space below when the last row is on the page', () => {
  assert.deepEqual(spaces(40, 50, 50, 300, 20), { before: 12000, after: 0 })
})
