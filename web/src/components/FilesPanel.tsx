import { createPortal } from 'react-dom'
import { useCallback, useEffect, useRef, useState } from 'react'
import { useFocusTrap } from '../useFocusTrap'
import { api, can, type TitleFile } from '../api'
import { useAuth } from '../AuthContext'
import SubtitleTiming from './SubtitleTiming'
import { formatBytes, timeAgo } from '../format'
import { useLive } from '../useLive'
import Icon from './Icon'
import { t } from '../i18n'
import { languageName } from '../languages'
import { srtToVtt, subtitleTracksFor } from '../subtitleTracks'

const KIND_ICON: Record<TitleFile['kind'], string> = { video: 'film', audio: 'music', subtitle: 'chat', image: 'image', nfo: 'info', other: 'folder' }

// The files in a movie's, show's or album's folder on disk, with a Play button
// for videos and songs. Playback is direct (no converting), so it works for
// formats the browser understands: usually MP4 and WebM for video, MP3 and AAC
// for music, and FLAC in current browsers.
export default function FilesPanel({ kind, id }: { kind: 'movie' | 'series' | 'album'; id: number }) {
  const [files, setFiles] = useState<TitleFile[] | null>(null)
  const [error, setError] = useState('')
  const [playing, setPlaying] = useState<TitleFile | null>(null)
  const [viewing, setViewing] = useState<TitleFile | null>(null)
  const [timing, setTiming] = useState('')
  const me = useAuth().user

  const load = useCallback(() => {
    ;(kind === 'movie' ? api.movieFiles(id) : kind === 'album' ? api.albumFiles(id) : api.seriesFiles(id))
      .then((r) => {
        setFiles(r.files)
        setError('')
      })
      .catch((e) => setError(e instanceof Error ? e.message : String(e)))
  }, [kind, id])
  useEffect(load, [load])
  useLive(load, 30000)

  if (error) return <p className="error-text">{error}</p>
  if (files === null) return <div className="skeleton" style={{ height: 120 }} />
  if (files.length === 0) return <p style={{ color: 'var(--text-dim)', margin: 0 }}>{kind === 'album' ? t('No files for this album on disk yet.') : t("No files in this title's folder yet.")}</p>

  const streamOf = (path: string) => (kind === 'album' ? api.albumStreamUrl(id, path) : api.streamUrl(kind, id, path))
  const sorted = [...files].sort((a, b) => Number(b.main) - Number(a.main) || (a.kind === 'video' || a.kind === 'audio' ? -1 : 0) - (b.kind === 'video' || b.kind === 'audio' ? -1 : 0) || a.path.localeCompare(b.path))

  return (
    <>
      <ul className="file-list">
        {sorted.map((f) => (
          <li key={f.path} className={f.main ? 'main' : undefined}>
            <span className={`file-ico kind-${f.kind}`}>
              <Icon name={KIND_ICON[f.kind]} size={16} />
            </span>
            <div className="file-name">
              <span title={f.path}>{f.path}</span>
              <small>
                {formatBytes(f.size)} · {timeAgo(f.modified)}
                {f.main && ' · ' + t('in your library')}
              </small>
            </div>
            {(f.kind === 'video' || f.kind === 'audio') && (
              <button className="btn-sm btn-with-icon" onClick={() => setPlaying(f)}>
                <Icon name="play" size={14} /> {t('Play')}
              </button>
            )}
            {(f.kind === 'image' || f.kind === 'subtitle' || f.kind === 'nfo') && (
              <button className="btn-sm btn-with-icon" onClick={() => setViewing(f)}>
                <Icon name="eye" size={14} /> {t('View')}
              </button>
            )}
            {f.kind === 'subtitle' && kind !== 'album' && /\.(srt|vtt)$/i.test(f.path) && can(me, 'subtitles') && (
              <button className={`btn-sm btn-with-icon${timing === f.path ? ' primary' : ''}`} onClick={() => setTiming(timing === f.path ? '' : f.path)} aria-expanded={timing === f.path}>
                <Icon name="clock" size={14} /> {t('Timing')}
              </button>
            )}
            {timing === f.path && kind !== 'album' && (
              <SubtitleTiming
                kind={kind}
                id={id}
                file={f.path}
                others={sorted.filter((o) => o.kind === 'subtitle' && o.path !== f.path && /\.(srt|vtt)$/i.test(o.path)).map((o) => o.path)}
                onDone={load}
              />
            )}
          </li>
        ))}
      </ul>
      {playing && (
        <Player
          src={streamOf(playing.path)}
          name={playing.path}
          audio={playing.kind === 'audio'}
          subs={playing.kind === 'video' ? subtitleTracksFor(playing.path, files).map((s) => ({ ...s, url: streamOf(s.path) })) : []}
          onClose={() => setPlaying(null)}
        />
      )}
      {viewing && <Viewer file={viewing} src={streamOf(viewing.path)} onClose={() => setViewing(null)} />}
    </>
  )
}

// A video's subtitles are read from the files next to it and handed to the
// browser's own player as tracks (it shows them from the CC menu).
function Player({ src, name, audio, subs = [], onClose }: { src: string; name: string; audio?: boolean; subs?: { path: string; code: string; url: string }[]; onClose: () => void }) {
  const box = useRef<HTMLDivElement>(null)
  useFocusTrap(box)
  const [failed, setFailed] = useState(false)
  const [tracks, setTracks] = useState<{ code: string; url: string }[]>([])
  const subKey = subs.map((s) => s.url).join('|')
  useEffect(() => {
    if (subs.length === 0) return
    let cancelled = false
    const made: string[] = []
    void Promise.all(
      subs.map(async (s) => {
        try {
          const r = await fetch(s.url, { credentials: 'include' })
          if (!r.ok) return null
          const url = URL.createObjectURL(new Blob([srtToVtt(await r.text())], { type: 'text/vtt' }))
          made.push(url)
          return { code: s.code, url }
        } catch {
          return null // a subtitle that can't be read is just left out
        }
      }),
    ).then((list) => {
      if (!cancelled) setTracks(list.filter((x): x is { code: string; url: string } => !!x))
    })
    return () => {
      cancelled = true
      made.forEach((u) => URL.revokeObjectURL(u))
    }
    // subs is rebuilt on every render; its addresses are what matter.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [subKey])
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => e.key === 'Escape' && onClose()
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [onClose])
  return createPortal(
    <div className="modal-backdrop" onClick={onClose}>
      <div className={`player${audio ? ' audio' : ''}`} onClick={(e) => e.stopPropagation()} role="dialog" aria-modal="true" ref={box} tabIndex={-1} aria-label={t('Playing {name}', { name })}>
        <div className="player-head">
          <strong title={name}>{name.split('/').pop()}</strong>
          <button className="icon-btn" onClick={onClose} aria-label={t('Close')}>
            <Icon name="x" size={18} />
          </button>
        </div>
        {failed ? (
          <div className="player-failed">
            <Icon name="warning" size={28} />
            <p>
              {audio
                ? t("Your browser can't play this file's format (FLAC needs a recent browser, and Apple Lossless only plays in Safari). The file itself is fine. Try a media player instead, such as Plex, Jellyfin, Navidrome or VLC.")
                : t("Your browser can't play this file's format. The file itself is fine. Try a media player instead, such as Plex, Jellyfin, Kodi or VLC.")}
            </p>
          </div>
        ) : (
          audio ? <audio src={src} controls autoPlay onError={() => setFailed(true)} /> : (
            <video src={src} controls autoPlay onError={() => setFailed(true)}>
              {tracks.map((tr, i) => (
                <track key={tr.url} kind="subtitles" src={tr.url} srcLang={tr.code} label={languageName(tr.code)} default={i === 0} />
              ))}
            </video>
          )
        )}
      </div>
    </div>,
    document.body,
  )
}

// Shows a picture, or the text of an NFO or subtitle file, in the app.
function Viewer({ file, src, onClose }: { file: TitleFile; src: string; onClose: () => void }) {
  const box = useRef<HTMLDivElement>(null)
  useFocusTrap(box)
  const [text, setText] = useState<string | null>(null)
  const [failed, setFailed] = useState(false)
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => e.key === 'Escape' && onClose()
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [onClose])
  useEffect(() => {
    if (file.kind === 'image') return
    fetch(src, { credentials: 'include' })
      .then((r) => (r.ok ? r.text() : Promise.reject(new Error(String(r.status)))))
      .then(setText)
      .catch(() => setFailed(true))
  }, [file.kind, src])

  return createPortal(
    <div className="modal-backdrop" onClick={onClose}>
      <div className="player viewer" onClick={(e) => e.stopPropagation()} role="dialog" aria-modal="true" ref={box} tabIndex={-1} aria-label={t('Viewing {name}', { name: file.path })}>
        <div className="player-head">
          <strong title={file.path}>{file.path.split('/').pop()}</strong>
          <button className="icon-btn" onClick={onClose} aria-label={t('Close')}>
            <Icon name="x" size={18} />
          </button>
        </div>
        {failed ? (
          <div className="player-failed">
            <Icon name="warning" size={28} />
            <p>{t("This file can't be shown here.")}</p>
          </div>
        ) : file.kind === 'image' ? (
          <img src={src} alt="" onError={() => setFailed(true)} />
        ) : text === null ? (
          <div className="skeleton" style={{ height: 200, margin: 16 }} />
        ) : (
          <pre className="viewer-text">{text}</pre>
        )}
      </div>
    </div>,
    document.body,
  )
}
