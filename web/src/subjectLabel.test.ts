// Run with: npm test (uses node's built-in test runner, no extra packages).
import assert from 'node:assert/strict'
import { test } from 'node:test'
import { subjectLabel } from './subjectLabel.ts'

test('subjects filed with a prefix and underscores are written as words', () => {
  assert.equal(subjectLabel('series:Harry_Potter'), 'Series: Harry Potter')
  assert.equal(subjectLabel('series:Luz_e_Escuridão'), 'Series: Luz e Escuridão')
  assert.equal(subjectLabel('place:New_York'), 'Place: New York')
  assert.equal(subjectLabel('Habit breaking'), 'Habit breaking')
  assert.equal(subjectLabel('Science_fiction'), 'Science fiction')
})
