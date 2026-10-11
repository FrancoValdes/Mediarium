import { useCallback, useEffect, useState } from 'react'
import { Link, useNavigate, useParams } from 'react-router-dom'
import { api, isViewer, type MusicPlaylist, type MusicPlaylistDetail, type MusicPlaylistTrack } from '../api'
import { useAuth } from '../AuthContext'
import { useConfirm } from '../components/ConfirmProvider'
import Icon from '../components/Icon'
import MusicMenu from '../components/MusicMenu'
import { albumCoverUrl, useMusicPlayer } from '../components/musicPlayerContext'
import type { PlayerTrack } from '../musicQueue'
import Switch from '../components/Switch'
import { useToast } from '../components/Toast'
import { useModules } from '../ModulesContext'
import { useDocumentTitle } from '../documentTitle'
import { clock } from '../musicAudio'
import { plural, t } from '../i18n'
import '../music.css'
import '../musicpanel.css'

function span(ms: number): string {
  const s = Math.round(ms / 1000)
  const h = Math.floor(s / 3600)
  const m = Math.floor((s % 3600) / 60)
  if (h > 0) return t('{h} h {m} min', { h, m })
  return m > 0 ? t('{m} min', { m }) : t('{n} sec', { n: s })
}

// Up to four covers in a square; a music note while the list is empty or has none.
function Mosaic({ albumIds, size = 'card' }: { albumIds: number[]; size?: 'card' | 'big' }) {
  const [broken, setBroken] = useState<ReadonlySet<number>>(new Set())
  const ids = albumIds.filter((id) => !broken.has(id)).slice(0, 4)
  const shown = ids.length >= 4 ? ids : ids.slice(0, 1)
  return (
    <span className={`mpl-mosaic ${size} n${shown.length}`} aria-hidden="true">
      {shown.length === 0 ? (
        <span className="mpl-none">
          <Icon name="music" size={size === 'big' ? 48 : 34} />
        </span>
      ) : (
        shown.map((id) => <img key={id} src={albumCoverUrl(id)} alt="" loading="lazy" onError={() => setBroken((b) => new Set(b).add(id))} />)
      )}
    </span>
  )
}

function asPlayerTrack(tr: MusicPlaylistTrack): PlayerTrack {
  return { id: tr.trackId, title: tr.title, artist: tr.artist, album: tr.album, albumId: tr.albumId, lengthMs: tr.lengthMs }
}

function usePlayList() {
  const player = useMusicPlayer()
  const toast = useToast()
  return useCallback(
    async (id: number, shuffle: boolean, from?: number) => {
      if (!player) return
      try {
        const full = await api.musicPlaylist(id)
        const have = full.tracks.filter((x) => x.hasFile)
        if (have.length === 0) {
          toast.error(t('None of the tracks on this playlist are in your library yet.'))
          return
        }
        player.setShuffle(shuffle)
        const list = have.map(asPlayerTrack)
        const start = shuffle ? Math.floor(Math.random() * list.length) : Math.max(0, have.findIndex((x) => x.entryId === from))
        player.playQueue(list, start)
      } catch (e) {
        toast.error(e instanceof Error ? e.message : String(e))
      }
    },
    [player, toast],
  )
}

// ---- All playlists -------------------------------------------------------------------

export function MusicPlaylists() {
  useDocumentTitle(t('Playlists'))
  const { on, loaded } = useModules()
  const navigate = useNavigate()
  const toast = useToast()
  const player = useMusicPlayer()
  const play = usePlayList()
  // A viewer can open and play the shared playlists but not make one.
  const viewer = isViewer(useAuth().user)
  const [lists, setLists] = useState<MusicPlaylist[] | null>(null)
  const [error, setError] = useState('')
  const [name, setName] = useState('')
  const [shared, setShared] = useState(false)
  const [busy, setBusy] = useState(false)

  const load = useCallback(() => {
    api
      .musicPlaylists()
      .then((l) => {
        setLists(l)
        setError('')
      })
      .catch((e) => setError(e instanceof Error ? e.message : String(e)))
  }, [])
  useEffect(load, [load])

  async function create() {
    const n = name.trim()
    if (!n) return
    setBusy(true)
    try {
      const made = await api.createMusicPlaylist({ name: n, shared })
      navigate(`/music/playlists/${made.id}`)
    } catch (e) {
      toast.error(e instanceof Error ? e.message : String(e))
      setBusy(false)
    }
  }

  if (loaded && !on('music')) {
    return (
      <div className="empty-state">
        <Icon name="music" size={44} />
        <p>{t('Music is switched off.')}</p>
      </div>
    )
  }

  const mine = (lists ?? []).filter((p) => p.mine)
  const others = (lists ?? []).filter((p) => !p.mine)

  const card = (p: MusicPlaylist) => (
    <li key={p.id} className="mpl-card">
      <Link to={`/music/playlists/${p.id}`} className="mpl-open" aria-label={t('Open {name}', { name: p.name })}>
        <Mosaic albumIds={p.albumIds} />
        <span className="mpl-info">
          <strong title={p.name}>{p.name}</strong>
          <small>
            {[plural(p.trackCount, '{n} track', '{n} tracks'), p.trackCount > 0 ? span(p.durationMs) : null, p.mine ? null : t('by {name}', { name: p.owner })].filter(Boolean).join(' · ')}
          </small>
          {p.shared && p.mine && (
            <span className="mpl-tag" title={t('Everyone on this server can see and play it')}>
              <Icon name="user" size={11} /> {t('Shared')}
            </span>
          )}
        </span>
      </Link>
      {player && p.trackCount > 0 && (
        <span className="mpl-quick">
          <button type="button" className="mp-btn mpl-go" onClick={() => void play(p.id, false)} aria-label={t('Play {name}', { name: p.name })} title={t('Play')}>
            <Icon name="play" size={17} />
          </button>
          <button type="button" className="mp-btn" onClick={() => void play(p.id, true)} aria-label={t('Shuffle {name}', { name: p.name })} title={t('Shuffle')}>
            <Icon name="shuffle" size={16} />
          </button>
        </span>
      )}
    </li>
  )

  return (
    <div className="mpl-page">
      <div className="mpl-top">
        <div>
          <Link to="/library?kind=music" className="mpl-crumb">
            <Icon name="chevron-left" size={14} /> {t('Music')}
          </Link>
          <h1>{t('Playlists')}</h1>
        </div>
        {!viewer && (
          <form
            className="mpl-create"
            onSubmit={(e) => {
              e.preventDefault()
              void create()
            }}
          >
            <input type="text" value={name} onChange={(e) => setName(e.target.value)} placeholder={t('Name of a new playlist')} aria-label={t('Name of a new playlist')} maxLength={100} disabled={busy} />
            <label className="mpl-share" title={t('Everyone on this server can see and play it')}>
              <input type="checkbox" checked={shared} onChange={(e) => setShared(e.target.checked)} disabled={busy} /> {t('Share with everyone')}
            </label>
            <button type="submit" className="primary btn-with-icon" disabled={busy || name.trim() === ''}>
              <Icon name="plus" size={16} /> {t('New playlist')}
            </button>
          </form>
        )}
      </div>

      {error && <p className="error-text">{error}</p>}
      {lists === null && !error && <div className="skeleton" style={{ height: 180, borderRadius: 18 }} />}
      {lists !== null && lists.length === 0 && (
        <div className="empty-state">
          <Icon name="list" size={44} />
          <p>{viewer ? t('Nobody has shared a playlist yet.') : t('No playlists yet. Name one above, or add tracks to a new one from the menu on any track, album or artist.')}</p>
        </div>
      )}
      {mine.length > 0 && (
        <>
          <h2 className="mpl-h">{t('Yours')}</h2>
          <ul className="mpl-grid">{mine.map(card)}</ul>
        </>
      )}
      {others.length > 0 && (
        <>
          <h2 className="mpl-h">{t('Shared with everyone')}</h2>
          <ul className="mpl-grid">{others.map(card)}</ul>
        </>
      )}
    </div>
  )
}

// ---- One playlist ----------------------------------------------------------------------

export function MusicPlaylistPage() {
  const { id: raw } = useParams()
  const id = Number(raw)
  const navigate = useNavigate()
  const toast = useToast()
  const confirm = useConfirm()
  const player = useMusicPlayer()
  const play = usePlayList()
  const [list, setList] = useState<MusicPlaylistDetail | null>(null)
  const [error, setError] = useState('')
  const [rename, setRename] = useState<string | null>(null)
  const [dragFrom, setDragFrom] = useState<number | null>(null)
  const [dragOver, setDragOver] = useState<number | null>(null)
  useDocumentTitle(list?.name ?? t('Playlists'))

  const load = useCallback(() => {
    api
      .musicPlaylist(id)
      .then((l) => {
        setList(l)
        setError('')
      })
      .catch((e) => setError(e instanceof Error ? e.message : String(e)))
  }, [id])
  useEffect(load, [load])

  if (error && !list) {
    return (
      <div className="empty-state">
        <Icon name="list" size={44} />
        <p>{error}</p>
        <Link to="/music/playlists">{t('Back to the playlists')}</Link>
      </div>
    )
  }
  if (!list) return <div className="skeleton" style={{ height: 260, borderRadius: 22 }} />

  const mine = list.mine
  const playable = list.tracks.filter((x) => x.hasFile).length

  async function run<T>(job: Promise<T>): Promise<T | undefined> {
    try {
      return await job
    } catch (e) {
      toast.error(e instanceof Error ? e.message : String(e))
      load()
      return undefined
    }
  }

  async function saveName() {
    if (rename === null || !list) return
    const n = rename.trim()
    setRename(null)
    if (!n || n === list.name) return
    const r = await run(api.updateMusicPlaylist(list.id, { name: n }))
    if (r) setList(r)
  }

  async function setShared(v: boolean) {
    if (!list) return
    const r = await run(api.updateMusicPlaylist(list.id, { shared: v }))
    if (r) {
      setList(r)
      toast.success(v ? t('Everyone on this server can now see this playlist.') : t('Only you can see this playlist now.'))
    }
  }

  async function remove() {
    if (!list) return
    const answer = await confirm({
      title: t('Delete {name}?', { name: list.name }),
      body: <p>{t('The playlist is deleted. The music in your library is not touched.')}</p>,
      confirmLabel: t('Delete'),
      danger: true,
    })
    if (!answer) return
    if ((await run(api.deleteMusicPlaylist(list.id))) !== undefined) {
      toast.success(t('{name} deleted.', { name: list.name }))
      navigate('/music/playlists')
    }
  }

  async function reorder(from: number, to: number) {
    if (!list || from === to || to < 0 || to >= list.tracks.length) return
    const tracks = list.tracks.slice()
    const [moved] = tracks.splice(from, 1)
    tracks.splice(to, 0, moved)
    setList({ ...list, tracks }) // shown at once, then confirmed by the server
    const r = await run(api.reorderMusicPlaylist(list.id, tracks.map((x) => x.entryId)))
    if (r) setList(r)
  }

  async function removeEntry(entryId: number) {
    if (!list) return
    const r = await run(api.removeFromMusicPlaylist(list.id, entryId))
    if (r) setList(r)
  }

  return (
    <div className="mpl-page">
      <Link to="/music/playlists" className="mpl-crumb">
        <Icon name="chevron-left" size={14} /> {t('Playlists')}
      </Link>
      <section className="mpl-hero">
        <Mosaic albumIds={list.albumIds} size="big" />
        <div className="mpl-hero-info">
          {rename === null ? (
            <h1>
              {list.name}
              {mine && (
                <button type="button" className="icon-btn mpl-rename" onClick={() => setRename(list.name)} aria-label={t('Rename this playlist')} title={t('Rename')}>
                  <Icon name="edit" size={15} />
                </button>
              )}
            </h1>
          ) : (
            <form
              className="mpl-rename-form"
              onSubmit={(e) => {
                e.preventDefault()
                void saveName()
              }}
            >
              <input type="text" value={rename} onChange={(e) => setRename(e.target.value)} onKeyDown={(e) => e.key === 'Escape' && setRename(null)} aria-label={t('Name of the playlist')} maxLength={100} autoFocus />
              <button type="submit" className="btn-sm primary">
                {t('Save')}
              </button>
              <button type="button" className="btn-sm" onClick={() => setRename(null)}>
                {t('Cancel')}
              </button>
            </form>
          )}
          <p className="mpl-meta">
            {[plural(list.trackCount, '{n} track', '{n} tracks'), list.trackCount > 0 ? span(list.durationMs) : null, mine ? null : t('by {name}', { name: list.owner })].filter(Boolean).join(' · ')}
          </p>
          <div className="mpl-actions">
            {player && (
              <>
                <button type="button" className="primary btn-with-icon" disabled={playable === 0} onClick={() => void play(list.id, false)}>
                  <Icon name="play" size={15} /> {t('Play')}
                </button>
                <button type="button" className="btn-with-icon" disabled={playable === 0} onClick={() => void play(list.id, true)}>
                  <Icon name="shuffle" size={15} /> {t('Shuffle')}
                </button>
                <button
                  type="button"
                  className="btn-with-icon"
                  disabled={playable === 0}
                  onClick={() => {
                    player.addToQueue(list.tracks.filter((x) => x.hasFile).map(asPlayerTrack))
                    toast.success(t('Added to the queue.'))
                  }}
                >
                  <Icon name="list" size={15} /> {t('Add to queue')}
                </button>
              </>
            )}
            {mine && (
              <button type="button" className="btn-with-icon btn-danger" onClick={() => void remove()}>
                <Icon name="trash" size={15} /> {t('Delete')}
              </button>
            )}
          </div>
          {mine ? (
            <Switch checked={list.shared} onChange={(v) => void setShared(v)} label={t('Share with everyone')} description={t('Everyone on this server can see and play it. Only you can change it.')} />
          ) : (
            <p className="mpl-meta">{t('Shared by {name}. Only they can change it.', { name: list.owner })}</p>
          )}
        </div>
      </section>

      {list.tracks.length === 0 ? (
        <div className="empty-state">
          <Icon name="music" size={44} />
          <p>{t('This playlist is empty. Use the menu on a track, an album or an artist to add music.')}</p>
        </div>
      ) : (
        <ol className="mpl-tracks">
          {list.tracks.map((tr, i) => {
            const now = player?.current?.id === tr.trackId && player.queue.some((q) => q.id === tr.trackId)
            return (
              <li
                key={tr.entryId}
                className={`${tr.hasFile ? '' : 'lack'}${now ? ' now' : ''}${dragFrom === i ? ' dragging' : ''}${dragOver === i && dragFrom !== null && dragFrom !== i ? (dragFrom < i ? ' over-after' : ' over-before') : ''}`}
                draggable={mine}
                onDragStart={(e) => {
                  setDragFrom(i)
                  e.dataTransfer.effectAllowed = 'move'
                  e.dataTransfer.setData('text/plain', String(i))
                }}
                onDragOver={(e) => {
                  if (dragFrom === null) return
                  e.preventDefault()
                  if (dragOver !== i) setDragOver(i)
                }}
                onDrop={(e) => {
                  e.preventDefault()
                  const from = dragFrom
                  setDragFrom(null)
                  setDragOver(null)
                  if (from !== null) void reorder(from, i)
                }}
                onDragEnd={() => {
                  setDragFrom(null)
                  setDragOver(null)
                }}
              >
                {mine && (
                  <span className="np-grip" aria-hidden="true" title={t('Drag to move')}>
                    <Icon name="grip" size={16} />
                  </span>
                )}
                <button type="button" className="mpl-row" disabled={!tr.hasFile || !player} onClick={() => void play(list.id, false, tr.entryId)} title={tr.hasFile ? t('Play from here') : t('Not downloaded yet')}>
                  <span className="mpl-n">{now ? <Icon name={player?.playing ? 'play' : 'pause'} size={12} /> : i + 1}</span>
                  <img className="mpl-cover" src={albumCoverUrl(tr.albumId)} alt="" width={36} height={36} loading="lazy" onError={(e) => (e.currentTarget.style.visibility = 'hidden')} />
                  <span className="mpl-t">
                    <strong>{tr.title}</strong>
                    <small>
                      <Link to={`/music/artist/${tr.artistId}`} onClick={(e) => e.stopPropagation()}>
                        {tr.artist}
                      </Link>{' '}
                      · {tr.album}
                      {!tr.hasFile && <span className="mpl-miss"> · {t('not downloaded')}</span>}
                    </small>
                  </span>
                  <span className="mpl-d">{clock(tr.lengthMs / 1000)}</span>
                </button>
                <span className="mpl-acts">
                  {tr.hasFile && <MusicMenu target={{ kind: 'track', track: asPlayerTrack(tr) }} label={t('More for {title}', { title: tr.title })} className="mp-btn" />}
                  {mine && (
                    <>
                      <button type="button" className="mp-btn" onClick={() => void reorder(i, i - 1)} disabled={i === 0} aria-label={t('Move {title} up', { title: tr.title })} title={t('Move up')}>
                        <Icon name="chevron-up" size={15} />
                      </button>
                      <button type="button" className="mp-btn" onClick={() => void reorder(i, i + 1)} disabled={i === list.tracks.length - 1} aria-label={t('Move {title} down', { title: tr.title })} title={t('Move down')}>
                        <Icon name="chevron-down" size={15} />
                      </button>
                      <button type="button" className="mp-btn" onClick={() => void removeEntry(tr.entryId)} aria-label={t('Remove {title} from the playlist', { title: tr.title })} title={t('Remove from the playlist')}>
                        <Icon name="x" size={15} />
                      </button>
                    </>
                  )}
                </span>
              </li>
            )
          })}
        </ol>
      )}
    </div>
  )
}
