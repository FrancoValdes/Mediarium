// Run with: npm test (uses node's built-in test runner, no extra packages).
import assert from 'node:assert/strict'
import { test } from 'node:test'
import { GITHUB_URL_MAX, SUPPORT_NOTE, draftProblem, fitWithin, githubIssueURL } from './report.ts'

test('a draft needs a short title and a sentence or two', () => {
  assert.equal(draftProblem({ title: 'ok', message: 'Long enough message' }), 'title')
  assert.equal(draftProblem({ title: 'Import fails', message: 'short' }), 'message')
  assert.equal(draftProblem({ title: 'x'.repeat(151), message: 'Long enough message' }), 'title-long')
  assert.equal(draftProblem({ title: 'Import fails', message: 'Every import fails with an error.' }), '')
})

test('a problem opens the bug form with the version, and the report stays out of the link', () => {
  const { url } = githubIssueURL({ kind: 'problem', title: 'Repair fails', message: 'It fails & stops.', version: '3.0.0', report: 'Version: 3.0.0\nlog line /data/movies' })
  const q = new URL(url).searchParams
  assert.equal(q.get('template'), 'bug_report.yml')
  assert.equal(q.get('title'), 'Repair fails')
  assert.equal(q.get('version'), '3.0.0')
  assert.equal(q.get('what-happened'), 'It fails & stops.')
  assert.equal(q.get('support'), SUPPORT_NOTE)
  assert.ok(!url.includes('log+line') && !url.includes('movies'))
})

test('without a report there is no note either', () => {
  const { url } = githubIssueURL({ kind: 'problem', title: 'Logs', message: 'No report this time.', version: '3' })
  assert.equal(new URL(url).searchParams.get('support'), null)
})

test('an idea opens the feature form and never carries the report', () => {
  const { url } = githubIssueURL({ kind: 'idea', title: 'Dark posters', message: 'Something nice to add.', version: '3.0.0', report: 'secret-ish' })
  const q = new URL(url).searchParams
  assert.equal(q.get('template'), 'feature_request.yml')
  assert.equal(q.get('problem'), 'Something nice to add.')
  assert.equal(q.get('support'), null)
})

test('a long message is cut to fit in a link', () => {
  const long = githubIssueURL({ kind: 'problem', title: 'Long', message: 'ä'.repeat(8000), version: '3', report: 'line\n'.repeat(5000) })
  assert.ok(long.url.length <= GITHUB_URL_MAX)
  assert.match(new URL(long.url).searchParams.get('what-happened') ?? '', /cut short/)
  assert.equal(new URL(long.url).searchParams.get('support'), SUPPORT_NOTE)
})

test('screenshots shrink to fit, never grow', () => {
  assert.deepEqual(fitWithin(4000, 2000), { width: 1600, height: 800 })
  assert.deepEqual(fitWithin(800, 600), { width: 800, height: 600 })
  assert.deepEqual(fitWithin(0, 600), { width: 0, height: 0 })
})
