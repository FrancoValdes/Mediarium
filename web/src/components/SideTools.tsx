import { createPortal } from 'react-dom'
import { useNavigate } from 'react-router-dom'
import { useEffect, useRef, useState, type ReactNode } from 'react'
import type { WatchLink } from '../api'
import { openBookApp } from '../bookshelf/open'
import { REPO_URL } from '../docs'
import { t } from '../i18n'
import { openReport } from '../report'
import Icon from './Icon'
import { useMusicPlayer } from './MusicPlayer'
import { MEDIA_SERVER_BRAND, MediaServerMark } from './mediaServerBrand'
import { withBase } from '../base'
import './InfoTip.css'

type Tip = { text: string; x: number; y: number; below?: boolean }

// Everything that plays, in one small menu that opens next to the play button:
// the book reader and the audiobook player, the music player, and the media
// servers.
function PlayMenu({ homes, ebooksOn, audiobooksOn, musicOn, at, onClose, below }: { homes: WatchLink[]; ebooksOn: boolean; audiobooksOn: boolean; musicOn: boolean; at: { x: number; y: number }; onClose: () => void; below?: boolean }) {
  const box = useRef<HTMLDivElement>(null)
  const navigate = useNavigate()
  const player = useMusicPlayer()
  const open = (e: React.MouseEvent, action: () => void) => {
    e.preventDefault()
    onClose()
    action()
  }
  useEffect(() => {
    box.current?.querySelector<HTMLElement>('a')?.focus()
    const onDown = (e: MouseEvent) => {
      const target = e.target as Element
      // The play button toggles the menu itself.
      if (target.closest?.('.side-tool.play')) return
      if (box.current && !box.current.contains(target)) onClose()
    }
    const onKey = (e: KeyboardEvent) => e.key === 'Escape' && onClose()
    document.addEventListener('mousedown', onDown)
    document.addEventListener('keydown', onKey)
    return () => {
      document.removeEventListener('mousedown', onDown)
      document.removeEventListener('keydown', onKey)
    }
  }, [onClose])
  return createPortal(
    <div ref={box} className={`side-menu${below ? ' below' : ''}`} role="menu" aria-label={t('Players and media servers')} style={{ left: at.x, top: at.y }}>
      {(ebooksOn || audiobooksOn) && (
        <>
          <div className="side-menu-head">{t('Read and listen')}</div>
          {ebooksOn && (
            <a role="menuitem" href={withBase('/bookshelf?tab=ebook')} onClick={(e) => open(e, () => openBookApp('/bookshelf?tab=ebook'))}>
              <span className="app-mark book" aria-hidden="true">
                <Icon name="book" size={14} />
              </span>
              <span>
                <strong>{t('Ebook reader')}</strong>
                <small>{t('Opens in its own window')}</small>
              </span>
              <Icon name="external" size={13} />
            </a>
          )}
          {audiobooksOn && (
            <a role="menuitem" href={withBase('/bookshelf?tab=audiobook')} onClick={(e) => open(e, () => openBookApp('/bookshelf?tab=audiobook'))}>
              <span className="app-mark audiobook" aria-hidden="true">
                <Icon name="headphones" size={14} />
              </span>
              <span>
                <strong>{t('Audiobook player')}</strong>
                <small>{t('Opens in its own window')}</small>
              </span>
              <Icon name="external" size={13} />
            </a>
          )}
        </>
      )}
      {musicOn && (
        <>
          <div className="side-menu-head">{t('Music')}</div>
          <a role="menuitem" href={withBase('/library?kind=music')} onClick={(e) => open(e, () => navigate('/library?kind=music'))}>
            <span className="app-mark music" aria-hidden="true">
              <Icon name="music" size={14} />
            </span>
            <span>
              <strong>{t('Music player')}</strong>
              <small>{player?.current ? t('Playing {title}', { title: player.current.title }) : t('Pick an artist or album to play')}</small>
            </span>
          </a>
        </>
      )}
      {homes.length > 0 && <div className="side-menu-head">{t('Open your media server')}</div>}
      {homes.map((h) => (
        <a key={h.serverId} role="menuitem" href={h.url} target="_blank" rel="noreferrer" onClick={onClose}>
          <MediaServerMark kind={h.kind} size={20} />
          <span>
            <strong>{MEDIA_SERVER_BRAND[h.kind].label}</strong>
            {h.name !== MEDIA_SERVER_BRAND[h.kind].label && <small>{h.name}</small>}
          </span>
          <Icon name="external" size={13} />
        </a>
      ))}
    </div>,
    document.body,
  )
}

// One round button with a tooltip that floats above the page, so the
// sidebar's edge never cuts it off.
function Tool({ label, className = '', href, onClick, children, expanded, showTip, hideTip }: {
  label: string
  className?: string
  href?: string
  onClick?: (el: HTMLElement) => void
  children: ReactNode
  expanded?: boolean
  showTip: (text: string, el: HTMLElement) => void
  hideTip: () => void
}) {
  const events = {
    'aria-label': label,
    onMouseEnter: (e: React.MouseEvent<HTMLElement>) => showTip(label, e.currentTarget),
    onFocus: (e: React.FocusEvent<HTMLElement>) => showTip(label, e.currentTarget),
    onMouseLeave: hideTip,
    onBlur: hideTip,
  }
  if (href) {
    return (
      <a
        className={`side-tool ${className}`}
        href={href}
        target="_blank"
        rel="noreferrer"
        onClick={(e) => {
          hideTip()
          if (onClick) {
            e.preventDefault()
            onClick(e.currentTarget)
          }
        }}
        {...events}
      >
        {children}
      </a>
    )
  }
  return (
    <button
      type="button"
      className={`side-tool ${className}`}
      aria-haspopup={expanded === undefined ? undefined : 'menu'}
      aria-expanded={expanded}
      onClick={(e) => {
        hideTip()
        onClick?.(e.currentTarget)
      }}
      {...events}
    >
      {children}
    </button>
  )
}

// The row of small buttons at the bottom of the sidebar: the players menu
// (books, music, media servers), reporting a problem, GitHub and support.
export default function SideTools({ ebooksOn, audiobooksOn, musicOn, homes, showSupport, variant = 'side', children }: { ebooksOn: boolean; audiobooksOn: boolean; musicOn: boolean; homes: WatchLink[]; showSupport: boolean; variant?: 'side' | 'top'; children?: ReactNode }) {
  const navigate = useNavigate()
  const [tip, setTip] = useState<Tip | null>(null)
  const [menuAt, setMenuAt] = useState<{ x: number; y: number } | null>(null)
  const top = variant === 'top'
  const anyPlayer = ebooksOn || audiobooksOn || musicOn || homes.length > 0
  const showTip = (text: string, el: HTMLElement) => {
    const r = el.getBoundingClientRect()
    // Kept clear of the window's edges (the tip is at most 260px wide).
    const x = Math.min(Math.max(r.left + r.width / 2, 138), window.innerWidth - 138)
    setTip(top ? { text, x, y: r.bottom, below: true } : { text, x, y: r.top })
  }
  const hideTip = () => setTip(null)
  const props = { showTip, hideTip }

  return (
    <div className={top ? 'top-tools' : 'side-tools'} role="group" aria-label={t('Apps and links')}>
      {anyPlayer && (
        <Tool
          label={t('Players and media servers')}
          className="play"
          expanded={!!menuAt}
          onClick={(el) => {
            const r = el.getBoundingClientRect()
            setMenuAt(menuAt ? null : top ? { x: Math.min(r.left, window.innerWidth - 340), y: r.bottom + 8 } : { x: r.left, y: r.top - 8 })
          }}
          {...props}
        >
          <Icon name="play" size={16} />
        </Tool>
      )}
      {menuAt && <PlayMenu homes={homes} ebooksOn={ebooksOn} audiobooksOn={audiobooksOn} musicOn={musicOn} at={menuAt} onClose={() => setMenuAt(null)} below={top} />}
      <Tool label={t('User guide')} onClick={() => navigate('/help')} className="help-tool" {...props}>
        <span className="side-q" aria-hidden="true">
          ?
        </span>
      </Tool>
      <Tool label={t('Report a problem or share an idea')} onClick={() => openReport()} className="report" {...props}>
        <Icon name="bug" size={16} />
      </Tool>
      <Tool label={t('Star Mediarium on GitHub')} href={REPO_URL} className="github" {...props}>
        <Icon name="github" size={16} />
      </Tool>
      {showSupport && (
        <Tool label={t('Support Mediarium')} href="https://ko-fi.com/ryanborg" className="support" {...props}>
          <Icon name="heart" size={16} />
        </Tool>
      )}
      {children}
      {tip &&
        !menuAt &&
        createPortal(
          <div className={`side-tip${tip.below ? ' below' : ''}`} role="tooltip" style={{ left: tip.x, top: tip.y }}>
            {tip.text}
          </div>,
          document.body,
        )}
    </div>
  )
}
