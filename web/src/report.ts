// "Report a problem or send an idea": the parts that don't need a browser,
// so they can be tested. The dialog itself is components/ReportDialog.tsx.

export type ReportKind = 'problem' | 'idea'

export interface ReportDraft {
  kind: ReportKind
  title: string
  message: string
  version: string
  // The support report, when the person chose to include it.
  report?: string
}

export const TITLE_MIN = 3
export const TITLE_MAX = 150
export const MESSAGE_MIN = 10
export const MESSAGE_MAX = 8000

// GitHub turns away links much longer than about 8,000 characters. Stay well
// under it so the report never gets lost on the way.
export const GITHUB_URL_MAX = 7000

const ISSUES = 'https://github.com/rdborg/Mediarium/issues/new'

// What's wrong with a draft, or '' when it's ready to send.
export function draftProblem(d: Pick<ReportDraft, 'title' | 'message'>): '' | 'title' | 'message' | 'title-long' | 'message-long' {
  const title = d.title.trim()
  const message = d.message.trim()
  if ([...title].length < TITLE_MIN) return 'title'
  if ([...title].length > TITLE_MAX) return 'title-long'
  if ([...message].length < MESSAGE_MIN) return 'message'
  if ([...message].length > MESSAGE_MAX) return 'message-long'
  return ''
}

// A link that opens a new GitHub issue with the form already filled in. The
// support report goes in only when it fits; reportFits says whether it did, so
// the dialog can offer to copy it instead.
export function githubIssueURL(d: ReportDraft): { url: string; reportFits: boolean } {
  const params = new URLSearchParams()
  const title = d.title.trim()
  const message = d.message.trim()
  if (d.kind === 'idea') {
    params.set('template', 'feature_request.yml')
    params.set('title', title)
    params.set('problem', message)
  } else {
    params.set('template', 'bug_report.yml')
    params.set('title', title)
    if (d.version) params.set('version', d.version)
    params.set('what-happened', message)
  }
  const base = `${ISSUES}?${params.toString()}`
  const report = d.kind === 'problem' ? (d.report ?? '').trim() : ''
  if (!report) return { url: base.length > GITHUB_URL_MAX ? shorten(d) : base, reportFits: true }
  const withReport = new URLSearchParams(params)
  withReport.set('support', report)
  const full = `${ISSUES}?${withReport.toString()}`
  if (full.length <= GITHUB_URL_MAX) return { url: full, reportFits: true }
  return { url: base.length > GITHUB_URL_MAX ? shorten(d) : base, reportFits: false }
}

// A very long message doesn't fit in a link either: cut it and say so.
function shorten(d: ReportDraft): string {
  const note = '\n\n(cut short: the rest is too long for a link)'
  let message = d.message.trim()
  for (;;) {
    message = message.slice(0, Math.floor(message.length * 0.8))
    const { url } = githubIssueURL({ ...d, message: message + note, report: undefined })
    if (url.length <= GITHUB_URL_MAX || message.length < 100) return url
  }
}

// The size a screenshot is scaled to before it leaves the browser: big enough
// to read, small enough to send quickly.
export function fitWithin(width: number, height: number, longest = 1600): { width: number; height: number } {
  if (width <= 0 || height <= 0) return { width: 0, height: 0 }
  const scale = Math.min(1, longest / Math.max(width, height))
  return { width: Math.max(1, Math.round(width * scale)), height: Math.max(1, Math.round(height * scale)) }
}

// Picture files people may add. SVG and the rest are refused: they can carry
// scripts, and nobody needs them for a screenshot.
export const PICTURE_TYPES = ['image/png', 'image/jpeg', 'image/webp']
export const PICTURE_MAX_BYTES = 15 * 1024 * 1024

// Opens the report window from anywhere: the header button, the profile menu
// and Settings > System. ReportHost in the app shell listens for it.
export const OPEN_REPORT_EVENT = 'mediarium:report'

export function openReport(kind: ReportKind = 'problem') {
  window.dispatchEvent(new CustomEvent<ReportKind>(OPEN_REPORT_EVENT, { detail: kind }))
}
