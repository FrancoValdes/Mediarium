// Waits for a sign-in on another site (a Plex PIN): asks the server every few
// seconds whether it is done. The next question goes out only after the last
// answer came back, so slow answers never overlap and "done" is handled once.
// It stops by itself when the PIN's life is over, so closing the sign-in
// window without signing in doesn't leave the button waiting for ever.
export interface PinPoll<T> {
  // Asks once: the result when signed in, undefined while still waiting.
  check: () => Promise<T | undefined>
  lifeSeconds: number
  onDone: (result: T) => void
  onExpired: () => void
  onError: (e: unknown) => void
  everyMs?: number
  // For tests: the clock and the timer.
  now?: () => number
  later?: (fn: () => void, ms: number) => unknown
  cancel?: (handle: unknown) => void
}

/** Starts polling and returns a function that stops it. */
export function pollPin<T>(p: PinPoll<T>): () => void {
  const now = p.now ?? Date.now
  const later = p.later ?? ((fn: () => void, ms: number) => setTimeout(fn, ms))
  const cancel = p.cancel ?? ((h: unknown) => clearTimeout(h as ReturnType<typeof setTimeout>))
  // A little over the PIN's life, so the server can say "expired" first when it can.
  const until = now() + Math.max(30, p.lifeSeconds || 0) * 1000 + 5000
  let stopped = false
  let handle: unknown
  const ask = async () => {
    if (stopped) return
    if (now() > until) {
      stopped = true
      p.onExpired()
      return
    }
    try {
      const result = await p.check()
      if (stopped) return
      if (result !== undefined) {
        stopped = true
        p.onDone(result)
        return
      }
    } catch (e) {
      if (stopped) return
      stopped = true
      p.onError(e)
      return
    }
    handle = later(() => void ask(), p.everyMs ?? 2000)
  }
  handle = later(() => void ask(), p.everyMs ?? 2000)
  return () => {
    stopped = true
    cancel(handle)
  }
}
