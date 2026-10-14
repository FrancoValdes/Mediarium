import assert from 'node:assert/strict'
import { test } from 'node:test'
import { noteSessionEnded, onSessionEnded, sessionEnded } from './sessionEnd.ts'

const gone = JSON.stringify({ error: 'You need to sign in first.' })

test('a 401 from a normal call means the session ended', () => {
  assert.equal(sessionEnded('/movies', 401, gone), true)
  assert.equal(sessionEnded('/queue?x=1', 401, 'unauthorized\n'), true)
  assert.equal(sessionEnded('/auth/api-keys', 401, gone), true)
})

test('other answers do not', () => {
  assert.equal(sessionEnded('/movies', 403, gone), false)
  assert.equal(sessionEnded('/movies', 200, '[]'), false)
  // A wrong password is a 401 too, with its own message.
  assert.equal(sessionEnded('/auth/change-password', 401, JSON.stringify({ error: "Your current password isn't right." })), false)
  assert.equal(sessionEnded('/plex/watchlist', 401, 'not json'), false)
  // The sign-in page's own calls.
  assert.equal(sessionEnded('/auth/login', 401, gone), false)
  assert.equal(sessionEnded('/auth/login/code', 401, gone), false)
  assert.equal(sessionEnded('/auth/me', 401, 'unauthorized'), false)
  assert.equal(sessionEnded('/auth/passkey/login/finish?c=x', 401, gone), false)
  assert.equal(sessionEnded('/onboarding/status', 401, gone), false)
})

test('listeners hear about it until they stop', () => {
  let heard = 0
  const stop = onSessionEnded(() => heard++)
  noteSessionEnded()
  stop()
  noteSessionEnded()
  assert.equal(heard, 1)
})
