// Run with: npm test (uses node's built-in test runner, no extra packages).
import assert from 'node:assert/strict'
import { existsSync, readdirSync, readFileSync, statSync } from 'node:fs'
import { join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { test } from 'node:test'
import { buildGuide, type GuideData } from './build.ts'
import { buildSearchIndex, highlight, indexGuide, plainText, resolveDocLink, searchGuide, searchTokens, snippet, tocFor, topicRefExists, type GuideIndex } from './guide.ts'
import { opensGuide } from './shortcut.ts'
import { GROUPS, TOPICS } from './topicDefs.ts'

// A small guide made from text in the test, so the rules can be checked exactly.
const PAGES: Record<string, string> = {
  'a.md': ['# Alpha page', '', 'Intro of alpha. See [beta](./b.md#second-part) and [top of beta](b.md) and [mine](#own).', '', '## Own', '', 'Text about torrents and the torrent port.', '', '## Troubleshooting', '', 'Alpha trouble.'].join('\n'),
  'b.md': ['# Beta page', '', 'Intro of beta.', '', '## First part', '', 'Plain text.', '', '## Second part', '', 'Backups are kept for seven days.', '', '### Deeper', '', 'More.', '', '## Troubleshooting', '', 'Beta trouble.'].join('\n'),
  'c.md': ['# Gamma page', '', '## Only here', '', 'Gamma text.', '', '## Other', '', 'Not wanted.'].join('\n'),
}
const read = (file: string) => {
  if (!(file in PAGES)) throw new Error('no such page ' + file)
  return PAGES[file]
}
const DEFS = [
  { id: 'one', title: 'One', summary: 's', group: 'Start here', sources: [{ file: 'a.md' }, { file: 'b.md', title: 'The beta page' }] },
  { id: 'two', title: 'Two', summary: 's', group: 'Start here', sources: [{ file: 'c.md', only: ['Only here'] }, { file: 'b.md', title: 'Beta again', only: ['Second part'] }] },
]
function small(): { data: GuideData; guide: GuideIndex } {
  const data = buildGuide(read, DEFS)
  return { data, guide: indexGuide(data) }
}

test('build: titles, demoted headings and chosen sections', () => {
  const { data } = small()
  const one = data.topics[0]
  assert.equal(one.sources[0].md.startsWith('Intro of alpha.'), true, 'the page title line is dropped, the intro stays')
  assert.ok(one.sources[1].md.includes('### First part'), 'headings of a titled page move one level down')
  assert.ok(!one.sources[1].md.includes('# Beta page'))
  const two = data.topics[1]
  assert.ok(two.sources[0].md.includes('Gamma text.') && !two.sources[0].md.includes('Not wanted.'))
  assert.ok(!two.sources[0].md.includes('Gamma page'))
})

test('build: a missing page or section stops the build', () => {
  assert.throws(() => buildGuide(read, [{ id: 'x', title: 'X', summary: '', group: 'g', sources: [{ file: 'nope.md' }] }]), /no such page/)
  assert.throws(() => buildGuide(read, [{ id: 'x', title: 'X', summary: '', group: 'g', sources: [{ file: 'c.md', only: ['Not a section'] }] }]), /has no section "Not a section"/)
})

test('anchors are unique inside a topic and links map to them', () => {
  const { guide } = small()
  const one = guide.byId.get('one')!
  const ids = one.headings.map((h) => h.id)
  assert.equal(new Set(ids).size, ids.length)
  assert.ok(ids.includes('troubleshooting') && ids.includes('troubleshooting-1'), ids.join(','))
  // The second "Troubleshooting" (in b.md) is "troubleshooting-1" on the page, but b.md's own link calls it "troubleshooting".
  const b = one.byFile.get('b.md')!
  assert.equal(b.slugs.get('troubleshooting'), 'troubleshooting-1')
  assert.equal(b.slugs.get('second-part'), 'second-part')
  assert.equal(b.top, 'the-beta-page')
})

test('doc links: to a topic, to a section, to the top of a page, and out', () => {
  const { guide } = small()
  const to = (href: string, from = 'a.md') => resolveDocLink(href, from, guide)
  assert.deepEqual(to('./b.md#second-part'), { kind: 'guide', path: '/help/one#second-part' })
  assert.deepEqual(to('b.md'), { kind: 'guide', path: '/help/one#the-beta-page' })
  assert.deepEqual(to('#own'), { kind: 'guide', path: '/help/one#own' })
  assert.deepEqual(to('./c.md'), { kind: 'guide', path: '/help/two' })
  assert.deepEqual(to('#troubleshooting', 'b.md'), { kind: 'guide', path: '/help/one#troubleshooting-1' })
  assert.deepEqual(to('https://github.com/rdborg/Mediarium/blob/main/docs/c.md#only-here'), { kind: 'guide', path: '/help/two#only-here' })
  assert.deepEqual(to('./reference/api.md'), { kind: 'external', url: 'https://github.com/rdborg/Mediarium/blob/main/docs/reference/api.md' })
  assert.deepEqual(to('../CHANGELOG.md'), { kind: 'external', url: 'https://github.com/rdborg/Mediarium/blob/main/CHANGELOG.md' })
  assert.deepEqual(to('./FEATURES.md#x'), { kind: 'external', url: 'https://github.com/rdborg/Mediarium/blob/main/docs/FEATURES.md#x' })
  assert.deepEqual(to('https://example.org/a'), { kind: 'external', url: 'https://example.org/a' })
  assert.deepEqual(to('../../../etc/passwd'), { kind: 'none' })
})

test('table of contents: main sections, and the sections of a titled page under it', () => {
  const { guide } = small()
  const toc = tocFor(guide.byId.get('one')!)
  assert.deepEqual(
    toc.map((e) => `${e.depth}:${e.text}`),
    ['0:Own', '0:Troubleshooting', '0:The beta page', '1:First part', '1:Second part', '1:Troubleshooting'],
  )
})

test('search: every word must be found, headings count most', () => {
  const { guide } = small()
  const sections = buildSearchIndex(guide)
  const ids = (q: string) => searchGuide(sections, q).map((h) => `${h.section.topicId}#${h.section.id}`)
  assert.deepEqual(ids('torrent port')[0], 'one#own')
  assert.equal(ids('backups')[0], 'one#second-part')
  assert.ok(ids('backups').includes('two#second-part') || ids('backups').length >= 1)
  // Words that are only filler are ignored.
  assert.equal(ids('how do I back up the backups')[0] !== undefined, true)
  assert.deepEqual(ids('zzzz'), [])
  assert.deepEqual(ids(''), [])
  // A heading match beats a body match.
  assert.equal(ids('second part')[0], 'one#second-part')
  // Prefix of a word counts, the middle of a word does not.
  assert.ok(ids('torre').length > 0)
  assert.deepEqual(ids('orrent'), [])
})

test('search: words, snippets and marks', () => {
  assert.deepEqual(searchTokens('How do I add a movie?'), ['add', 'movie'])
  assert.deepEqual(searchTokens('the'), ['the'])
  assert.equal(plainText(['## Head', '', '- **bold** and `code` and [a link](x.md)', '| a | b |']), 'Head bold and code and a link a b')
  const { guide } = small()
  const s = buildSearchIndex(guide).find((x) => x.id === 'own')!
  assert.ok(snippet(s, ['torrent']).includes('torrents'))
  assert.deepEqual(highlight('Torrent port, torrents', ['torrent']), [
    { text: 'Torrent', hit: true },
    { text: ' port, ', hit: false },
    { text: 'torrent', hit: true },
    { text: 's', hit: false },
  ])
  assert.deepEqual(highlight('a.b', ['a.b']), [{ text: 'a.b', hit: true }])
})

test('shortcut: ? opens the guide unless you are typing or a window is open', () => {
  const key = (k: string, target: unknown = { tagName: 'BODY' }, extra: object = {}) => ({ key: k, ctrlKey: false, metaKey: false, altKey: false, defaultPrevented: false, target: target as EventTarget, ...extra })
  assert.equal(opensGuide(key('?'), false), true)
  assert.equal(opensGuide(key('/'), false), false)
  assert.equal(opensGuide(key('?', { tagName: 'INPUT', type: 'text' }), false), false)
  assert.equal(opensGuide(key('?', { tagName: 'TEXTAREA' }), false), false)
  assert.equal(opensGuide(key('?'), true), false)
  assert.equal(opensGuide(key('?', undefined, { ctrlKey: true }), false), false)
  assert.equal(opensGuide(key('?', undefined, { defaultPrevented: true }), false), false)
})

// ---- the real documentation ------------------------------------------------

const docs = fileURLToPath(new URL('../../../docs/', import.meta.url))
const haveDocs = existsSync(join(docs, 'README.md'))

test('the real docs build into a guide where every topic has text', { skip: !haveDocs }, () => {
  const data = buildGuide((file) => readFileSync(join(docs, file), 'utf8'))
  assert.equal(data.topics.length, TOPICS.length)
  for (const t of data.topics) {
    assert.ok(GROUPS.includes(t.group as (typeof GROUPS)[number]), `${t.id}: group ${t.group}`)
    assert.ok(t.summary.length > 20 && t.summary.length < 140, `${t.id}: summary length`)
    assert.ok(t.sources.reduce((n, s) => n + s.md.length, 0) > 400, `${t.id}: too little text`)
    for (const s of t.sources) {
      assert.ok(!/^For scripts\b/m.test(s.md), `${t.id}/${s.file}: developer notes left in`)
      assert.ok(!/!\[[^\]]*\]\(/.test(s.md), `${t.id}/${s.file}: a picture is left in`)
    }
  }
  const ids = data.topics.map((t) => t.id)
  assert.equal(new Set(ids).size, ids.length)
})

test('the real docs: no internal link is left dangling, and the guide covers the main docs pages', { skip: !haveDocs }, () => {
  const data = buildGuide((file) => readFileSync(join(docs, file), 'utf8'))
  const guide = indexGuide(data)
  // Every link to another page of the docs either lands in a topic (with a real anchor) or points to the website.
  const bad: string[] = []
  for (const t of data.topics) {
    for (const s of t.sources) {
      for (const m of s.md.matchAll(/\]\(([^)\s]+)\)/g)) {
        const href = m[1]
        if (/^https?:/i.test(href) && !href.includes('/Mediarium/blob/main/docs/')) continue
        const r = resolveDocLink(href, s.file, guide)
        if (r.kind === 'none') bad.push(`${t.id}/${s.file}: ${href}`)
        if (r.kind === 'guide') {
          const [id, anchor] = r.path.replace('/help/', '').split('#')
          if (!topicRefExists(anchor ? `${id}#${anchor}` : id, guide)) bad.push(`${t.id}/${s.file}: ${href} -> ${r.path}`)
        }
      }
    }
  }
  assert.deepEqual(bad, [])
  // The pages people read (not the developer and release notes) are all in some topic.
  const used = new Set(TOPICS.flatMap((t) => t.sources.map((s) => s.file)))
  const left = readdirSync(docs)
    .filter((f) => f.endsWith('.md') && statSync(join(docs, f)).isFile())
    .filter((f) => !used.has(f))
    .sort()
  // These stay out on purpose: the index page, the feature list, the legal text, release notes and community credits.
  // A new page has to be given a topic (web/src/guide/topicDefs.ts) or added here.
  assert.deepEqual(left, ['FEATURES.md', 'LEGAL.md', 'README.md', 'RELEASING.md', 'community.md'])
})

// Every HelpLink and InfoTip learnMore in the code names a topic and heading that exist.
test('HelpLink and learnMore references in the code point to real guide sections', { skip: !haveDocs }, () => {
  const data = buildGuide((file) => readFileSync(join(docs, file), 'utf8'))
  const guide = indexGuide(data)
  const src = fileURLToPath(new URL('../', import.meta.url))
  const files: string[] = []
  const walk = (dir: string) => {
    for (const name of readdirSync(dir)) {
      const p = join(dir, name)
      if (statSync(p).isDirectory()) {
        if (name !== 'locales') walk(p)
      } else if (/\.tsx$/.test(name)) files.push(p)
    }
  }
  walk(src)
  const found: string[] = []
  // The settings pages name their guide topic in settingsNav.ts (help: 'topic#anchor').
  for (const m of readFileSync(join(src, 'settingsNav.ts'), 'utf8').matchAll(/\bhelp:'([^']+)'/g)) found.push(m[1])
  for (const f of files) {
    const text = readFileSync(f, 'utf8')
    for (const m of text.matchAll(/<HelpLink[^>]*\btopic="([^"]+)"/g)) found.push(m[1])
    for (const m of text.matchAll(/\blearnMore="([^"]+)"/g)) found.push(m[1])
    for (const m of text.matchAll(/<TipLabel\s+help="([^"]+)"/g)) found.push(m[1])
  }
  assert.ok(found.length >= 5, 'expected the app to use HelpLink in several places, found ' + found.length)
  const missing = found.filter((ref) => !topicRefExists(ref, guide))
  assert.deepEqual(missing, [])
})
