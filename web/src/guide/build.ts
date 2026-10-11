// Turns the documentation pages into the bundle the user guide loads.
// Runs while the web app is built (web/tools/build-guide.mjs) and in tests;
// it does no file access itself.
import { cleanBody, selectParts, shiftHeadings, splitParts } from './markdown.ts'
import { DROP_HEADINGS, TOPICS, type TopicDef } from './topicDefs.ts'

export interface GuideSource {
  file: string
  // Set when the page has its own heading inside a topic of several pages.
  title?: string
  md: string
}

export interface GuideTopic {
  id: string
  title: string
  summary: string
  group: string
  sources: GuideSource[]
}

export interface GuideData {
  topics: GuideTopic[]
}

// Reads one source: the sections wanted, without the title line, developer
// notes and pictures.
function buildSource(file: string, md: string, def: TopicDef['sources'][number]): GuideSource {
  const parts = splitParts(md)
  const names = new Set(parts.filter((p) => p.level === 2).map((p) => p.title.toLowerCase()))
  for (const n of [...(def.only ?? []), ...(def.except ?? [])]) {
    if (!names.has(n.toLowerCase())) throw new Error(`docs/${file} has no section "${n}" (named in web/src/guide/topicDefs.ts)`)
  }
  const kept = selectParts(parts, { only: def.only, except: def.except, drop: DROP_HEADINGS })
  const lines: string[] = []
  for (const p of kept) {
    // The page's own title is shown by the topic, so its line is dropped; the introduction under it stays.
    lines.push(...(p.level === 1 ? p.lines.slice(1) : p.lines))
  }
  let body = cleanBody(lines).join('\n').replace(/\n{3,}/g, '\n\n').trim()
  if (def.title) body = shiftHeadings(body, 1)
  return { file, title: def.title, md: body }
}

// `read` returns the text of a page in docs/ (and throws when it is missing).
export function buildGuide(read: (file: string) => string, defs: TopicDef[] = TOPICS): GuideData {
  const cache = new Map<string, string>()
  const get = (file: string) => {
    if (!cache.has(file)) cache.set(file, read(file))
    return cache.get(file)!
  }
  const topics = defs.map((d) => {
    const sources = d.sources.map((s) => buildSource(s.file, get(s.file), s)).filter((s) => s.md !== '')
    if (sources.length === 0) throw new Error(`Guide topic "${d.id}" has no text`)
    return { id: d.id, title: d.title, summary: d.summary, group: d.group, sources }
  })
  return { topics }
}
