import { useEffect, useRef, useState } from 'react'
import { createPortal } from 'react-dom'

// A small glowing pill that rides the right edge of the sidebar when its menu
// is longer than the screen: it shows that the menu scrolls and how far down you
// are, without a scrollbar taking up room. Drawn in the page body, over the
// sidebar's edge, so the menu itself keeps its full width.
export default function SidebarScrollMark({ target }: { target: React.RefObject<HTMLElement | null> }) {
  const [at, setAt] = useState<{ left: number; top: number } | null>(null)
  const [busy, setBusy] = useState(false)
  const timer = useRef(0)

  useEffect(() => {
    const nav = target.current
    if (!nav) return
    const MARK = 30
    const measure = () => {
      const max = nav.scrollHeight - nav.clientHeight
      const r = nav.getBoundingClientRect()
      if (max <= 2 || r.width === 0 || r.left < 0 || getComputedStyle(nav).position === 'fixed') {
        setAt(null)
        return
      }
      const room = r.height - 2 * 22 - MARK
      setAt({ left: r.right - 3, top: r.top + 22 + (nav.scrollTop / max) * room })
    }
    const onScroll = () => {
      measure()
      setBusy(true)
      window.clearTimeout(timer.current)
      timer.current = window.setTimeout(() => setBusy(false), 700)
    }
    measure()
    nav.addEventListener('scroll', onScroll, { passive: true })
    window.addEventListener('resize', measure)
    const size = new ResizeObserver(measure)
    size.observe(nav)
    const kids = new MutationObserver(measure)
    kids.observe(nav, { childList: true, subtree: true })
    return () => {
      nav.removeEventListener('scroll', onScroll)
      window.removeEventListener('resize', measure)
      size.disconnect()
      kids.disconnect()
      window.clearTimeout(timer.current)
    }
  }, [target])

  if (!at) return null
  return createPortal(<span className={`side-scroll-mark${busy ? ' busy' : ''}`} style={{ left: at.left, top: at.top }} aria-hidden="true" />, document.body)
}
