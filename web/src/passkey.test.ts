// Run with: npm test (uses node's built-in test runner, no extra packages).
import assert from 'node:assert/strict'
import { test } from 'node:test'
import { b64uToBytes, bytesToB64u } from './passkey.ts'

test('base64url round-trips bytes of every length', () => {
  for (let n = 0; n < 40; n++) {
    const bytes = new Uint8Array(n).map((_, i) => (i * 37 + n) % 256)
    assert.deepEqual(Array.from(b64uToBytes(bytesToB64u(bytes))), Array.from(bytes))
  }
})

test('base64url uses the url-safe alphabet without padding', () => {
  const text = bytesToB64u(new Uint8Array([0xfb, 0xff, 0xfe, 0x3e, 0x3f]))
  assert.equal(text, '-__-Pj8')
  assert.ok(!/[+/=]/.test(text))
})

test('text a server made is read with or without padding', () => {
  assert.deepEqual(Array.from(b64uToBytes('-__-Pj8')), [0xfb, 0xff, 0xfe, 0x3e, 0x3f])
  assert.deepEqual(Array.from(b64uToBytes('YQ')), [97])
  assert.deepEqual(Array.from(b64uToBytes('YQ==')), [97])
  assert.deepEqual(Array.from(b64uToBytes('')), [])
})
