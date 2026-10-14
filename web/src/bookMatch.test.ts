import assert from 'node:assert/strict'
import { test } from 'node:test'
import { canFindByHand, matchBody, resultLine, suggestedQuery } from './bookMatch.ts'

test('only the books not found or not saved can be matched by hand', () => {
  const cases: [Parameters<typeof canFindByHand>[0], boolean][] = [
    ['unmatched', true],
    ['failed', true],
    ['imported', false],
    ['already', false],
  ]
  for (const [status, want] of cases) assert.equal(canFindByHand(status), want, status)
})

test('the search box starts with the title and author read from the folder', () => {
  const cases: [Parameters<typeof suggestedQuery>[0], string][] = [
    [{ title: 'The Hobbit', author: 'J.R.R. Tolkien' }, 'The Hobbit J.R.R. Tolkien'],
    [{ title: ' Dune ', author: '' }, 'Dune'],
    [{ title: '', author: 'Frank Herbert' }, 'Frank Herbert'],
  ]
  for (const [r, want] of cases) assert.equal(suggestedQuery(r), want)
})

test('a search result reads as one line', () => {
  assert.equal(resultLine({ title: 'The Hobbit', author: 'J.R.R. Tolkien', year: 1937 }), 'The Hobbit · J.R.R. Tolkien · 1937')
  assert.equal(resultLine({ title: 'The Hobbit', author: '' }), 'The Hobbit')
})

test('the match request carries the row and the picked book', () => {
  const body = matchBody({ path: '/books/x.epub', kind: 'ebook' }, { key: 'OL1W', title: 'The Hobbit', author: 'J.R.R. Tolkien', authorKey: 'OL26320A', year: 1937, coverId: 123 }, true)
  assert.deepEqual(body, { path: '/books/x.epub', kind: 'ebook', olKey: 'OL1W', title: 'The Hobbit', author: 'J.R.R. Tolkien', authorKey: 'OL26320A', year: 1937, coverId: 123, rename: true })
})
