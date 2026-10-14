import { createContext, useCallback, useContext, useEffect, useRef, useState, type ReactNode } from 'react'
import { api, ApiError, startModulesEarly, type User } from './api'
import { onSessionEnded } from './sessionEnd'

interface AuthState {
  loading: boolean
  // True while the server cannot be reached (for example it is restarting
  // after an update); the check is retried by itself until it answers.
  offline: boolean
  // True when the server does answer but slowly, or says it is busy (as
  // opposed to not answering at all, which is what a restart looks like).
  slow: boolean
  firstRunNeeded: boolean
  user: User | null
  // True when someone is signed in but never finished the first-run wizard
  // (for example the page was closed halfway), so it resumes.
  needsWizard: boolean
  refresh: () => Promise<void>
  setUser: (u: User | null) => void
}

const AuthContext = createContext<AuthState | null>(null)

// Longer than the ten seconds the server itself allows a page, so its "busy"
// answer arrives before this gives up.
const TIMEOUT_MS = 12000
const RETRY_MS = 3000

class TimedOut extends Error {
  constructor() {
    super('timed out')
  }
}

// Resolves like `p`, or rejects if it takes longer than TIMEOUT_MS, so a
// request left hanging by a restarting server (or a tunnel in front of it)
// cannot keep the loading screen up forever.
function withTimeout<T>(p: Promise<T>): Promise<T> {
  return new Promise((resolve, reject) => {
    const t = setTimeout(() => reject(new TimedOut()), TIMEOUT_MS)
    p.then(
      (v) => {
        clearTimeout(t)
        resolve(v)
      },
      (e) => {
        clearTimeout(t)
        reject(e)
      },
    )
  })
}

// "Server is not there right now" as opposed to a real answer such as 401.
function unreachable(e: unknown): boolean {
  if (e instanceof ApiError) return e.status >= 500 || e.status === 0
  return true // network error or timeout
}

// "The server is there but slow" as opposed to not answering at all.
function tooSlow(e: unknown): boolean {
  return e instanceof TimedOut || (e instanceof ApiError && e.busy)
}

export function AuthProvider({ children }: { children: ReactNode }) {
  const [loading, setLoading] = useState(true)
  const [offline, setOffline] = useState(false)
  const [slow, setSlow] = useState(false)
  const [firstRunNeeded, setFirstRunNeeded] = useState(false)
  const [user, setUser] = useState<User | null>(null)
  const [needsWizard, setNeedsWizard] = useState(false)
  const retry = useRef<ReturnType<typeof setTimeout> | undefined>(undefined)

  const refresh = useCallback(async () => {
    clearTimeout(retry.current)
    setLoading(true)
    try {
      // The three answers are asked for together, not one after the other: each wait
      // is a round trip to the server (a long one behind a reverse proxy), and the
      // page cannot start before all of them. What is not needed (the account of a
      // server that is still to be set up) is simply ignored.
      const statusCall = withTimeout(api.onboardingStatus())
      const meCall = withTimeout(api.me())
      const settingsCall = withTimeout(api.getSettings())
      meCall.catch(() => undefined)
      settingsCall.catch(() => undefined)
      startModulesEarly()
      const status = await statusCall
      setFirstRunNeeded(status.firstRunNeeded)
      if (!status.firstRunNeeded) {
        try {
          const me = await meCall
          setUser(me)
          try {
            setNeedsWizard(!(await settingsCall).onboardingDone)
          } catch {
            setNeedsWizard(false)
          }
        } catch (e) {
          if (unreachable(e)) throw e
          setUser(null) // signed out
        }
      }
      setOffline(false)
      setSlow(false)
      setLoading(false)
    } catch (e) {
      if (!unreachable(e)) {
        setOffline(false)
        setSlow(false)
        setLoading(false)
        return
      }
      // Keep the loading screen, say so, and try again shortly.
      setOffline(true)
      setSlow(tooSlow(e))
      retry.current = setTimeout(() => void refresh(), RETRY_MS)
    }
  }, [])

  useEffect(() => {
    void refresh()
    return () => clearTimeout(retry.current)
  }, [refresh])

  // The session ended while the app was open: show the sign-in page. It shows
  // in place of the page, at the same address, so signing in goes back there.
  useEffect(() => onSessionEnded(() => setUser(null)), [])

  return (
    <AuthContext.Provider value={{ loading, offline, slow, firstRunNeeded, user, needsWizard, refresh, setUser }}>
      {children}
    </AuthContext.Provider>
  )
}

export function useAuth() {
  const ctx = useContext(AuthContext)
  if (!ctx) throw new Error('useAuth must be used within AuthProvider')
  return ctx
}
