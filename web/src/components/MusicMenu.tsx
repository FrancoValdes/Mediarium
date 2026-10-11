import { useCallback, useEffect, useLayoutEffect, useRef, useState, type ReactNode } from 'react'
import { createPortal } from 'react-dom'
import { api, isViewer, type MusicPlaylist, type PlaylistSelection } from '../api'
import { useAuth } from '../AuthContext'
import { t, plural } from '../i18n'
import Icon from './Icon'
import { useMusicPlayer, type PlayerTrack } from './musicPlayerContext'
import { useToast } from './Toast'

// A small popover next to a button, drawn on top of the page so no card or
// table around the button can cut it off. It follows the button when the page
// scrolls, and closes on Escape, on a click outside it, when the button is
// scrolled out of sight, and when the window changes size.
export function Popover({ anchor, onClose, children, label }: { anchor: HTMLElement | null; onClose: () => void; children: ReactNode; label: string }) {
  const box = useRef<HTMLDivElement>(null)
  const focused = useRef(false)
  const [pos, setPos] = useState<{ left: number; top?: number; bottom?: number; maxHeight: number } | null>(null)

  const place = useCallback(() => {
    if (!anchor) return
    const r = anchor.getBoundingClientRect()
    if (r.bottom < 0 || r.top > window.innerHeight) {
      onClose()
      return
    }
    const w = Math.min(300, window.innerWidth - 16)
    const left = Math.min(Math.max(8, r.right - w), window.innerWidth - w - 8)
    const below = window.innerHeight - r.bottom
    // Opens downwards when there is room, otherwise upwards.
    if (below >= 260 || below >= r.top) setPos({ left, top: r.bottom + 6, maxHeight: Math.max(160, below - 14) })
    else setPos({ left, bottom: window.innerHeight - r.top + 6, maxHeight: Math.max(160, r.top - 14) })
  }, [anchor, onClose])

  useLayoutEffect(() => {
    place()
  }, [place])

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') {
        e.stopPropagation()
        onClose()
        anchor?.focus()
      }
    }
    const onDown = (e: MouseEvent) => {
      const el = e.target as Node
      if (!box.current?.contains(el) && !anchor?.contains(el)) onClose()
    }
    const onScroll = (e: Event) => {
      if (e.target instanceof Node && box.current?.contains(e.target)) return // scrolling the list itself
      place()
    }
    document.addEventListener('keydown', onKey, true)
    document.addEventListener('mousedown', onDown)
    window.addEventListener('resize', place)
    window.addEventListener('scroll', onScroll, true)
    return () => {
      document.removeEventListener('keydown', onKey, true)
      document.removeEventListener('mousedown', onDown)
      window.removeEventListener('resize', place)
      window.removeEventListener('scroll', onScroll, true)
    }
  }, [anchor, onClose, place])

  useEffect(() => {
    if (pos && !focused.current) {
      focused.current = true
      box.current?.querySelector<HTMLElement>('input, button')?.focus({ preventScroll: true })
    }
  }, [pos])

  if (!anchor) return null
  return createPortal(
    <div
      ref={box}
      className="mm-pop"
      role="dialog"
      aria-label={label}
      style={pos ? { left: pos.left, top: pos.top, bottom: pos.bottom, maxHeight: pos.maxHeight } : { visibility: 'hidden' }}
    >
      {children}
    </div>,
    document.body,
  )
}

// Lists your playlists to add something to, with a field to make a new one.
export function PlaylistPicker({ selection, onDone, onBack }: { selection: PlaylistSelection; onDone: () => void; onBack?: () => void }) {
  const toast = useToast()
  const [lists, setLists] = useState<MusicPlaylist[] | null>(null)
  const [name, setName] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')

  useEffect(() => {
    let live = true
    api
      .musicPlaylists()
      .then((l) => live && setLists(l.filter((p) => p.mine)))
      .catch((e) => live && setError(e instanceof Error ? e.message : String(e)))
    return () => {
      live = false
    }
  }, [])

  async function addTo(p: MusicPlaylist) {
    setBusy(true)
    try {
      const r = await api.addToMusicPlaylist(p.id, selection)
      toast.success(r.added === 1 ? t('Added to {name}.', { name: p.name }) : plural(r.added, 'Added {n} track to {name}.', 'Added {n} tracks to {name}.', { name: p.name }))
      onDone()
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
      setBusy(false)
    }
  }

  async function create() {
    const n = name.trim()
    if (!n) return
    setBusy(true)
    try {
      const made = await api.createMusicPlaylist({ name: n, ...selection })
      toast.success(plural(made.trackCount, 'Made {name} with {n} track.', 'Made {name} with {n} tracks.', { name: made.name }))
      onDone()
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
      setBusy(false)
    }
  }

  return (
    <div className="mm-picker">
      <div className="mm-head">
        {onBack && (
          <button type="button" className="mm-back" onClick={onBack} aria-label={t('Back')} title={t('Back')}>
            <Icon name="chevron-left" size={16} />
          </button>
        )}
        <strong>{t('Add to playlist')}</strong>
      </div>
      <form
        className="mm-new"
        onSubmit={(e) => {
          e.preventDefault()
          void create()
        }}
      >
        <input type="text" value={name} onChange={(e) => setName(e.target.value)} placeholder={t('New playlist')} aria-label={t('Name of a new playlist')} maxLength={100} disabled={busy} />
        <button type="submit" className="btn-sm primary" disabled={busy || name.trim() === ''}>
          {t('Create')}
        </button>
      </form>
      {error && <p className="error-text mm-msg">{error}</p>}
      {lists === null && !error && <div className="skeleton" style={{ height: 64, margin: 8 }} />}
      {lists !== null && lists.length === 0 && <p className="mm-msg">{t('You have no playlists yet. Name one above to start it.')}</p>}
      {lists !== null && lists.length > 0 && (
        <ul className="mm-lists">
          {lists.map((p) => (
            <li key={p.id}>
              <button type="button" onClick={() => void addTo(p)} disabled={busy}>
                <span className="mm-name">{p.name}</span>
                <small>{plural(p.trackCount, '{n} track', '{n} tracks')}</small>
              </button>
            </li>
          ))}
        </ul>
      )}
    </div>
  )
}

// What a menu acts on: one track, or a whole album or artist (whose tracks are
// only fetched when an action needs them).
export type MusicTarget =
  | { kind: 'track'; track: PlayerTrack }
  | { kind: 'album'; albumId: number; tracks: () => Promise<PlayerTrack[]> }
  | { kind: 'artist'; artistId: number; tracks: () => Promise<PlayerTrack[]> }

// The "more" button on a track, an album or an artist: play it next, add it to
// the queue, or put it on a playlist.
export default function MusicMenu({ target, label, className, direct }: { target: MusicTarget; label: string; className?: string; direct?: boolean }) {
  const player = useMusicPlayer()
  const toast = useToast()
  // A viewer can queue music but not put it on a playlist.
  const viewer = isViewer(useAuth().user)
  const [open, setOpen] = useState(false)
  const first = direct ? 'playlist' : 'menu'
  const [view, setView] = useState<'menu' | 'playlist'>(first)
  const [busy, setBusy] = useState(false)
  const [btn, setBtn] = useState<HTMLButtonElement | null>(null)
  const close = useCallback(() => {
    setOpen(false)
    setView(first)
  }, [first])

  const selection: PlaylistSelection =
    target.kind === 'track' ? { trackIds: [target.track.id] } : target.kind === 'album' ? { albumId: target.albumId } : { artistId: target.artistId }

  async function queueIt(where: 'next' | 'end') {
    if (!player) return
    setBusy(true)
    try {
      const tracks = target.kind === 'track' ? [target.track] : await target.tracks()
      if (tracks.length === 0) {
        toast.error(target.kind === 'artist' ? t('None of this artist is in your library yet.') : t('None of this album is in your library yet.'))
        return
      }
      if (where === 'next') player.playNextTracks(tracks)
      else player.addToQueue(tracks)
      toast.success(
        where === 'next'
          ? plural(tracks.length, '{n} track will play next.', '{n} tracks will play next.')
          : plural(tracks.length, 'Added {n} track to the queue.', 'Added {n} tracks to the queue.'),
      )
      close()
    } catch (e) {
      toast.error(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(false)
    }
  }

  if (viewer && (direct || !player)) return null
  return (
    <>
      <button
        ref={setBtn}
        type="button"
        className={className ?? 'icon-btn mm-btn'}
        onClick={() => (open ? close() : setOpen(true))}
        aria-haspopup="dialog"
        aria-expanded={open}
        aria-label={label}
        title={label}
      >
        <Icon name={direct ? 'playlist-add' : 'more'} size={17} />
      </button>
      {open && (
        <Popover anchor={btn} onClose={close} label={label}>
          {view === 'menu' ? (
            <ul className="mm-actions" role="menu">
              {player && (
                <>
                  <li role="none">
                    <button type="button" role="menuitem" disabled={busy} onClick={() => void queueIt('next')}>
                      <Icon name="skip-next" size={16} /> {t('Play next')}
                    </button>
                  </li>
                  <li role="none">
                    <button type="button" role="menuitem" disabled={busy} onClick={() => void queueIt('end')}>
                      <Icon name="list" size={16} /> {t('Add to queue')}
                    </button>
                  </li>
                </>
              )}
              {!viewer && (
                <li role="none">
                  <button type="button" role="menuitem" onClick={() => setView('playlist')}>
                    <Icon name="playlist-add" size={16} /> {target.kind === 'artist' ? t('Add top tracks to a playlist') : t('Add to playlist')}
                  </button>
                </li>
              )}
            </ul>
          ) : (
            <PlaylistPicker selection={selection} onDone={close} onBack={direct ? undefined : () => setView('menu')} />
          )}
        </Popover>
      )}
    </>
  )
}
