// Run with: npm test
import assert from 'node:assert/strict'
import { test } from 'node:test'
import { defaultProfileFor, type ProfileDefaults, type TitleKind } from './profileDefaults.ts'

const cases: { name: string; kind: TitleKind; d: ProfileDefaults; want: number }[] = [
  { name: 'movie with no TV default', kind: 'movie', d: { defaultId: 3 }, want: 3 },
  { name: 'show with no TV default uses the movie default', kind: 'series', d: { defaultId: 3 }, want: 3 },
  { name: 'show with TV default 0 uses the movie default', kind: 'tv', d: { defaultId: 3, defaultTvId: 0 }, want: 3 },
  { name: 'show uses its own default', kind: 'series', d: { defaultId: 3, defaultTvId: 7 }, want: 7 },
  { name: 'movie ignores the TV default', kind: 'movie', d: { defaultId: 3, defaultTvId: 7 }, want: 3 },
]

for (const c of cases) {
  test(`defaultProfileFor: ${c.name}`, () => assert.equal(defaultProfileFor(c.kind, c.d), c.want))
}
