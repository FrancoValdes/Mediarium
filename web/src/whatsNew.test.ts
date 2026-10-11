// Run with: npm test (uses node's built-in test runner, no extra packages).
import assert from 'node:assert/strict'
import { test } from 'node:test'
import { compareVersions, notesToShow, type ReleaseNotes } from './whatsNew.ts'

const notes: ReleaseNotes[] = [
  { version: '3.1.0', items: ['c'] },
  { version: '3.0.0', items: ['b'] },
  { version: '2.1.1', items: ['a'] },
]

test('versions compare by number, a beta counting as its version', () => {
  assert.ok(compareVersions('3.0.0', '2.1.1') > 0)
  assert.ok(compareVersions('2.10.0', '2.9.9') > 0)
  assert.equal(compareVersions('3.0.0-beta.2', '3.0.0'), 0)
  assert.equal(compareVersions('dev', '3.0.0'), 0)
})

test('the notes since the last version seen are shown, newest first', () => {
  const cases: [string | null, string, string[]][] = [
    [null, '3.1.0', []], // a browser that never saw Mediarium before: nothing
    ['3.1.0', '3.1.0', []], // already seen
    ['2.1.1', '3.0.0', ['3.0.0']],
    ['2.1.0', '3.1.0', ['3.1.0', '3.0.0', '2.1.1']],
    ['2.1.1', '3.0.0-beta.1', ['3.0.0']], // betas show the version they lead to
    ['3.0.0-beta.1', '3.0.0-beta.2', []], // one beta to the next: already shown
    ['2.1.1', 'dev', []],
  ]
  for (const [seen, current, want] of cases) {
    assert.deepEqual(
      notesToShow(seen, current, notes).map((n) => n.version),
      want,
      `${seen} -> ${current}`,
    )
  }
})
