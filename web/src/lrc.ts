// Synced lyrics (LRC): a line of words with the time it is sung, e.g.
//   [01:23.45] some words
// The same line can carry several times ("[00:10.00][01:30.00] chorus"), a
// header can say [offset:+500] (milliseconds the words come earlier, so lines
// are shown that much sooner), and other header tags ([ar:], [ti:], [al:]...)
// are only information.

export interface LrcLine {
  time: number // seconds
  text: string
}

export interface ParsedLyrics {
  lines: LrcLine[]
  synced: boolean
}

const STAMP = /\[(\d{1,3}):(\d{1,2})(?:[.:](\d{1,3}))?\]/g
const HEADER = /^\[([A-Za-z#]+):(.*)\]$/
// Word-by-word times of the "enhanced" form: <00:12.34>
const WORD_STAMP = /<\d{1,3}:\d{1,2}(?:[.:]\d{1,3})?>/g

function seconds(min: string, sec: string, frac?: string): number {
  let f = 0
  if (frac) f = Number(frac) / 10 ** frac.length
  return Number(min) * 60 + Number(sec) + f
}

// parseLrc reads LRC text. Text without time stamps comes back as plain lines
// with synced = false.
export function parseLrc(text: string): ParsedLyrics {
  const raw = text.replace(/^\ufeff/, '').split(/\r\n|\r|\n/)
  const lines: LrcLine[] = []
  let offset = 0
  let stamped = 0
  const plain: LrcLine[] = [] // every line of words in order, for text without enough times
  for (const row of raw) {
    const line = row.trim()
    const head = HEADER.exec(line)
    if (head) {
      if (head[1].toLowerCase() === 'offset') {
        const n = Number(head[2].trim())
        if (Number.isFinite(n)) offset = n / 1000
      }
      continue
    }
    const times: number[] = []
    STAMP.lastIndex = 0
    let m: RegExpExecArray | null
    let end = 0
    while ((m = STAMP.exec(line)) !== null) {
      if (m.index !== end) break // stamps only count at the start of the line
      times.push(seconds(m[1], m[2], m[3]))
      end = STAMP.lastIndex
    }
    const words = line.slice(end).replace(WORD_STAMP, '').replace(/\s+/g, ' ').trim()
    if (times.length > 0) {
      stamped += 1
      for (const t of times) lines.push({ time: Math.max(0, t - offset), text: words })
      if (words !== '') plain.push({ time: 0, text: words })
    } else if (line !== '') {
      plain.push({ time: 0, text: line })
    }
  }
  if (stamped < 2) return { lines: plain, synced: false }
  // Stable sort: equal times keep the order they were written in.
  const sorted = lines.map((l, i) => ({ l, i })).sort((a, b) => a.l.time - b.l.time || a.i - b.i).map((x) => x.l)
  return { lines: sorted, synced: true }
}

// activeLine is the index of the line being sung at `time` (the last one that
// has started), or -1 before the first line.
export function activeLine(lines: LrcLine[], time: number): number {
  let lo = 0
  let hi = lines.length - 1
  let found = -1
  while (lo <= hi) {
    const mid = (lo + hi) >> 1
    if (lines[mid].time <= time) {
      found = mid
      lo = mid + 1
    } else {
      hi = mid - 1
    }
  }
  return found
}
