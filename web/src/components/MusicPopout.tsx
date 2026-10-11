import { useEffect, useState } from 'react'
import { t } from '../i18n'
import { clock } from '../musicAudio'
import Icon from './Icon'
import { albumCoverUrl, MusicPlayerProvider, useMusicPlayer } from './MusicPlayer'
import '../player.css'

// The music player in a window of its own, like the audiobook player: the
// cover, the controls and the queue, with nothing else around it. It carries on
// from where the app window left off, and gives the music back when you press
// "Back to the app".
function NowPlaying() {
  const p = useMusicPlayer()
  const current = p?.current
  const [noCover, setNoCover] = useState<number | null>(null) // the album whose cover is missing
  useEffect(() => {
    document.documentElement.classList.add('music-popout-mode')
    return () => document.documentElement.classList.remove('music-popout-mode')
  }, [])
  useEffect(() => {
    document.title = current ? `${current.title} · ${current.artist}` : t('Music player')
  }, [current])
  if (!p) return null
  const c = p.current
  const total = p.duration || (c ? c.lengthMs / 1000 : 0)
  const pct = total > 0 ? Math.min(100, (p.time / total) * 100) : 0

  if (!c) {
    return (
      <main className="mp-pop mp-pop-empty">
        <Icon name="music" size={36} />
        <h1>{p.taken ? t('Music is playing in the app window now') : t('Nothing is playing')}</h1>
        <p>{p.taken ? t('You can close this window.') : t('Choose music in the Mediarium window and press Play, then open the player in its own window.')}</p>
        <button onClick={() => window.close()}>{t('Close this window')}</button>
      </main>
    )
  }

  const repeatLabel = p.repeat === 'one' ? t('Repeat this track') : p.repeat === 'all' ? t('Repeat the queue') : t('Repeat')

  return (
    <main className="mp-pop">
      {noCover === c.albumId ? (
        <span className="mp-pop-cover mp-pop-nocover" aria-hidden="true">
          <Icon name="music" size={64} />
        </span>
      ) : (
        <img className="mp-pop-cover" src={albumCoverUrl(c.albumId)} alt="" width={260} height={260} onError={() => setNoCover(c.albumId)} />
      )}
      <div className="mp-pop-info">
        <h1 title={c.title}>{c.title}</h1>
        <p title={`${c.artist} · ${c.album}`}>{p.problem || [c.artist, c.album].filter(Boolean).join(' · ')}</p>
      </div>
      <div className="mini-player mp-pop-bar">
        <div className="mp-seek">
          <span className="mp-time">{clock(p.time)}</span>
          <input type="range" min={0} max={Math.max(total, 1)} step={1} value={Math.min(p.time, Math.max(total, 1))} onChange={(e) => p.seek(Number(e.target.value))} aria-label={t('Seek')} style={{ ['--p' as string]: `${pct}%` }} />
          <span className="mp-time">{clock(total)}</span>
        </div>
        <div className="mp-pop-controls">
          <button type="button" className={`mp-btn${p.shuffle ? ' on' : ''}`} onClick={() => p.setShuffle(!p.shuffle)} aria-pressed={p.shuffle} aria-label={t('Shuffle')} title={t('Shuffle')}>
            <Icon name="shuffle" size={17} />
          </button>
          <button type="button" className="mp-btn" onClick={p.prev} aria-label={t('Previous track')} title={t('Previous track')}>
            <Icon name="skip-prev" size={18} />
          </button>
          <button type="button" className="mp-btn mp-play" onClick={p.toggle} aria-label={p.playing ? t('Pause') : t('Play')} title={p.playing ? t('Pause') : t('Play')}>
            <Icon name={p.playing ? 'pause' : 'play'} size={20} />
          </button>
          <button type="button" className="mp-btn" onClick={p.next} aria-label={t('Next track')} title={t('Next track')}>
            <Icon name="skip-next" size={18} />
          </button>
          <button type="button" className={`mp-btn${p.repeat !== 'off' ? ' on' : ''}`} onClick={p.cycleRepeat} aria-label={repeatLabel} title={repeatLabel}>
            <Icon name="repeat" size={17} />
            {p.repeat === 'one' && <b className="mp-one">1</b>}
          </button>
        </div>
        <label className="mp-vol mp-pop-vol" title={t('Volume')}>
          <Icon name={p.volume === 0 ? 'volume-off' : 'volume'} size={17} />
          <input type="range" min={0} max={1} step={0.05} value={p.volume} onChange={(e) => p.setVolume(Number(e.target.value))} aria-label={t('Volume')} style={{ ['--p' as string]: `${p.volume * 100}%` }} />
        </label>
      </div>
      <ol className="mp-pop-queue" aria-label={t('Up next')}>
        {p.queue.map((tr, i) => (
          <li key={tr.uid} className={i === p.index ? 'now' : ''}>
            <button type="button" onClick={() => p.jumpTo(i)}>
              <span className="mp-pop-n">{i === p.index && p.playing ? '♪' : i + 1}</span>
              <span className="mp-pop-t">{tr.title}</span>
              <span className="mp-pop-d">{clock(tr.lengthMs / 1000)}</span>
            </button>
          </li>
        ))}
      </ol>
      <button type="button" className="mp-pop-back" onClick={p.handBack}>
        <Icon name="open" size={14} /> {t('Back to the app')}
      </button>
    </main>
  )
}

export default function MusicPopout() {
  return (
    <MusicPlayerProvider popout>
      <NowPlaying />
    </MusicPlayerProvider>
  )
}
