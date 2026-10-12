import assert from 'node:assert/strict'
import { test } from 'node:test'
import { compareTitles, titleSortKey } from './titleSort.ts'

test('a leading article does not count', () => {
  assert.equal(titleSortKey('The Matrix'), 'Matrix')
  assert.equal(titleSortKey('A Quiet Place'), 'Quiet Place')
  assert.equal(titleSortKey('An Education'), 'Education')
  assert.equal(titleSortKey('the matrix'), 'matrix')
})

test('a title that is only an article, or starts with a word that merely begins like one, is left alone', () => {
  assert.equal(titleSortKey('The'), 'The')
  assert.equal(titleSortKey('Theory of Everything'), 'Theory of Everything')
  assert.equal(titleSortKey('Anatomy of a Fall'), 'Anatomy of a Fall')
  assert.equal(titleSortKey('Another Round'), 'Another Round')
  assert.equal(titleSortKey('"The Office"'), 'Office"')
})

test('the library sorts The Matrix under M', () => {
  const titles = ['The Matrix', 'Alien', 'A Quiet Place', 'Mad Max', 'The Abyss']
  assert.deepEqual([...titles].sort(compareTitles), ['The Abyss', 'Alien', 'Mad Max', 'The Matrix', 'A Quiet Place'])
})
