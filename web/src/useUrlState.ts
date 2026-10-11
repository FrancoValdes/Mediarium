import { useCallback } from 'react'
import { useSearchParams } from 'react-router-dom'

// Page state that lives in the address (the tab, the filters), so Back brings
// it back exactly as it was. A value equal to its default leaves the address
// clean. Changes replace the current history entry instead of adding one, so
// Back still goes to the page before.
//
// Several values change in one call (patch), because React Router does not
// queue updates made in the same moment.
export function useUrlState<K extends string>(defaults: Record<K, string>): [Record<K, string>, (changes: Partial<Record<K, string>>) => void] {
  const [params, setParams] = useSearchParams()
  const values = {} as Record<K, string>
  for (const k of Object.keys(defaults) as K[]) values[k] = params.get(k) ?? defaults[k]
  const patch = useCallback(
    (changes: Partial<Record<K, string>>) => {
      const next = new URLSearchParams(params)
      for (const k of Object.keys(changes) as K[]) {
        const v = changes[k] ?? ''
        if (v === defaults[k]) next.delete(k)
        else next.set(k, v)
      }
      setParams(next, { replace: true })
    },
    // defaults is a constant object at every call site
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [params, setParams],
  )
  return [values, patch]
}
