import type { MouseEvent } from 'react'

// The "?" buttons and "Learn more" links open the guide in a window over the
// page, so what you were doing stays where it is. The window itself is
// components/GuidePopup.tsx, mounted once in the app.
export const OPEN_GUIDE = 'mediarium:open-guide'

export function openGuide(topic: string) {
  window.dispatchEvent(new CustomEvent<string>(OPEN_GUIDE, { detail: topic }))
}

// A link click that opens the popup. Ctrl, Cmd, Shift and middle clicks keep
// their normal job (a new tab with the full guide page).
export function guideClick(topic: string, then?: () => void) {
  return (e: MouseEvent) => {
    if (e.defaultPrevented || e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return
    e.preventDefault()
    then?.()
    openGuide(topic)
  }
}
