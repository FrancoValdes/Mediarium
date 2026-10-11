import { useEffect, useState, type RefObject } from 'react'

// True once the element is on screen or about to be (within `margin` of it),
// and it stays true after that. A list far down a long page uses it to wait
// with its request until the person scrolls close, so a page of many lists does
// not ask the server, and the services behind it, for all of them at once. A
// browser without IntersectionObserver, or an element that is not there, counts
// as near, so nothing is ever held back for good.
export function useNearViewport(ref: RefObject<Element | null>, margin = 1600, warmAfterMs = 0): boolean {
  const [near, setNear] = useState(() => typeof IntersectionObserver === 'undefined')
  // With warmAfterMs, the list also starts by itself after that long, so lists far down the page are already
  // there when you scroll to them, spread out so they do not all ask at once.
  useEffect(() => {
    if (near || warmAfterMs <= 0) return
    const timer = window.setTimeout(() => setNear(true), warmAfterMs)
    return () => window.clearTimeout(timer)
  }, [near, warmAfterMs])
  useEffect(() => {
    if (near) return
    const el = ref.current
    if (!el) {
      setNear(true)
      return
    }
    const watch = new IntersectionObserver(
      (entries) => {
        if (entries.some((e) => e.isIntersecting)) {
          setNear(true)
          watch.disconnect()
        }
      },
      { rootMargin: `${margin}px 0px` },
    )
    watch.observe(el)
    return () => watch.disconnect()
  }, [near, ref, margin])
  return near
}
