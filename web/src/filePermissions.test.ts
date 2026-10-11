// Run with: npm test (uses node's built-in test runner, no extra packages).
import assert from 'node:assert/strict'
import { test } from 'node:test'
import { checkMode, modeFromUmask, symbolic } from './filePermissions.ts'

test('empty text means not set', () => {
  assert.deepEqual(checkMode('', false), { problem: null, worldWritable: false, value: null })
  assert.deepEqual(checkMode('   ', true), { problem: null, worldWritable: false, value: null })
})

test('ordinary modes are fine', () => {
  assert.equal(checkMode('644', false).problem, null)
  assert.equal(checkMode('0644', false).value, 0o644)
  assert.equal(checkMode('0o755', true).value, 0o755)
  assert.equal(checkMode('664', false).worldWritable, false)
  assert.equal(checkMode('775', true).worldWritable, false)
})

test('anything but three octal digits is refused', () => {
  for (const bad of ['64', '6444', '688', 'abc', '1755', '-644', '6 4 4']) {
    assert.notEqual(checkMode(bad, false).problem, null, bad)
  }
})

test('the owner has to keep access', () => {
  assert.notEqual(checkMode('444', false).problem, null)
  assert.notEqual(checkMode('644', true).problem, null)
  assert.notEqual(checkMode('555', true).problem, null)
})

test('world-writable modes are flagged', () => {
  assert.equal(checkMode('666', false).worldWritable, true)
  assert.equal(checkMode('777', true).worldWritable, true)
  assert.equal(checkMode('646', false).worldWritable, true)
  assert.equal(checkMode('757', true).worldWritable, true)
})

test('modes are shown the way ls shows them', () => {
  assert.equal(symbolic(0o644), 'rw-r--r--')
  assert.equal(symbolic(0o755), 'rwxr-xr-x')
  assert.equal(symbolic(0o664), 'rw-rw-r--')
  assert.equal(symbolic(0o600), 'rw-------')
})

test('a umask becomes the mode it gives', () => {
  assert.equal(modeFromUmask('022', false), 0o644)
  assert.equal(modeFromUmask('022', true), 0o755)
  assert.equal(modeFromUmask('002', false), 0o664)
  assert.equal(modeFromUmask('002', true), 0o775)
  assert.equal(modeFromUmask('0077', true), 0o700)
  assert.equal(modeFromUmask('abc', true), null)
})
