import type { IrcConnection, IrcTemplate } from './api'

// Small helpers for the IRC announce card. The server checks everything again;
// these only keep the form honest while it is being filled in.

const PLACEHOLDER = /\{\{\s*\.([A-Za-z_][A-Za-z0-9_]*)(\s*\|\s*raw)?\s*\}\}/g
const GROUP = /\(\?P?<([A-Za-z_][A-Za-z0-9_]*)>/g

/** The names a template's patterns capture. */
export function groupNames(patterns: string[]): string[] {
  const out = new Set<string>()
  for (const p of patterns) {
    for (const m of p.matchAll(GROUP)) out.add(m[1])
  }
  return [...out]
}

/** The tracker keys (passkey, rsskey...) the download address needs: names it uses that no pattern captures. */
export function neededKeys(t: IrcTemplate): string[] {
  const groups = new Set(groupNames(t.patterns))
  const out: string[] = []
  for (const m of t.downloadUrl.matchAll(PLACEHOLDER)) {
    if (!groups.has(m[1]) && !out.includes(m[1])) out.push(m[1])
  }
  return out
}

/** One channel per line (or separated by commas); an optional second word is the channel's join password. */
export function parseChannels(text: string): { name: string; key: string }[] {
  const out: { name: string; key: string }[] = []
  for (const line of text.split(/\r?\n|,/)) {
    const [name, key] = line.trim().split(/\s+/)
    if (name) out.push({ name, key: key ?? '' })
  }
  return out
}

export function channelsText(c: IrcConnection): string {
  return c.channels.map((ch) => ch.name).join('\n')
}

/** Nicks separated by commas, spaces or lines. */
export function parseList(text: string): string[] {
  return text
    .split(/[\s,]+/)
    .map((s) => s.trim())
    .filter(Boolean)
}

export type StateTone = 'ok' | 'warn' | 'off' | 'bad'

/** How a connection is doing, as a tone for its colour and the English words for it (the caller translates). */
export function stateOf(c: IrcConnection): { tone: StateTone; key: 'off' | 'problem' | 'listening' | 'joining' | 'notInChannel' | 'cannotConnect' | 'connecting' | 'notRunning' } {
  const s = c.status
  if (!c.enabled) return { tone: 'off', key: 'off' }
  if (s.problem) return { tone: 'bad', key: 'problem' }
  switch (s.state) {
    case 'joined':
      return { tone: 'ok', key: 'listening' }
    case 'connected':
      return s.channels.some((ch) => ch.error) ? { tone: 'warn', key: 'notInChannel' } : { tone: 'warn', key: 'joining' }
    case 'connecting':
      return s.lastError ? { tone: 'bad', key: 'cannotConnect' } : { tone: 'warn', key: 'connecting' }
    default:
      return { tone: 'off', key: 'notRunning' }
  }
}
