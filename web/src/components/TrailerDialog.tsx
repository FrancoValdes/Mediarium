import { createPortal } from 'react-dom'
import { useEffect, useRef } from 'react'
import { t } from '../i18n'
import { useFocusTrap } from '../useFocusTrap'
import Icon from './Icon'

// A trailer in a box over the page, so you stay where you are. It plays from
// YouTube's privacy-friendly address, and only after you pressed the link, so
// nothing is asked of YouTube before that.
export default function TrailerDialog({ name, videoKey, watchUrl, onClose }: { name: string; videoKey: string; watchUrl: string; onClose: () => void }) {
  const box = useRef<HTMLDivElement>(null)
  useFocusTrap(box)
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => e.key === 'Escape' && onClose()
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [onClose])
  const src = `https://www.youtube-nocookie.com/embed/${encodeURIComponent(videoKey)}?${new URLSearchParams({ autoplay: '1', rel: '0', modestbranding: '1', playsinline: '1' }).toString()}`
  return createPortal(
    <div className="modal-backdrop" onMouseDown={(e) => e.target === e.currentTarget && onClose()}>
      <div ref={box} tabIndex={-1} className="modal trailer-dialog" role="dialog" aria-modal="true" aria-label={name}>
        <div className="trailer-head">
          <h2>{name}</h2>
          <a className="link-pill" href={watchUrl} target="_blank" rel="noreferrer">
            <Icon name="external" size={13} /> {t('Open on YouTube')}
          </a>
          <button type="button" className="icon-btn" onClick={onClose} aria-label={t('Close')} title={t('Close')}>
            <Icon name="x" size={16} />
          </button>
        </div>
        <div className="trailer-frame">
          <iframe src={src} title={name} allow="autoplay; encrypted-media; picture-in-picture; fullscreen" allowFullScreen referrerPolicy="strict-origin-when-cross-origin" />
        </div>
      </div>
    </div>,
    document.body,
  )
}
