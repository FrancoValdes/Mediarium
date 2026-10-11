import { useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { api, isAdmin, type IndexerStat, type LibraryStats, type WatchStats, type WatchedStatTitle } from '../api'
import { useAuth } from '../AuthContext'
import { formatBytes, timeAgo } from '../format'
import KindGlyph from '../components/KindGlyph'
import Loading from '../components/Loading'
import OverTime from '../components/OverTime'
import { locale, plural, t } from '../i18n'
import '../stats.css'
import TipLabel from '../components/TipLabel'

// How much of a kind is in, as a ring around its shape.
function Ring({ pct }: { pct: number }) {
  return (
    <svg className="stat-ring" viewBox="0 0 44 44" aria-hidden="true">
      <circle cx="22" cy="22" r="19" pathLength={100} className="track" />
      <circle cx="22" cy="22" r="19" pathLength={100} className="fill" strokeDasharray={`${Math.max(pct, 0.5)} 100`} />
    </svg>
  )
}

// Statistics: what is in the library, at which quality, how much space it
// takes, and how downloads went month by month.
export default function Statistics() {
  const [s, setS] = useState<LibraryStats | null>(null)
  const [error, setError] = useState('')
  useEffect(() => {
    api
      .libraryStats()
      .then(setS)
      .catch((e) => setError(e instanceof Error ? e.message : String(e)))
  }, [])

  if (error) return <p className="error-text">{error}</p>
  if (!s) return <Loading height={300} />
  const qualityMax = Math.max(1, ...s.quality.map((q) => q.count))
  const monthMax = Math.max(1, ...s.months.map((m) => m.completed + m.failed))
  const pct = (a: number, b: number) => (b > 0 ? Math.round((a * 100) / b) : 0)
  return (
    <div className="stats-page">
      <div className="page-header">
        <h1>{t('Statistics')}</h1>
      </div>
      <div className="stat-cards">
        <div className="card stat-card k-movie">
          <span className="stat-shape">
            <Ring pct={pct(s.moviesHave, s.movies)} />
            <KindGlyph kind="movie" size={26} variant="solid" />
          </span>
          <small>{t('Movies')}</small>
          <strong>{s.movies}</strong>
          <span>
            {t('{n} downloaded ({pct}%)', { n: s.moviesHave, pct: pct(s.moviesHave, s.movies) })} · {formatBytes(s.movieBytes)}
          </span>
        </div>
        <div className="card stat-card k-tv">
          <span className="stat-shape">
            <Ring pct={pct(s.episodesHave, s.episodes)} />
            <KindGlyph kind="tv" size={26} variant="solid" />
          </span>
          <small>{t('TV shows')}</small>
          <strong>{s.shows}</strong>
          <span>
            {t('{have} of {total} episodes ({pct}%)', { have: s.episodesHave, total: s.episodes, pct: pct(s.episodesHave, s.episodes) })} · {formatBytes(s.episodeBytes)}
          </span>
        </div>
        {s.albums > 0 && (
          <div className="card stat-card k-music">
            <span className="stat-shape">
              <Ring pct={pct(s.albumsHave, s.albums)} />
              <KindGlyph kind="music" size={26} variant="solid" />
            </span>
            <small>{t('Albums')}</small>
            <strong>{s.albums}</strong>
            <span>
              {t('{n} downloaded ({pct}%)', { n: s.albumsHave, pct: pct(s.albumsHave, s.albums) })}
            </span>
          </div>
        )}
        <div className="card stat-card k-size">
          <small>{t('Library size')}</small>
          <strong>{formatBytes(s.movieBytes + s.episodeBytes)}</strong>
          <span>{s.usenetShare > 0 || s.months.length > 0 ? t('{pct}% of downloads came from Usenet', { pct: s.usenetShare }) : t('No downloads yet')}</span>
          {(s.usenetShare > 0 || s.months.length > 0) && (
            <span className="stat-split" aria-hidden="true">
              <i style={{ width: `${s.usenetShare}%` }} />
            </span>
          )}
        </div>
      </div>

      <OverTime />

      <div className="half-cols">
        <section className="card">
          <h2>{t('By quality')}</h2>
          {s.quality.length === 0 && <p style={{ color: 'var(--text-dim)' }}>{t('Nothing downloaded yet.')}</p>}
          <div className="bar-list">
            {s.quality.map((q) => (
              <div key={q.label} className="bar-row">
                <span className="bar-label">{q.label}</span>
                <span className="bar-track">
                  <span className="bar-fill" style={{ width: `${(q.count * 100) / qualityMax}%` }} />
                </span>
                <span className="bar-value">
                  {q.count} · {formatBytes(q.bytes ?? 0)}
                </span>
              </div>
            ))}
          </div>
        </section>
        <section className="card">
          <h2>{t('Downloads by month')}</h2>
          {s.months.length === 0 && <p style={{ color: 'var(--text-dim)' }}>{t('No downloads in the history yet.')}</p>}
          <div className="bar-list">
            {s.months.map((m) => (
              <div key={m.month} className="bar-row">
                <span className="bar-label">{new Date(`${m.month}-15`).toLocaleDateString(locale(), { month: 'short', year: 'numeric' })}</span>
                <span className="bar-track">
                  <span className="bar-fill" style={{ width: `${(m.completed * 100) / monthMax}%` }} />
                  <span className="bar-fill bad" style={{ width: `${(m.failed * 100) / monthMax}%` }} />
                </span>
                <span className="bar-value">
                  {m.failed > 0 ? t('{done} done, {failed} failed', { done: m.completed, failed: m.failed }) : t('{done} done', { done: m.completed })} · {formatBytes(m.bytes)}
                </span>
              </div>
            ))}
          </div>
          {s.historyKeptDays > 0 && <p className="field-hint">{t('The history is kept for {n} days, so older months are not counted.', { n: s.historyKeptDays })}</p>}
        </section>
      </div>
      <IndexerStats />
      {s.watched && <WatchedStats w={s.watched} />}
    </div>
  )
}

// How each indexer has answered over the last days: searches, failures, the
// share that worked, releases found and grabs. Administrators only, because it
// names the indexers.
function IndexerStats() {
  const admin = isAdmin(useAuth().user)
  const [data, setData] = useState<{ days: number; indexers: IndexerStat[] } | null>(null)
  useEffect(() => {
    if (admin) api.indexerStats().then(setData).catch(() => undefined)
  }, [admin])
  if (!admin || !data || data.indexers.length === 0) return null
  const busiest = Math.max(1, ...data.indexers.map((i) => i.queries))
  return (
    <section className="card" style={{ marginTop: 16 }}>
      <h2><TipLabel help="indexers#how-each-indexer-is-doing">{t('Indexers, last {n} days', { n: data.days })}</TipLabel></h2>
      <div style={{ overflowX: 'auto' }}>
        <table className="indexer-stats" style={{ width: '100%' }}>
          <thead>
            <tr>
              <th style={{ textAlign: 'left' }}>{t('Indexer')}</th>
              <th style={{ textAlign: 'right' }}>{t('Searches')}</th>
              <th style={{ textAlign: 'right' }}>{t('Failed')}</th>
              <th style={{ textAlign: 'right' }}>{t('Worked')}</th>
              <th style={{ textAlign: 'right' }}>{t('Releases found')}</th>
              <th style={{ textAlign: 'right' }}>{t('Grabbed')}</th>
            </tr>
          </thead>
          <tbody>
            {data.indexers.map((i) => (
              <tr key={i.id} style={{ opacity: i.enabled ? 1 : 0.55 }}>
                <td>
                  <strong>{i.name}</strong> <span className="badge">{i.protocol === 'torrent' ? t('Torrent') : t('Usenet')}</span>
                  {!i.enabled && <small> · {t('Off')}</small>}
                  {i.pausedUntil && <small> · {t('paused')}</small>}
                  <span className="bar-track" style={{ display: 'block', marginTop: 4, maxWidth: 220 }}>
                    <span className="bar-fill" style={{ width: `${(i.queries * 100) / busiest}%` }} />
                  </span>
                </td>
                <td style={{ textAlign: 'right' }}>{i.queries}</td>
                <td style={{ textAlign: 'right' }}>{i.failures}</td>
                <td style={{ textAlign: 'right' }}>{i.successRate < 0 ? '–' : `${Math.round(i.successRate * 100)}%`}</td>
                <td style={{ textAlign: 'right' }}>{i.results}</td>
                <td style={{ textAlign: 'right' }}>{i.grabs}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      <p className="field-hint">{t('A search is one question to one indexer. Grabs count the releases taken from it, by hand or by Mediarium. An indexer that is paused after repeated failures is not asked, and those skipped searches are not counted.')}</p>
    </section>
  )
}

function titleLink(item: WatchedStatTitle): string {
  return item.kind === 'series' ? `/series/${item.id}` : `/title/${item.tmdbId ?? 0}`
}

// What gets watched, read from Plex, Jellyfin and Emby (Settings > Media
// servers > "Read what's been watched").
function WatchedStats({ w }: { w: WatchStats }) {
  const pct = (a: number, b: number) => (a + b > 0 ? Math.round((a * 100) / (a + b)) : 0)
  const topMax = Math.max(1, ...w.top.map((item) => item.plays))
  return (
    <>
      <h2 style={{ marginTop: 28 }}>{t('What gets watched')}</h2>
      <div className="stat-cards">
        <div className="card stat-card">
          <small>{t('Movies watched')}</small>
          <strong>{w.moviesWatched}</strong>
          <span>
            {t('{pct}% of the {total} you have', { pct: pct(w.moviesWatched, w.moviesUnwatched), total: w.moviesWatched + w.moviesUnwatched })}
          </span>
        </div>
        <div className="card stat-card">
          <small>{t('Episodes watched')}</small>
          <strong>{w.episodesWatched}</strong>
          <span>
            {t('{pct}% of the {total} you have', { pct: pct(w.episodesWatched, w.episodesUnwatched), total: w.episodesWatched + w.episodesUnwatched })}
          </span>
        </div>
        <div className="card stat-card">
          <small>{t('Never watched')}</small>
          <strong>{formatBytes(w.unwatchedBytes)}</strong>
          <span>
            {t('{movies} and {episodes} nobody has played', {
              movies: plural(w.moviesUnwatched, '{n} movie', '{n} movies'),
              episodes: plural(w.episodesUnwatched, '{n} episode', '{n} episodes'),
            })}
          </span>
        </div>
        <div className="card stat-card">
          <small>{t('Plays')}</small>
          <strong>{w.plays}</strong>
          <span>{w.lastSync ? t('Checked {when}', { when: timeAgo(w.lastSync) }) : t('Not checked yet')}</span>
        </div>
      </div>
      <div className="half-cols">
        <section className="card">
          <h2>{t('Most watched')}</h2>
          {w.top.length === 0 && <p style={{ color: 'var(--text-dim)' }}>{t('Nothing watched yet.')}</p>}
          <div className="bar-list">
            {w.top.map((item) => (
              <div key={`${item.kind}-${item.id}`} className="bar-row">
                <Link className="bar-label" to={titleLink(item)} title={item.title}>
                  {item.title}
                </Link>
                <span className="bar-track">
                  <span className="bar-fill" style={{ width: `${(item.plays * 100) / topMax}%` }} />
                </span>
                <span className="bar-value">
                  {plural(item.plays, '{n} play', '{n} plays')}
                  {item.kind === 'series' ? ` · ${plural(item.episodes ?? 0, '{n} episode', '{n} episodes')}` : ''}
                </span>
              </div>
            ))}
          </div>
        </section>
        <section className="card">
          <h2>{t('Watched lately')}</h2>
          {w.recent.length === 0 && (
            <p style={{ color: 'var(--text-dim)' }}>{w.top.length === 0 ? t('Nothing watched yet.') : t("Your media servers haven't said when things were played.")}</p>
          )}
          <div className="bar-list">
            {w.recent.map((item) => (
              <div key={`${item.kind}-${item.id}`} className="bar-row recent-row">
                <Link className="bar-label" to={titleLink(item)} title={item.title}>
                  {item.title}
                </Link>
                <span className="bar-value">{timeAgo(item.lastPlayed)}</span>
              </div>
            ))}
          </div>
        </section>
      </div>
    </>
  )
}
