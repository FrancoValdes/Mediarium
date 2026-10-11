// When to ask, now and then, for a star, feedback or a share. Never in the
// first two weeks, at most once every three months, never again after
// "Don't ask again". Remembered per browser. Kept apart for testing.

export const NUDGE_FIRST_SEEN = 'mediarium-first-seen'
export const NUDGE_LAST_ASKED = 'mediarium-nudge-asked'
export const NUDGE_NEVER = 'mediarium-nudge-never'

const DAY = 86_400_000
export const FIRST_ASK_AFTER = 14 * DAY
export const ASK_AGAIN_AFTER = 90 * DAY

export interface NudgeState {
  firstSeen: number | null
  lastAsked: number | null
  never: boolean
}

export function shouldNudge(s: NudgeState, now: number): boolean {
  if (s.never || s.firstSeen === null) return false
  if (now - s.firstSeen < FIRST_ASK_AFTER) return false
  return s.lastAsked === null || now - s.lastAsked >= ASK_AGAIN_AFTER
}
