// Run with: npm test (uses node's built-in test runner, no extra packages).
import assert from 'node:assert/strict'
import { test } from 'node:test'
import { cleanBody, inlineText, parseInline, parseMarkdown, safeHref, scanHeadings, selectParts, shiftHeadings, slugify, splitParts, uniqueSlugs, type Block, type Inline } from './markdown.ts'

// A compact shape of the inline pieces, so a test can compare them in one line.
function shape(nodes: Inline[]): string {
  return nodes
    .map((n) => {
      switch (n.t) {
        case 'text':
          return n.v
        case 'code':
          return `<code:${n.v}>`
        case 'strong':
          return `<b:${shape(n.c)}>`
        case 'em':
          return `<i:${shape(n.c)}>`
        case 'del':
          return `<s:${shape(n.c)}>`
        case 'link':
          return `<a ${n.href}:${shape(n.c)}>`
        case 'img':
          return `<img ${n.src}|${n.alt}>`
        case 'br':
          return '<br>'
      }
    })
    .join('')
}

test('inline: bold, italic, code and strikethrough', () => {
  const cases: [string, string][] = [
    ['plain words', 'plain words'],
    ['a **bold** word', 'a <b:bold> word'],
    ['an *italic* word', 'an <i:italic> word'],
    ['an _italic_ word', 'an <i:italic> word'],
    ['**Settings > Media types**', '<b:Settings > Media types>'],
    ['bold **with `code` inside**', 'bold <b:with <code:code> inside>'],
    ['*one **two** one*', '<i:one <b:two> one>'],
    ['***both***', '<b:<i:both>>'],
    ['~~gone~~', '<s:gone>'],
    ['use `PUID_FILE` here', 'use <code:PUID_FILE> here'],
    ['``a ` b``', '<code:a ` b>'],
    ['snake_case_name stays', 'snake_case_name stays'],
    ['2 * 3 * 4', '2 * 3 * 4'],
    ['a lone * star', 'a lone * star'],
    ['literal \\*stars\\*', 'literal *stars*'],
    ['Add <artist> dialog', 'Add <artist> dialog'],
  ]
  for (const [src, want] of cases) assert.equal(shape(parseInline(src)), want, src)
})

test('inline: links, images and addresses', () => {
  const cases: [string, string][] = [
    ['[the page](./x.md#a)', '<a ./x.md#a:the page>'],
    ['[a **b**](https://x.test/p?q=1)', '<a https://x.test/p?q=1:a <b:b>>'],
    ['[paren](https://x.test/a_(b))', '<a https://x.test/a_(b):paren>'],
    ['[title](x.md "A title")', '<a x.md:title>'],
    ['[`code` link](x.md)', '<a x.md:<code:code> link>'],
    ['see <https://example.org/a>.', 'see <a https://example.org/a:https://example.org/a>.'],
    ['visit https://example.org/docs, then', 'visit <a https://example.org/docs:https://example.org/docs>, then'],
    ['![alt text](images/a.png)', '<img images/a.png|alt text>'],
    ['not a [link] here', 'not a [link] here'],
    ['line one<br>line two', 'line one<br>line two'],
  ]
  for (const [src, want] of cases) assert.equal(shape(parseInline(src)), want, src)
})

test('inline: line breaks', () => {
  assert.equal(shape(parseInline('one\ntwo')), 'one two')
  assert.equal(shape(parseInline('one  \ntwo')), 'one<br>two')
  assert.equal(shape(parseInline('one\\\ntwo')), 'one<br>two')
})

test('headings: anchors the way GitHub writes them', () => {
  const cases: [string, string][] = [
    ['The Usenet and torrents page', 'the-usenet-and-torrents-page'],
    ['Torrent port (58264)', 'torrent-port-58264'],
    ['Normal, SDH and forced subtitles', 'normal-sdh-and-forced-subtitles'],
    ['1. What you need', '1-what-you-need'],
    ['Why one /data folder? (hardlinks)', 'why-one-data-folder-hardlinks'],
    ['PUID, PGID and TZ', 'puid-pgid-and-tz'],
    ['"Mediarium is slow to answer right now"', 'mediarium-is-slow-to-answer-right-now'],
    ['Secrets from files (Docker secrets)', 'secrets-from-files-docker-secrets'],
  ]
  for (const [text, want] of cases) assert.equal(slugify(text), want, text)
  assert.deepEqual(uniqueSlugs(['Troubleshooting', 'Other', 'Troubleshooting', 'Troubleshooting']), ['troubleshooting', 'other', 'troubleshooting-1', 'troubleshooting-2'])
  const taken = new Set(['intro'])
  assert.deepEqual(uniqueSlugs(['Intro'], taken), ['intro-1'])
})

test('links: only safe addresses survive', () => {
  assert.equal(safeHref('https://example.org/a'), 'https://example.org/a')
  assert.equal(safeHref('http://example.org'), 'http://example.org')
  assert.equal(safeHref('mailto:a@example.org'), 'mailto:a@example.org')
  assert.equal(safeHref('./music.md#adding-an-artist'), './music.md#adding-an-artist')
  assert.equal(safeHref('music.md'), 'music.md')
  assert.equal(safeHref('#top'), '#top')
  assert.equal(safeHref('../CHANGELOG.md'), '../CHANGELOG.md')
  assert.equal(safeHref('javascript:alert(1)'), null)
  assert.equal(safeHref('  JavaScript:alert(1)'), null)
  assert.equal(safeHref('data:text/html;base64,AAAA'), null)
  assert.equal(safeHref('vbscript:x'), null)
  assert.equal(safeHref('file:///etc/passwd'), null)
  assert.equal(safeHref(''), null)
})

test('raw HTML is never kept', () => {
  const blocks = parseMarkdown('<script>alert(1)</script>\n\nText after\n\n<p align="center"><img src="images/a.png" onerror="x()" /></p>\n\n<!-- a\ncomment -->\n\nLast')
  const text = blocks.map((b) => (b.t === 'p' ? inlineText(b.c) : `[${b.t}]`)).join('|')
  assert.equal(text, 'Text after|Last')
  // Inside a paragraph a tag is only words, never markup.
  const inline = parseInline('click <b onclick="x()">here</b> now')
  assert.ok(inline.every((n) => n.t === 'text'))
})

function types(blocks: Block[]): string {
  return blocks.map((b) => b.t).join(',')
}

test('blocks: headings, paragraphs and rules', () => {
  const blocks = parseMarkdown('# Title\n\nFirst line\nsecond line.\n\n## Part ##\n\n---\n\nEnd')
  assert.equal(types(blocks), 'heading,p,heading,hr,p')
  assert.equal(blocks[0].t === 'heading' && blocks[0].text, 'Title')
  assert.equal(blocks[2].t === 'heading' && blocks[2].text, 'Part')
  assert.equal(blocks[1].t === 'p' && shape(blocks[1].c), 'First line second line.')
})

test('blocks: fenced code keeps every character', () => {
  const blocks = parseMarkdown('Run:\n\n```yaml\nservices:\n  app:\n    image: x   # **not bold**\n\n# not a heading\n```\n\nDone')
  assert.equal(types(blocks), 'p,code,p')
  const code = blocks[1]
  assert.ok(code.t === 'code')
  assert.equal(code.lang, 'yaml')
  assert.equal(code.v, 'services:\n  app:\n    image: x   # **not bold**\n\n# not a heading')
})

test('blocks: lists, nested lists and numbered steps', () => {
  const blocks = parseMarkdown('- one\n- two\n  - inner a\n  - inner b\n- three, which\n  carries on\n\n1. first\n2. second\n   more of second\n\n   ```\n   code in step\n   ```\n3. third')
  assert.equal(types(blocks), 'list,list')
  const bullets = blocks[0]
  assert.ok(bullets.t === 'list' && !bullets.ordered)
  assert.equal(bullets.items.length, 3)
  assert.equal(types(bullets.items[1]), 'p,list')
  const inner = bullets.items[1][1]
  assert.ok(inner.t === 'list' && inner.items.length === 2)
  const third = bullets.items[2][0]
  assert.equal(third.t === 'p' && shape(third.c), 'three, which carries on')
  const steps = blocks[1]
  assert.ok(steps.t === 'list' && steps.ordered && steps.items.length === 3)
  assert.equal(types(steps.items[1]), 'p,code')
})

test('blocks: tables, with alignment and an escaped pipe', () => {
  const blocks = parseMarkdown('| Choice | Looks for |\n|:--|--:|\n| **Normal** | one subtitle |\n| a \\| b | `x|y` |\n\nAfter')
  assert.equal(types(blocks), 'table,p')
  const table = blocks[0]
  assert.ok(table.t === 'table')
  assert.deepEqual(table.align, ['left', 'right'])
  assert.equal(table.head.map(shape).join('/'), 'Choice/Looks for')
  assert.equal(table.rows.length, 2)
  assert.equal(shape(table.rows[0][0]), '<b:Normal>')
  assert.equal(shape(table.rows[1][0]), 'a | b')
})

test('blocks: quotes hold other blocks', () => {
  const blocks = parseMarkdown('> **Note**\n> second line\n>\n> - a\n> - b\n\nAfter')
  assert.equal(types(blocks), 'quote,p')
  const q = blocks[0]
  assert.ok(q.t === 'quote')
  assert.equal(types(q.c), 'p,list')
})

test('headings are found the same way when only scanning', () => {
  const md = '# Top\n\ntext\n\n```\n# not a heading\n```\n\n## Real **one**\n\n- a\n\n### Another\n'
  assert.deepEqual(scanHeadings(md), [
    { level: 1, text: 'Top' },
    { level: 2, text: 'Real one' },
    { level: 3, text: 'Another' },
  ])
  const found = parseMarkdown(md).flatMap((b) => (b.t === 'heading' ? [b.text] : []))
  assert.deepEqual(found, scanHeadings(md).map((h) => h.text))
})

test('build helpers: split, select, clean and shift', () => {
  const md = ['# Page', 'Intro text.', '', '## Keep', 'Kept text.', '', '### Sub', 'Sub text.', '', '## Skip me', 'Skipped.', '', '## For scripts', 'Dev text.', '', '## Last', 'Final.'].join('\n')
  const parts = splitParts(md)
  assert.deepEqual(parts.map((p) => `${p.level}:${p.title}`), ['0:', '1:Page', '2:Keep', '3:Sub', '2:Skip me', '2:For scripts', '2:Last'])
  const only = selectParts(parts, { only: ['Keep'] })
  assert.deepEqual(only.map((p) => p.title), ['Keep', 'Sub'])
  const except = selectParts(parts, { except: ['Skip me'], drop: /^for scripts$/i })
  assert.deepEqual(except.map((p) => p.title), ['', 'Page', 'Keep', 'Sub', 'Last'])
  assert.equal(shiftHeadings('# A\n\n```\n# code\n```\n\n## B\n', 1), '## A\n\n```\n# code\n```\n\n### B\n')
  assert.equal(shiftHeadings('###### Deep', 1), '###### Deep')
})

test('clean: developer notes, pictures and their captions go; code stays', () => {
  const lines = [
    'Some text.',
    '',
    'For scripts: `GET /api/x` answers 200.',
    '',
    '![A screenshot](images/a.png)',
    '',
    '*The screenshot caption.*',
    '',
    'A paragraph in between.',
    '',
    '*An italic sentence that stays.*',
    '',
    '<p align="center"><img src="images/b.png" alt="b" /></p>',
    '',
    '```',
    'For scripts: inside code',
    '',
    '![not a picture](x)',
    '```',
    '',
    'Scripts: `PUT /api/y`.',
    '',
    'End.',
  ]
  const out = cleanBody(lines).join('\n')
  assert.ok(!out.includes('GET /api/x'))
  assert.ok(!out.includes('A screenshot'))
  assert.ok(!out.includes('The screenshot caption'))
  assert.ok(!out.includes('PUT /api/y'))
  assert.ok(out.includes('An italic sentence that stays.'))
  assert.ok(out.includes('For scripts: inside code'))
  assert.ok(out.includes('![not a picture](x)'))
  assert.ok(out.includes('Some text.') && out.includes('End.'))
})
