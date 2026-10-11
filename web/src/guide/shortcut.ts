import { useEffect } from 'react'
import { useLocation, useNavigate } from 'react-router-dom'
import { isTypingTarget } from '../shortcuts.ts'

// "?" opens the user guide from anywhere, unless you are typing in a field, a
// window is open, or you are already reading the guide.
export function opensGuide(
  e: { key: string; ctrlKey: boolean; metaKey: boolean; altKey: boolean; defaultPrevented: boolean; target: EventTarget | null },
  windowOpen: boolean,
): boolean {
  if (e.key !== '?' || e.ctrlKey || e.metaKey || e.altKey || e.defaultPrevented) return false
  return !isTypingTarget(e.target) && !windowOpen
}

export function useGuideShortcut() {
  const navigate = useNavigate()
  const onGuide = useLocation().pathname.startsWith('/help')
  useEffect(() => {
    if (onGuide) return
    const onKey = (e: KeyboardEvent) => {
      if (!opensGuide(e, document.querySelector('[role="dialog"], [role="alertdialog"]') !== null)) return
      e.preventDefault()
      navigate('/help')
    }
    document.addEventListener('keydown', onKey)
    return () => document.removeEventListener('keydown', onKey)
  }, [navigate, onGuide])
}
