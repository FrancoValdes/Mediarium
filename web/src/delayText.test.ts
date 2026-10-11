// Run with: npm test (uses node's built-in test runner, no extra packages).
import assert from 'node:assert/strict'
import { test } from 'node:test'
import { untilText } from './delayText.ts'

const now = new Date(2026, 9, 9, 12, 0, 0) // local time, so the test holds in any zone

test('a time later today is just the time', () => {
  assert.equal(untilText(new Date(2026, 9, 9, 14, 20).toISOString(), now, 'en-GB'), '14:20')
})

test('a time on another day names the day', () => {
  assert.equal(untilText(new Date(2026, 9, 10, 1, 5).toISOString(), now, 'en-GB'), '10 Oct, 01:05')
})

test('something that is not a time is blank', () => {
  assert.equal(untilText('soon', now, 'en-GB'), '')
})
