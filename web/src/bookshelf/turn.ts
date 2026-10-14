// The page-turn animation shared by the ebook reader and the comic reader: the
// old page slides or fades out, the new one comes in. With "none", or when the
// system asks for less motion, the page just turns.

export type Turn = 'none' | 'slide' | 'fade'
export type TurnSpeed = 'fast' | 'normal' | 'slow'

// An old page going out and a new one coming in: this many milliseconds for both together.
export const TURN_MS: Record<TurnSpeed, number> = { fast: 160, normal: 280, slow: 460 }

export const reducedMotion = () => typeof window !== 'undefined' && window.matchMedia?.('(prefers-reduced-motion: reduce)').matches

const wait = (ms: number) => new Promise<void>((resolve) => window.setTimeout(resolve, ms))

// animateTurn runs go() (which shows the next or previous page) between the
// two halves of the animation on el. It resolves when the page is in.
export async function animateTurn(el: HTMLElement | null, effect: Turn, speed: TurnSpeed, dir: 'next' | 'prev', go: () => unknown): Promise<void> {
  if (!el || effect === 'none' || reducedMotion()) {
    await go()
    return
  }
  const half = TURN_MS[speed] / 2
  const shift = (out: boolean) => (effect === 'slide' ? `translateX(${(dir === 'next') === out ? '-' : ''}5%)` : 'none')
  try {
    el.style.transition = `transform ${half}ms ease-in, opacity ${half}ms ease-in`
    el.style.transform = shift(true)
    el.style.opacity = '0'
    await wait(half)
    await go()
    el.style.transition = 'none'
    el.style.transform = shift(false)
    void el.offsetWidth // let the browser take the new start before it moves
    el.style.transition = `transform ${half}ms ease-out, opacity ${half}ms ease-out`
    el.style.transform = 'none'
    el.style.opacity = '1'
    await wait(half)
  } finally {
    el.style.transition = ''
    el.style.transform = ''
    el.style.opacity = ''
  }
}

const PREFS_KEY = 'mediarium.reader'

// The turn choice saved by the ebook reader (Aa), read by the comic reader too.
export function storedTurn(): { turn: Turn; speed: TurnSpeed } {
  try {
    const p = JSON.parse(localStorage.getItem(PREFS_KEY) ?? '{}') as { turn?: string; speed?: string }
    return { turn: p.turn === 'none' || p.turn === 'fade' ? p.turn : 'slide', speed: p.speed === 'fast' || p.speed === 'slow' ? p.speed : 'normal' }
  } catch {
    return { turn: 'slide', speed: 'normal' }
  }
}

export function saveTurn(next: { turn?: Turn; speed?: TurnSpeed }) {
  try {
    const p = JSON.parse(localStorage.getItem(PREFS_KEY) ?? '{}') as Record<string, unknown>
    localStorage.setItem(PREFS_KEY, JSON.stringify({ ...p, ...next }))
  } catch {
    // the choice just isn't remembered
  }
}
