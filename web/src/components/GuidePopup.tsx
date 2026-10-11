import { lazy, Suspense, useCallback, useEffect, useRef, useState } from 'react'
import { createPortal } from 'react-dom'
import { Link, useLocation } from 'react-router-dom'
import { helpPath } from '../guide/path.ts'
import { OPEN_GUIDE } from '../guide/popup.ts'
import { t } from '../i18n'
import Icon from './Icon'
import PageLoading from './PageLoading'

const GuidePopupBody = lazy(() => import('./GuidePopupBody'))

// The user guide in a window over the page. Opened by openGuide() (the "?"
// buttons and "Learn more" links). Escape, the close button or a click outside
// closes it; following a link inside it closes it and goes there.
export default function GuidePopupHost() {
  const [topic, setTopic] = useState<string | null>(null)
  const location = useLocation()
  const box = useRef<HTMLDivElement>(null)
  const back = useRef<Element | null>(null)
  const close = useCallback(() => {
    setTopic(null)
    const el = back.current
    back.current = null
    if (el instanceof HTMLElement) el.focus({ preventScroll: true })
  }, [])

  useEffect(() => {
    const on = (e: Event) => {
      back.current = document.activeElement
      setTopic((e as CustomEvent<string>).detail)
    }
    window.addEventListener(OPEN_GUIDE, on)
    return () => window.removeEventListener(OPEN_GUIDE, on)
  }, [])
  // Going to another page by a link in the guide closes the window.
  useEffect(() => {
    setTopic(null)
  }, [location.pathname])
  useEffect(() => {
    if (topic === null) return
    box.current?.focus()
    const key = (e: KeyboardEvent) => {
      if (e.key === 'Escape') close()
    }
    document.addEventListener('keydown', key)
    return () => document.removeEventListener('keydown', key)
  }, [topic, close])

  if (topic === null) return null
  return createPortal(
    <div className="modal-backdrop guide-pop-back" onMouseDown={(e) => e.target === e.currentTarget && close()}>
      <div ref={box} tabIndex={-1} className="modal guide-pop" role="dialog" aria-modal="true" aria-label={t('User guide')}>
        <div className="guide-pop-bar">
          <strong>{t('User guide')}</strong>
          <Link to={helpPath(topic)} className="guide-pop-open">
            {t('Open in the full guide')}
          </Link>
          <button type="button" className="guide-pop-x" aria-label={t('Close')} onClick={close}>
            <Icon name="x" size={16} />
          </button>
        </div>
        <div className="guide-pop-body">
          <Suspense fallback={<PageLoading />}>
            <GuidePopupBody topic={topic} />
          </Suspense>
        </div>
      </div>
    </div>,
    document.body,
  )
}
