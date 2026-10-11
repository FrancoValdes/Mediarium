// A small, safe Markdown reader for the user guide.
//
// It turns the project's own documentation (docs/*.md) into plain data: blocks
// and inline pieces. Nothing here produces HTML. The page draws the pieces as
// React elements, so text from the docs can never become markup, and a link is
// only ever one of a few safe kinds (see safeHref). Raw HTML in a document is
// left out.
//
// Supported: headings, paragraphs, bold, italic, strikethrough, code spans and
// fenced code, links, images (the guide leaves them out), bullet and numbered
// lists (nested), block quotes, tables, rules and line breaks. That is all the
// docs use.

export type Inline =
  | { t: 'text'; v: string }
  | { t: 'code'; v: string }
  | { t: 'strong'; c: Inline[] }
  | { t: 'em'; c: Inline[] }
  | { t: 'del'; c: Inline[] }
  | { t: 'link'; href: string; c: Inline[] }
  | { t: 'img'; alt: string; src: string }
  | { t: 'br' }

export type Block =
  | { t: 'heading'; level: number; c: Inline[]; text: string }
  | { t: 'p'; c: Inline[] }
  | { t: 'code'; lang: string; v: string }
  | { t: 'list'; ordered: boolean; start: number; items: Block[][] }
  | { t: 'quote'; c: Block[] }
  | { t: 'table'; head: Inline[][]; align: ('left' | 'center' | 'right' | '')[]; rows: Inline[][][] }
  | { t: 'hr' }

// ---------------------------------------------------------------- text helpers

// The words of some inline pieces, with all formatting taken off.
export function inlineText(nodes: Inline[]): string {
  let out = ''
  for (const n of nodes) {
    if (n.t === 'text' || n.t === 'code') out += n.v
    else if (n.t === 'br') out += ' '
    else if (n.t === 'img') out += n.alt
    else out += inlineText(n.c)
  }
  return out
}

// The anchor GitHub gives a heading: lower case, punctuation dropped, spaces to
// hyphens. The docs link to their own headings this way.
export function slugify(text: string): string {
  return text
    .toLowerCase()
    .trim()
    .replace(/[^\p{L}\p{N}\s_-]/gu, '')
    .replace(/\s/g, '-')
}

// Makes a run of headings unique the way GitHub does: the second "Troubleshooting"
// becomes "troubleshooting-1".
export function uniqueSlugs(texts: string[], taken: Set<string> = new Set()): string[] {
  return texts.map((text) => {
    const base = slugify(text) || 'section'
    let id = base
    for (let n = 1; taken.has(id); n++) id = `${base}-${n}`
    taken.add(id)
    return id
  })
}

// Only these kinds of address are ever turned into a link. Anything else
// (javascript:, data:, file: ...) comes out as plain text.
export function safeHref(href: string): string | null {
  const h = href.trim()
  if (h === '') return null
  if (h.startsWith('#') || h.startsWith('/') || h.startsWith('./') || h.startsWith('../')) return h
  if (/^(https?:|mailto:)/i.test(h)) return h
  // A relative path such as "x.md#a" or "images/a.png" has no scheme before the first slash.
  if (!/^[a-z][a-z0-9+.-]*:/i.test(h)) return h
  return null
}

// ---------------------------------------------------------------- inline

const PUNCT = /[!-/:-@[-`{-~]/

// Finds the "]" that closes the "[" at i, skipping code spans, escapes and
// nested brackets.
function matchBracket(s: string, i: number): number {
  let depth = 0
  for (let j = i; j < s.length; j++) {
    const c = s[j]
    if (c === '\\') j++
    else if (c === '`') {
      let n = 0
      while (s[j + n] === '`') n++
      const close = s.indexOf('`'.repeat(n), j + n)
      if (close >= 0) j = close + n - 1
      else j += n - 1
    } else if (c === '[') depth++
    else if (c === ']' && --depth === 0) return j
  }
  return -1
}

// "[text](destination "title")" starting at i. Null when it is not one.
function linkAt(s: string, i: number): { text: string; href: string; end: number } | null {
  const close = matchBracket(s, i)
  if (close < 0 || s[close + 1] !== '(') return null
  let j = close + 2
  while (s[j] === ' ') j++
  let href = ''
  if (s[j] === '<') {
    const end = s.indexOf('>', j)
    if (end < 0) return null
    href = s.slice(j + 1, end)
    j = end + 1
  } else {
    let depth = 0
    const start = j
    for (; j < s.length; j++) {
      const c = s[j]
      if (c === '\\') j++
      else if (c === '(') depth++
      else if (c === ')') {
        if (depth === 0) break
        depth--
      } else if (/\s/.test(c)) break
    }
    href = s.slice(start, j).replace(/\\(.)/g, '$1')
  }
  while (s[j] === ' ') j++
  if (s[j] === '"' || s[j] === "'") {
    const q = s[j]
    const end = s.indexOf(q, j + 1)
    if (end < 0) return null
    j = end + 1
    while (s[j] === ' ') j++
  }
  if (s[j] !== ')') return null
  return { text: s.slice(i + 1, close), href, end: j + 1 }
}

// The end of an emphasis run that opens at i with `k` marker characters, or -1.
function emphasisEnd(s: string, i: number, ch: string, k: number): number {
  const word = /[\p{L}\p{N}]/u
  for (let j = i + k; j < s.length; j++) {
    const c = s[j]
    if (c === '\\') {
      j++
      continue
    }
    if (c === '`') {
      let n = 0
      while (s[j + n] === '`') n++
      const close = s.indexOf('`'.repeat(n), j + n)
      if (close >= 0) j = close + n - 1
      else j += n - 1
      continue
    }
    if (c !== ch) continue
    let run = 0
    while (s[j + run] === ch) run++
    const before = s[j - 1] ?? ''
    const canClose = j > i + k && !/\s/.test(before)
    // Only a run of exactly the opening length closes, so "**a**" inside "*...*" is left alone.
    if (canClose && run === k && (ch !== '_' || !word.test(s[j + k] ?? ''))) return j
    j += run - 1
  }
  return -1
}

// Reads inline Markdown into pieces.
export function parseInline(s: string): Inline[] {
  const out: Inline[] = []
  let buf = ''
  const flush = () => {
    if (buf) {
      out.push({ t: 'text', v: buf })
      buf = ''
    }
  }
  const word = /[\p{L}\p{N}]/u
  let i = 0
  while (i < s.length) {
    const ch = s[i]
    if (ch === '\\' && i + 1 < s.length) {
      if (s[i + 1] === '\n') {
        flush()
        out.push({ t: 'br' })
        i += 2
        continue
      }
      if (PUNCT.test(s[i + 1])) {
        buf += s[i + 1]
        i += 2
        continue
      }
    }
    if (ch === '`') {
      let n = 0
      while (s[i + n] === '`') n++
      const close = s.indexOf('`'.repeat(n), i + n)
      if (close >= 0) {
        let v = s.slice(i + n, close).replace(/\n/g, ' ')
        if (v.length > 1 && v.startsWith(' ') && v.endsWith(' ') && v.trim() !== '') v = v.slice(1, -1)
        flush()
        out.push({ t: 'code', v })
        i = close + n
        continue
      }
      buf += '`'.repeat(n)
      i += n
      continue
    }
    if (ch === '\n') {
      if (/ {2,}$/.test(buf)) {
        buf = buf.replace(/ +$/, '')
        flush()
        out.push({ t: 'br' })
      } else buf = buf.replace(/ +$/, '') + ' '
      i++
      while (s[i] === ' ') i++
      continue
    }
    if (ch === '!' && s[i + 1] === '[') {
      const l = linkAt(s, i + 1)
      if (l) {
        flush()
        out.push({ t: 'img', alt: inlineText(parseInline(l.text)), src: l.href })
        i = l.end
        continue
      }
    }
    if (ch === '[') {
      const l = linkAt(s, i)
      if (l) {
        flush()
        out.push({ t: 'link', href: l.href, c: parseInline(l.text) })
        i = l.end
        continue
      }
    }
    if (ch === '<') {
      const rest = s.slice(i)
      const auto = /^<(https?:\/\/[^\s<>]+)>/.exec(rest)
      if (auto) {
        flush()
        out.push({ t: 'link', href: auto[1], c: [{ t: 'text', v: auto[1] }] })
        i += auto[0].length
        continue
      }
      const br = /^<br\s*\/?>/i.exec(rest)
      if (br) {
        flush()
        out.push({ t: 'br' })
        i += br[0].length
        continue
      }
      // Anything else, such as "<artist>", is text the reader should see.
    }
    if ((ch === 'h' && (s.startsWith('http://', i) || s.startsWith('https://', i))) && !word.test(s[i - 1] ?? '')) {
      const m = /^https?:\/\/[^\s<>]+/.exec(s.slice(i))
      if (m) {
        const url = m[0].replace(/[.,;:!?'")\]]+$/, '')
        if (url.length > 8) {
          flush()
          out.push({ t: 'link', href: url, c: [{ t: 'text', v: url }] })
          i += url.length
          continue
        }
      }
    }
    if (ch === '*' || ch === '_' || ch === '~') {
      let run = 0
      while (s[i + run] === ch) run++
      const next = s[i + run] ?? ''
      const prev = s[i - 1] ?? ''
      const opens = next !== '' && !/\s/.test(next) && (ch !== '_' || !word.test(prev))
      if (opens && ch === '~' && run === 2) {
        const end = emphasisEnd(s, i, ch, 2)
        if (end > 0) {
          flush()
          out.push({ t: 'del', c: parseInline(s.slice(i + 2, end)) })
          i = end + 2
          continue
        }
      } else if (opens && ch !== '~') {
        if (run >= 3) {
          const end = emphasisEnd(s, i, ch, 3)
          if (end > 0) {
            flush()
            out.push({ t: 'strong', c: [{ t: 'em', c: parseInline(s.slice(i + 3, end)) }] })
            i = end + 3
            continue
          }
        }
        const k = run >= 2 ? 2 : 1
        const end = emphasisEnd(s, i, ch, k)
        if (end > 0) {
          flush()
          const inner = parseInline(s.slice(i + k, end))
          out.push(k === 2 ? { t: 'strong', c: inner } : { t: 'em', c: inner })
          i = end + k
          continue
        }
      }
      buf += ch.repeat(run)
      i += run
      continue
    }
    buf += ch
    i++
  }
  flush()
  return out
}

// ---------------------------------------------------------------- blocks

const FENCE = /^( {0,3})(`{3,}|~{3,})\s*([^`\s]*)[^`]*$/
const HEADING = /^ {0,3}(#{1,6})[ \t]+(.*?)(?:[ \t]+#+)?[ \t]*$/
const HR = /^ {0,3}([-*_])(?: *\1){2,} *$/
const BULLET = /^( {0,3})([-*+])( +|$)/
const NUMBERED = /^( {0,3})(\d{1,9})([.)])( +|$)/
const QUOTE = /^ {0,3}> ?/
// HTML the docs use only for layout. It is skipped, never shown or run.
const HTML_BLOCK = /^\s*<(?:!--|\/?(?:p|img|div|script|style|iframe|object|embed|form|input|details|summary|br|table|thead|tbody|tr|td|th|picture|source|video|a|center|h[1-6]|ul|ol|li|span|svg|figure|figcaption|kbd|sub|sup|hr)(?:[\s/>]|$))/i
const DELIM = /^\s*\|?\s*:?-+:?\s*(?:\|\s*:?-+:?\s*)*\|?\s*$/

function indentOf(line: string): number {
  let n = 0
  while (n < line.length && line[n] === ' ') n++
  return n
}

function splitRow(line: string): string[] {
  let s = line.trim()
  if (s.startsWith('|')) s = s.slice(1)
  if (s.endsWith('|') && !s.endsWith('\\|')) s = s.slice(0, -1)
  const cells: string[] = []
  let cur = ''
  for (let i = 0; i < s.length; i++) {
    if (s[i] === '\\' && s[i + 1] === '|') {
      cur += '|'
      i++
    } else if (s[i] === '|') {
      cells.push(cur.trim())
      cur = ''
    } else cur += s[i]
  }
  cells.push(cur.trim())
  return cells
}

function startsBlock(line: string, nextLine?: string): boolean {
  if (line.trim() === '') return true
  if (FENCE.test(line) || HEADING.test(line) || HR.test(line) || QUOTE.test(line) || HTML_BLOCK.test(line)) return true
  if (BULLET.test(line)) return true
  const n = NUMBERED.exec(line)
  if (n && n[2] === '1') return true
  if (nextLine !== undefined && line.includes('|') && DELIM.test(nextLine) && nextLine.includes('|')) return true
  return false
}

function parseBlocks(lines: string[], top: boolean): Block[] {
  const blocks: Block[] = []
  let i = 0
  while (i < lines.length) {
    const line = lines[i]
    if (line.trim() === '') {
      i++
      continue
    }
    const fence = FENCE.exec(line)
    if (fence) {
      const marker = fence[2]
      const body: string[] = []
      i++
      while (i < lines.length) {
        const l = lines[i]
        const close = /^ {0,3}(`{3,}|~{3,})\s*$/.exec(l)
        if (close && close[1][0] === marker[0] && close[1].length >= marker.length) break
        body.push(l.slice(Math.min(indentOf(l), fence[1].length)))
        i++
      }
      i++
      blocks.push({ t: 'code', lang: fence[3], v: body.join('\n') })
      continue
    }
    const h = HEADING.exec(line)
    if (h && top) {
      const c = parseInline(h[2])
      blocks.push({ t: 'heading', level: h[1].length, c, text: inlineText(c) })
      i++
      continue
    }
    if (HR.test(line)) {
      blocks.push({ t: 'hr' })
      i++
      continue
    }
    if (HTML_BLOCK.test(line)) {
      if (/^\s*<!--/.test(line)) {
        while (i < lines.length && !lines[i].includes('-->')) i++
        i++
      } else {
        while (i < lines.length && lines[i].trim() !== '') i++
      }
      continue
    }
    if (QUOTE.test(line)) {
      const inner: string[] = []
      while (i < lines.length && lines[i].trim() !== '' && (QUOTE.test(lines[i]) || !startsBlock(lines[i]))) {
        inner.push(lines[i].replace(QUOTE, ''))
        i++
      }
      blocks.push({ t: 'quote', c: parseBlocks(inner, false) })
      continue
    }
    const bullet = BULLET.exec(line)
    const numbered = NUMBERED.exec(line)
    if (bullet || numbered) {
      const ordered = !!numbered
      const base = (bullet ?? numbered)![1].length
      const items: Block[][] = []
      const start = numbered ? Number(numbered[2]) : 1
      while (i < lines.length) {
        const first = lines[i]
        const m = ordered ? NUMBERED.exec(first) : BULLET.exec(first)
        if (!m || m[1].length > base + 3) break
        const markerWidth = m[0].length
        // The text after the marker decides how far the item's own lines are indented.
        const gap = ordered ? m[4].length : m[3].length
        const contentIndent = m[1].length + (ordered ? m[2].length + 1 : 1) + (gap >= 1 && gap <= 4 ? gap : 1)
        // Docs often indent what belongs to a numbered step by two spaces, not three.
        const childIndent = Math.min(contentIndent, m[1].length + 2)
        const item: string[] = [first.slice(markerWidth)]
        i++
        while (i < lines.length) {
          const l = lines[i]
          if (l.trim() === '') {
            // A blank line stays in the item only when more of the item follows.
            let j = i
            while (j < lines.length && lines[j].trim() === '') j++
            if (j < lines.length && indentOf(lines[j]) >= childIndent) {
              for (; i < j; i++) item.push('')
              continue
            }
            break
          }
          if (indentOf(l) >= childIndent) {
            item.push(l.slice(Math.min(indentOf(l), contentIndent)))
            i++
            continue
          }
          // The next item of this list (or of a list around it) ends this one.
          if (BULLET.test(l) || NUMBERED.test(l)) break
          // A line that is not indented but carries on the last paragraph of the item.
          const prev = item[item.length - 1]
          if (prev.trim() !== '' && !startsBlock(l, lines[i + 1]) && !FENCE.test(prev)) {
            item.push(l.trim())
            i++
            continue
          }
          break
        }
        items.push(parseBlocks(item, false))
        // Same kind of list goes on; a blank line between items is allowed.
        let j = i
        while (j < lines.length && lines[j].trim() === '') j++
        const nextLine = lines[j]
        if (nextLine !== undefined && (ordered ? NUMBERED.test(nextLine) : BULLET.test(nextLine)) && !HR.test(nextLine) && indentOf(nextLine) <= base + 3) {
          i = j
          continue
        }
        break
      }
      blocks.push({ t: 'list', ordered, start, items })
      continue
    }
    if (line.includes('|') && i + 1 < lines.length && DELIM.test(lines[i + 1]) && lines[i + 1].includes('|')) {
      const head = splitRow(line)
      const align = splitRow(lines[i + 1]).map((d) => {
        const l = d.startsWith(':')
        const r = d.endsWith(':')
        return l && r ? 'center' : r ? 'right' : l ? 'left' : ''
      }) as ('left' | 'center' | 'right' | '')[]
      i += 2
      const rows: Inline[][][] = []
      while (i < lines.length && lines[i].trim() !== '' && lines[i].includes('|')) {
        const cells = splitRow(lines[i])
        while (cells.length < head.length) cells.push('')
        rows.push(cells.slice(0, head.length).map(parseInline))
        i++
      }
      blocks.push({ t: 'table', head: head.map(parseInline), align, rows })
      continue
    }
    // A paragraph runs until a blank line or the start of another kind of block.
    const para = [line.trimStart()]
    i++
    while (i < lines.length && !startsBlock(lines[i], lines[i + 1])) {
      para.push(lines[i].trimStart())
      i++
    }
    blocks.push({ t: 'p', c: parseInline(para.join('\n').trimEnd()) })
  }
  return blocks
}

// Reads a whole document into blocks.
export function parseMarkdown(md: string): Block[] {
  return parseBlocks(md.replace(/\r\n?/g, '\n').replace(/\t/g, '    ').split('\n'), true)
}

// The headings of a document, in order, with the words and level of each. It reads the
// same lines parseMarkdown turns into heading blocks, without building the rest.
export function scanHeadings(md: string): { level: number; text: string }[] {
  const out: { level: number; text: string }[] = []
  let fence = ''
  for (const line of md.replace(/\r\n?/g, '\n').split('\n')) {
    const f = /^ {0,3}(`{3,}|~{3,})/.exec(line)
    if (f) {
      if (!fence) fence = f[1]
      else if (f[1][0] === fence[0] && f[1].length >= fence.length && /^ {0,3}[`~]+\s*$/.test(line)) fence = ''
      continue
    }
    if (fence) continue
    const h = HEADING.exec(line)
    if (h) out.push({ level: h[1].length, text: inlineText(parseInline(h[2])) })
  }
  return out
}

// ---------------------------------------------------------------- build-time helpers

export interface Part {
  level: number // 0 for text before the first heading
  title: string
  lines: string[] // the heading line and everything under it, up to the next heading
}

// Cuts a document at its headings (not those inside code blocks).
export function splitParts(md: string): Part[] {
  const parts: Part[] = [{ level: 0, title: '', lines: [] }]
  let fence = ''
  for (const line of md.replace(/\r\n?/g, '\n').split('\n')) {
    const f = /^ {0,3}(`{3,}|~{3,})/.exec(line)
    if (f) {
      if (!fence) fence = f[1]
      else if (f[1][0] === fence[0] && f[1].length >= fence.length && /^ {0,3}[`~]+\s*$/.test(line)) fence = ''
    }
    const h = fence ? null : HEADING.exec(line)
    if (h) parts.push({ level: h[1].length, title: inlineText(parseInline(h[2])), lines: [line] })
    else parts[parts.length - 1].lines.push(line)
  }
  return parts
}

// Keeps the sections the filters allow. `only`/`except` name level-2 headings
// (their sub-headings go with them); `drop` names headings of any level that are removed
// together with what is under them.
export function selectParts(parts: Part[], opts: { only?: string[]; except?: string[]; drop?: RegExp }): Part[] {
  const out: Part[] = []
  let keep2 = true
  let dropLevel = 0
  for (const p of parts) {
    if (dropLevel && p.level > 0 && p.level <= dropLevel) dropLevel = 0
    if (dropLevel) continue
    if (p.level === 2) {
      const name = p.title.toLowerCase()
      keep2 = (!opts.only || opts.only.some((n) => n.toLowerCase() === name)) && !(opts.except ?? []).some((n) => n.toLowerCase() === name)
    }
    if (p.level === 1) {
      // The document's title and the introduction under it.
      if (!opts.only) out.push(p)
      keep2 = !opts.only
      continue
    }
    if (p.level > 0 && opts.drop?.test(p.title)) {
      dropLevel = p.level
      continue
    }
    if (p.level === 0) {
      // Text before the first heading is kept only when the whole document is.
      if (!opts.only) out.push(p)
      continue
    }
    if (keep2) out.push(p)
  }
  return out
}

// Removes what a reader of the app does not need: notes for people who script
// against the API, pictures (the guide does not carry them) and the captions that went
// with them.
export function cleanBody(lines: string[]): string[] {
  const text = lines.join('\n')
  const paras = text.split(/\n{2,}/)
  const kept: string[] = []
  let fence = false
  let afterImage = false
  for (const para of paras) {
    const ticks = (para.match(/^ {0,3}(```|~~~)/gm) ?? []).length
    const inCode = fence || ticks > 0
    if (ticks % 2 === 1) fence = !fence
    if (inCode) {
      kept.push(para)
      afterImage = false
      continue
    }
    const p = para.trim()
    if (/^(For scripts|Scripts)\b[:,]/.test(p)) continue
    if (/^!\[[^\]]*\]\([^)]*\)$/.test(p) || /^<p\b[^>]*>\s*<img\b[^>]*>\s*<\/p>$/i.test(p)) {
      afterImage = true
      continue
    }
    if (afterImage && /^\*[^*].*\*$/.test(p) && !p.includes('\n')) {
      afterImage = false
      continue
    }
    afterImage = false
    kept.push(para)
  }
  return kept.join('\n\n').split('\n')
}

// Moves every heading down by `by` levels (at most to 6), skipping code blocks.
export function shiftHeadings(md: string, by: number): string {
  if (by === 0) return md
  return splitParts(md)
    .flatMap((p) => {
      if (p.level === 0) return p.lines
      const [first, ...rest] = p.lines
      const lvl = Math.min(6, p.level + by)
      return [first.replace(/^ {0,3}#{1,6}/, '#'.repeat(lvl)), ...rest]
    })
    .join('\n')
}
