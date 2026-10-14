import { useCallback, useEffect, useRef, useState } from 'react'
import { api, type Settings } from './api'
import { useToast } from './components/Toast'
import { t } from './i18n.ts'
import { saveOrder } from './autosaveOrder.ts'

// Shared behaviour for settings that apply the moment you change them (a
// switch, a checkbox): show the change immediately, save it, then trust what
// the server says it stored, not what we assumed. The value is re-read from
// the server when the page opens and whenever the browser tab comes back
// into view, so what you see is always what is really saved. A failed save
// puts the control back and says why; a successful one confirms with a toast.
export function useAutosaveSetting<T>(
  read: (s: Settings) => T,
  write: (value: T) => Parameters<typeof api.putSettings>[0],
  describe: (saved: T) => string,
  initial: T,
) {
  const [value, setValue] = useState<T>(initial)
  const [loaded, setLoaded] = useState(false)
  const [saving, setSaving] = useState(false)
  const toast = useToast()
  const readRef = useRef(read)
  readRef.current = read
  const order = useRef(saveOrder()).current
  // What the server last said it stored: a failed save goes back to it.
  const stored = useRef<T>(initial)

  const refresh = useCallback(() => {
    const stamp = order.readStarted()
    api
      .getSettings()
      .then((s) => {
        // A save happened meanwhile: its answer is newer than this one.
        if (!order.readCounts(stamp)) return
        stored.current = readRef.current(s)
        setValue(stored.current)
        setLoaded(true)
      })
      .catch(() => undefined)
  }, [order])

  useEffect(() => {
    refresh()
    const onVisible = () => {
      if (document.visibilityState === 'visible') refresh()
    }
    document.addEventListener('visibilitychange', onVisible)
    window.addEventListener('focus', refresh)
    return () => {
      document.removeEventListener('visibilitychange', onVisible)
      window.removeEventListener('focus', refresh)
    }
  }, [refresh])

  const change = useCallback(
    async (next: T) => {
      const mine = order.saveStarted()
      setValue(next)
      setSaving(true)
      try {
        const confirmed = readRef.current(await api.putSettings(write(next)))
        stored.current = confirmed
        if (order.isNewest(mine)) setValue(confirmed)
        toast.success(describe(confirmed))
      } catch (e) {
        // Back to what is really saved, unless a newer change is on its way.
        if (order.isNewest(mine)) setValue(stored.current)
        toast.error(t('Not saved: {error}', { error: e instanceof Error ? e.message : String(e) }))
      } finally {
        order.saveEnded()
        if (order.isNewest(mine)) setSaving(false)
      }
    },
    [order, write, describe, toast],
  )

  return { value, change, loaded, saving, refresh }
}
