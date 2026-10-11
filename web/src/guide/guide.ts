// The logic of the user guide that needs no screen: heading anchors, where a
// documentation link should go, and search. Kept apart so it can be tested.
import type { GuideData, GuideTopic } from './build.ts'
import { scanHeadings, slugify, splitParts, uniqueSlugs } from './markdown.ts'

export const DOCS_ROOT = 'https://github.com/rdborg/Mediarium/blob/main/docs/'
const REPO_BLOB = 'https://github.com/rdborg/Mediarium/blob/main/'

export interface Heading {
  level: number
  text: string
  // The anchor on the page.
  id: string
  // The documentation page it came from.
  file: string
}

export interface TopicIndex {
  topic: GuideTopic
  // Every heading of the topic, in order. A page that has its own title in the topic starts with that.
  headings: Heading[]
  // Per source (same order as topic.sources): the anchor of each of its headings, in order, so the page can
  // give each heading block its id, and the anchor of its title when it has one.
  sourceIds: { titleId: string; ids: string[] }[]
  // Per page: the anchor a link names (the way GitHub writes it) mapped to the anchor used here.
  byFile: Map<string, { top: string; slugs: Map<string, string> }>
}

// Anchors for one topic. They are unique across all the topic's pages.
export function indexTopic(topic: GuideTopic): TopicIndex {
  const taken = new Set<string>()
  const headings: Heading[] = []
  const sourceIds: TopicIndex['sourceIds'] = []
  const byFile = new Map<string, { top: string; slugs: Map<string, string> }>()
  for (const src of topic.sources) {
    const found = scanHeadings(src.md)
    let titleId = ''
    if (src.title) {
      titleId = uniqueSlugs([src.title], taken)[0]
      headings.push({ level: 2, text: src.title, id: titleId, file: src.file })
    }
    const ids = uniqueSlugs(
      found.map((h) => h.text),
      taken,
    )
    const local = uniqueSlugs(found.map((h) => h.text))
    found.forEach((h, i) => headings.push({ level: h.level, text: h.text, id: ids[i], file: src.file }))
    // A file can feed a topic more than once only by mistake; the later wins.
    const entry = byFile.get(src.file) ?? { top: titleId, slugs: new Map<string, string>() }
    local.forEach((slug, i) => entry.slugs.set(slug, ids[i]))
    if (titleId) entry.slugs.set(slugify(src.title ?? ''), titleId)
    byFile.set(src.file, entry)
    sourceIds.push({ titleId, ids })
  }
  return { topic, headings, sourceIds, byFile }
}

export interface TocEntry {
  id: string
  text: string
  // 0 for a main section, 1 for a section of a page that has its own heading in the topic.
  depth: 0 | 1
}

// The "on this page" list of a topic: its main sections, and under a page that has
// its own heading the sections of that page.
export function tocFor(ti: TopicIndex): TocEntry[] {
  const titled = new Set(ti.topic.sources.filter((s) => s.title).map((s) => s.file))
  const out: TocEntry[] = []
  for (const h of ti.headings) {
    if (h.level === 2) out.push({ id: h.id, text: h.text, depth: 0 })
    else if (h.level === 3 && titled.has(h.file)) out.push({ id: h.id, text: h.text, depth: 1 })
  }
  return out
}

export interface GuideIndex {
  topics: TopicIndex[]
  byId: Map<string, TopicIndex>
}

export function indexGuide(data: GuideData): GuideIndex {
  const topics = data.topics.map(indexTopic)
  return { topics, byId: new Map(topics.map((t) => [t.topic.id, t])) }
}

// Where a link in the documentation goes: to a topic of the guide, or out to the
// documentation on the web.
export type DocTarget = { kind: 'guide'; path: string } | { kind: 'external'; url: string } | { kind: 'none' }

// Works out the path of a relative link from the page it is on ("../CHANGELOG.md" from docs/x.md is "CHANGELOG.md").
function repoPath(from: string): string | null {
  const stack: string[] = ['docs']
  for (const seg of from.split('/')) {
    if (seg === '' || seg === '.') continue
    if (seg === '..') {
      if (stack.length === 0) return null
      stack.pop()
    } else stack.push(seg)
  }
  return stack.join('/')
}

const ONLINE_DOCS = /^https:\/\/github\.com\/rdborg\/Mediarium\/(?:blob|tree)\/main\/docs\/?([^#?]*)(#.*)?$/i

// `fromFile` is the documentation page the link is written in.
export function resolveDocLink(href: string, fromFile: string, guide: GuideIndex): DocTarget {
  let rest = href.trim()
  if (rest === '') return { kind: 'none' }
  let file = ''
  let anchor = ''
  const online = ONLINE_DOCS.exec(rest)
  if (online) {
    file = online[1]
    anchor = (online[2] ?? '').slice(1)
    if (file === '' || !file.endsWith('.md')) return { kind: 'external', url: rest }
  } else if (/^[a-z][a-z0-9+.-]*:/i.test(rest) || rest.startsWith('//')) {
    return { kind: 'external', url: rest }
  } else {
    const hash = rest.indexOf('#')
    if (hash >= 0) {
      anchor = rest.slice(hash + 1)
      rest = rest.slice(0, hash)
    }
    if (rest === '') file = fromFile
    else {
      const p = repoPath(rest)
      if (p === null) return { kind: 'none' }
      if (p.startsWith('docs/')) file = p.slice('docs/'.length)
      else return { kind: 'external', url: REPO_BLOB + p + (anchor ? '#' + anchor : '') }
    }
  }
  if (file.endsWith('.md') && !file.includes('/')) {
    // Prefer a topic that holds the whole page, and the one that has the anchor when it is named.
    const candidates = guide.topics.filter((t) => t.byFile.has(file))
    if (anchor) {
      const hit = candidates.find((t) => t.byFile.get(file)!.slugs.has(anchor))
      if (hit) return { kind: 'guide', path: `/help/${hit.topic.id}#${hit.byFile.get(file)!.slugs.get(anchor)}` }
    }
    const whole = candidates.find((t) => t.topic.sources.some((s) => s.file === file && !s.title)) ?? candidates[0]
    if (whole && !anchor) {
      const top = whole.byFile.get(file)!.top
      return { kind: 'guide', path: `/help/${whole.topic.id}${top ? '#' + top : ''}` }
    }
    if (whole && anchor === '') return { kind: 'guide', path: `/help/${whole.topic.id}` }
  }
  return { kind: 'external', url: DOCS_ROOT + file + (anchor ? '#' + anchor : '') }
}

// Finds out whether "topic#anchor" names something that exists. Used by the tests that check every
// HelpLink in the code.
export function topicRefExists(ref: string, guide: GuideIndex): boolean {
  const [id, anchor] = ref.split('#')
  const t = guide.byId.get(id)
  if (!t) return false
  return anchor === undefined || t.headings.some((h) => h.id === anchor)
}

// ---------------------------------------------------------------- search

export interface Section {
  topicId: string
  topicTitle: string
  // The anchor of the section's heading ('' for the top of a topic).
  id: string
  heading: string
  // What the heading sits under, for the result list.
  parent: string
  text: string
  low: string
}

// The words of some Markdown lines, without the marks.
export function plainText(lines: string[]): string {
  return lines
    .join('\n')
    .replace(/^ {0,3}(```|~~~).*$/gm, ' ')
    .replace(/!\[([^\]]*)\]\([^)]*\)/g, '')
    .replace(/\[([^\]]*)\]\([^)]*\)/g, '$1')
    .replace(/[`*]/g, '')
    .replace(/^ {0,3}#{1,6}\s+/gm, '')
    .replace(/^\s*>\s?/gm, '')
    .replace(/^\s*(?:[-+]|\d+[.)])\s+/gm, '')
    .replace(/\|/g, ' ')
    .replace(/^[\s:-]+$/gm, ' ')
    .replace(/\s+/g, ' ')
    .trim()
}

// Splits every topic at its headings, one searchable entry per section.
export function buildSearchIndex(guide: GuideIndex): Section[] {
  const out: Section[] = []
  for (const ti of guide.topics) {
    const { topic } = ti
    let cursor = 0 // into ti.headings
    type Open = { id: string; heading: string; parent: string; lines: string[] }
    const flush = (s: Open) => {
      if (s.id === '' && s.lines.every((l) => l.trim() === '')) return
      const text = plainText(s.lines)
      out.push({ topicId: topic.id, topicTitle: topic.title, id: s.id, heading: s.heading, parent: s.parent, text, low: (s.heading + ' ' + text).toLowerCase() })
    }
    for (const src of topic.sources) {
      let sec: Open = { id: '', heading: topic.title, parent: '', lines: [] }
      // What a top-level section of this page sits under, and the level that counts as top-level.
      let top = topic.title
      let baseLevel = 2
      if (src.title) {
        const h = ti.headings[cursor++]
        sec = { id: h.id, heading: h.text, parent: topic.title, lines: [] }
        top = h.text
        baseLevel = 3
      }
      let lastTop = top
      for (const p of splitParts(src.md)) {
        if (p.level === 0) {
          sec.lines.push(...p.lines)
          continue
        }
        flush(sec)
        const h = ti.headings[cursor++]
        const parent = p.level <= baseLevel ? top : lastTop
        if (p.level <= baseLevel) lastTop = h.text
        sec = { id: h.id, heading: h.text, parent, lines: p.lines.slice(1) }
      }
      flush(sec)
    }
  }
  return out
}

const STOP = new Set(['a', 'an', 'the', 'to', 'do', 'i', 'how', 'can', 'my', 'is', 'it', 'in', 'of', 'for', 'and', 'or', 'on', 'what', 'does', 'me', 'with', 'are', 'at', 'be'])

export function searchTokens(query: string): string[] {
  const all = query
    .toLowerCase()
    .split(/[^\p{L}\p{N}]+/u)
    .filter(Boolean)
  const useful = all.filter((w) => !STOP.has(w))
  return useful.length > 0 ? useful : all
}

function startsWord(low: string, token: string): number {
  let from = 0
  let count = 0
  for (;;) {
    const at = low.indexOf(token, from)
    if (at < 0) return count
    const before = at === 0 ? '' : low[at - 1]
    if (before === '' || !/[\p{L}\p{N}]/u.test(before)) count++
    from = at + token.length
  }
}

export interface Hit {
  section: Section
  score: number
}

// The sections that answer a search, best first. A section has to mention every
// word (as the start of a word); when nothing does, those that mention most of them.
export function searchGuide(sections: Section[], query: string, limit = 30): Hit[] {
  const tokens = searchTokens(query)
  if (tokens.length === 0) return []
  const phrase = tokens.join(' ')
  const scored = sections.map((s) => {
    const head = s.heading.toLowerCase()
    const topic = s.topicTitle.toLowerCase()
    let matched = 0
    let score = 0
    for (const tok of tokens) {
      const inHead = startsWord(head, tok)
      const inTopic = startsWord(topic, tok)
      const inBody = startsWord(s.low, tok)
      if (inHead || inTopic || inBody) matched++
      score += inHead * 10 + inTopic * 3 + Math.min(inBody, 6)
    }
    if (tokens.length > 1 && head.includes(phrase)) score += 20
    else if (tokens.length > 1 && s.low.includes(phrase)) score += 8
    return { section: s, score, matched }
  })
  const need = tokens.length
  let pool = scored.filter((x) => x.matched === need)
  if (pool.length === 0 && need > 1) pool = scored.filter((x) => x.matched >= Math.ceil(need / 2) && x.matched > 0)
  return pool
    .sort((a, b) => b.matched - a.matched || b.score - a.score)
    .slice(0, limit)
    .map(({ section, score }) => ({ section, score }))
}

// A short piece of a section's text around the first word found, for the result list.
export function snippet(s: Section, tokens: string[], length = 150): string {
  const low = s.text.toLowerCase()
  let at = -1
  for (const tok of tokens) {
    const i = low.indexOf(tok)
    if (i >= 0 && (at < 0 || i < at)) at = i
  }
  if (at < 0) return s.text.slice(0, length) + (s.text.length > length ? '…' : '')
  const start = Math.max(0, at - 40)
  const piece = s.text.slice(start, start + length)
  return (start > 0 ? '…' : '') + piece + (start + length < s.text.length ? '…' : '')
}

// Cuts text into pieces so the words searched for can be marked.
export function highlight(text: string, tokens: string[]): { text: string; hit: boolean }[] {
  if (tokens.length === 0) return [{ text, hit: false }]
  const esc = tokens.map((t) => t.replace(/[.*+?^${}()|[\]\\]/g, '\\$&'))
  const re = new RegExp(`(?<![\\p{L}\\p{N}])(${esc.join('|')})`, 'giu')
  const out: { text: string; hit: boolean }[] = []
  let last = 0
  for (const m of text.matchAll(re)) {
    if (m.index > last) out.push({ text: text.slice(last, m.index), hit: false })
    out.push({ text: m[0], hit: true })
    last = m.index + m[0].length
  }
  if (last < text.length) out.push({ text: text.slice(last), hit: false })
  return out
}
