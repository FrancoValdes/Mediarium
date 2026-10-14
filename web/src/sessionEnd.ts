// Noticing that the session ended while the app is open (it expired, the
// password was changed on another device, or an administrator signed the
// person out). Every call would then fail with "You need to sign in first.",
// so the app shows the sign-in page instead. It is shown in place, at the same
// address, so signing in again goes back to where the person was.

// Calls whose 401 means something else: a wrong password or code, or the
// checks the app makes before anyone is signed in.
const SIGN_IN_CALLS = ['/auth/login', '/auth/me', '/auth/methods', '/auth/passkey/', '/onboarding/']

/** True when a 401 answer to path says the person is no longer signed in. */
export function sessionEnded(path: string, status: number, body: string): boolean {
  if (status !== 401) return false
  if (SIGN_IN_CALLS.some((p) => path.startsWith(p))) return false
  // The two answers the server gives a call with no valid session: the plain
  // one from the session check and the JSON one from the route check.
  const text = body.trim()
  if (text === 'unauthorized') return true
  try {
    return (JSON.parse(text) as { error?: string } | null)?.error === 'You need to sign in first.'
  } catch {
    return false
  }
}

const listeners = new Set<() => void>()

/** Runs fn whenever a call finds the session has ended. Returns a function that stops it. */
export function onSessionEnded(fn: () => void): () => void {
  listeners.add(fn)
  return () => listeners.delete(fn)
}

export function noteSessionEnded() {
  listeners.forEach((fn) => fn())
}
