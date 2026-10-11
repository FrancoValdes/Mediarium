// Run with: npm test (uses node's built-in test runner, no extra packages).
import assert from 'node:assert/strict'
import { test } from 'node:test'
import { GITHUB_URL_MAX, draftProblem, fitWithin, githubIssueURL } from './report.ts'

test('a draft needs a short title and a sentence or two', () => {
  assert.equal(draftProblem({ title: 'ok', message: 'Long enough message' }), 'title')
  assert.equal(draftProblem({ title: 'Import fails', message: 'short' }), 'message')
  assert.equal(draftProblem({ title: 'x'.repeat(151), message: 'Long enough message' }), 'title-long')
  assert.equal(draftProblem({ title: 'Import fails', message: 'Every import fails with an error.' }), '')
})

test('a problem opens the bug form with the version and the report', () => {
  const { url, reportFits } = githubIssueURL({ kind: 'problem', title: 'Repair fails', message: 'It fails & stops.', version: '3.0.0', report: 'Version: 3.0.0\nlog line' })
  const q = new URL(url).searchParams
  assert.equal(reportFits, true)
  assert.equal(q.get('template'), 'bug_report.yml')
  assert.equal(q.get('title'), 'Repair fails')
  assert.equal(q.get('version'), '3.0.0')
  assert.equal(q.get('what-happened'), 'It fails & stops.')
  assert.equal(q.get('support'), 'Version: 3.0.0\nlog line')
})

test('an idea opens the feature form and never carries the report', () => {
  const { url } = githubIssueURL({ kind: 'idea', title: 'Dark posters', message: 'Something nice to add.', version: '3.0.0', report: 'secret-ish' })
  const q = new URL(url).searchParams
  assert.equal(q.get('template'), 'feature_request.yml')
  assert.equal(q.get('problem'), 'Something nice to add.')
  assert.equal(q.get('support'), null)
})

test('a report too long for a link is left out, and a long message is cut', () => {
  const big = githubIssueURL({ kind: 'problem', title: 'Logs', message: 'Lots of log lines below.', version: '3', report: 'line\n'.repeat(5000) })
  assert.equal(big.reportFits, false)
  assert.equal(new URL(big.url).searchParams.get('support'), null)
  assert.ok(big.url.length <= GITHUB_URL_MAX)

  const long = githubIssueURL({ kind: 'problem', title: 'Long', message: 'ä'.repeat(8000), version: '3' })
  assert.ok(long.url.length <= GITHUB_URL_MAX)
  assert.match(new URL(long.url).searchParams.get('what-happened') ?? '', /cut short/)
})

test('screenshots shrink to fit, never grow', () => {
  assert.deepEqual(fitWithin(4000, 2000), { width: 1600, height: 800 })
  assert.deepEqual(fitWithin(800, 600), { width: 800, height: 600 })
  assert.deepEqual(fitWithin(0, 600), { width: 0, height: 0 })
})
