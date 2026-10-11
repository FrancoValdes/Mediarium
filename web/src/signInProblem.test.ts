// Run with: npm test (uses node's built-in test runner, no extra packages).
import assert from 'node:assert/strict'
import { test } from 'node:test'
import { signInProblem } from './signInProblem.ts'

test('every code the server can send has words', () => {
  for (const code of ['off', 'no_account', 'not_allowed', 'disabled', 'email_unverified', 'link_admin', 'ambiguous', 'linked_already', 'state', 'limit', 'token', 'id_token', 'nonce', 'provider', 'claims', 'failed']) {
    const text = signInProblem(code, 'Authentik')
    assert.ok(text.length > 10, code)
    assert.ok(!text.includes('{'), `${code}: ${text}`)
  }
})

test('the provider is named where it helps', () => {
  assert.ok(signInProblem('no_account', 'Authentik').includes('Authentik'))
  assert.ok(signInProblem('whatever-else', 'Authentik').includes('Authentik'))
})

test('a made-up code is never repeated back, so an address cannot put words on the page', () => {
  const text = signInProblem('<script>alert(1)</script> call this number now', 'Authentik')
  assert.ok(!text.includes('script'))
  assert.ok(!text.includes('number'))
})
