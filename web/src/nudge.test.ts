// Run with: npm test (uses node's built-in test runner, no extra packages).
import assert from 'node:assert/strict'
import { test } from 'node:test'
import { ASK_AGAIN_AFTER, FIRST_ASK_AFTER, shouldNudge } from './nudge.ts'

const now = Date.UTC(2026, 9, 6)

test('never in the first two weeks', () => {
  assert.equal(shouldNudge({ firstSeen: now - FIRST_ASK_AFTER + 1000, lastAsked: null, never: false }, now), false)
  assert.equal(shouldNudge({ firstSeen: now - FIRST_ASK_AFTER, lastAsked: null, never: false }, now), true)
})

test('at most once every three months', () => {
  const firstSeen = now - 200 * 86_400_000
  assert.equal(shouldNudge({ firstSeen, lastAsked: now - ASK_AGAIN_AFTER + 1000, never: false }, now), false)
  assert.equal(shouldNudge({ firstSeen, lastAsked: now - ASK_AGAIN_AFTER, never: false }, now), true)
})

test("never after don't ask again, or before a first visit is known", () => {
  assert.equal(shouldNudge({ firstSeen: 0, lastAsked: null, never: true }, now), false)
  assert.equal(shouldNudge({ firstSeen: null, lastAsked: null, never: false }, now), false)
})
