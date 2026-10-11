import { createContext, useCallback, useContext, useEffect, useMemo, useState, type ReactNode } from 'react'
import { api, isViewer } from './api'
import { useAuth } from './AuthContext'
import type { AddTarget } from './components/AddDialog'
import { useToast } from './components/Toast'
import { t } from './i18n'

// Discover's "Select" mode and the "Not interested" list, shared by every row
// of posters on the page.
export interface Picks {
  // True for a viewer: the Not interested list is changed by accounts that can change things.
  readOnly: boolean
  excluded: Set<string>
  notInterested: (t: AddTarget) => void
  showAgain: (kind: string, tmdbId: number) => void
  selecting: boolean
  setSelecting: (on: boolean) => void
  picked: Map<string, AddTarget>
  toggle: (t: AddTarget) => void
  clear: () => void
}

export const pickKey = (t: { kind: string; tmdbId: number }) => `${t.kind}:${t.tmdbId}`

const PicksContext = createContext<Picks | null>(null)

export function usePicks(): Picks | null {
  return useContext(PicksContext)
}

export function PicksProvider({ children }: { children: ReactNode }) {
  const toast = useToast()
  const readOnly = isViewer(useAuth().user)
  const [excluded, setExcluded] = useState<Set<string>>(new Set())
  const [selecting, setSelectingState] = useState(false)
  const [picked, setPicked] = useState<Map<string, AddTarget>>(new Map())

  useEffect(() => {
    api
      .listExclusions()
      .then((list) => setExcluded(new Set(list.map((e) => pickKey(e)))))
      .catch(() => undefined)
  }, [])

  const notInterested = useCallback(
    (item: AddTarget) => {
      const key = pickKey(item)
      setExcluded((s) => new Set(s).add(key))
      api
        .addExclusion({ kind: item.kind, tmdbId: item.tmdbId, title: item.title, year: item.year })
        .then(() => toast.success(t("{title} won't be shown again. Undo it under Not interested at the top of Discover.", { title: item.title })))
        .catch((e) => toast.error(e instanceof Error ? e.message : String(e)))
    },
    [toast],
  )

  const showAgain = useCallback((kind: string, tmdbId: number) => {
    setExcluded((s) => {
      const n = new Set(s)
      n.delete(pickKey({ kind, tmdbId }))
      return n
    })
    void api.removeExclusion(kind, tmdbId)
  }, [])

  const toggle = useCallback((t: AddTarget) => {
    setPicked((m) => {
      const n = new Map(m)
      const key = pickKey(t)
      if (n.has(key)) n.delete(key)
      else n.set(key, t)
      return n
    })
  }, [])

  const value = useMemo<Picks>(
    () => ({
      readOnly,
      excluded,
      notInterested,
      showAgain,
      selecting,
      setSelecting: (on: boolean) => {
        setSelectingState(on)
        if (!on) setPicked(new Map())
      },
      picked,
      toggle,
      clear: () => setPicked(new Map()),
    }),
    [readOnly, excluded, notInterested, showAgain, selecting, picked, toggle],
  )
  return <PicksContext.Provider value={value}>{children}</PicksContext.Provider>
}
