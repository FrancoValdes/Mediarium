import { useEffect, useMemo, useState } from 'react'
import TimeChart, { type TimePoint } from './TimeChart'
import { api, type StatsDay } from '../api'
import { formatBytes } from '../format'
import { locale, t } from '../i18n'

// "Over time" on the Statistics page: how the library grew and how many
// downloads came in each week, from the daily snapshots.
const RANGES = [
  { days: 90, label: t('3 months') },
  { days: 365, label: t('A year') },
  { days: 0, label: t('All') },
]

// weekStart is the Monday of a day's week ("YYYY-MM-DD").
function weekStart(day: string): string {
  const d = new Date(day + 'T00:00:00Z')
  const back = (d.getUTCDay() + 6) % 7
  d.setUTCDate(d.getUTCDate() - back)
  return d.toISOString().slice(0, 10)
}

export default function OverTime() {
  const [range, setRange] = useState(90)
  const [days, setDays] = useState<StatsDay[] | null>(null)
  const [error, setError] = useState('')
  useEffect(() => {
    let live = true
    api
      .statsHistory()
      .then((r) => live && setDays(r.days))
      .catch((e) => live && setError(e instanceof Error ? e.message : String(e)))
    return () => {
      live = false
    }
  }, [])

  const shown = useMemo(() => (days && range > 0 ? days.slice(-range) : (days ?? [])), [days, range])
  const size: TimePoint[] = shown.map((d) => ({ x: d.day, y: d.libraryBytes >= 0 ? d.libraryBytes : null }))
  const titles: TimePoint[] = shown.map((d) => ({ x: d.day, y: d.movies + d.shows }))
  const weeks = useMemo(() => {
    const byWeek = new Map<string, number>()
    for (const d of shown) byWeek.set(weekStart(d.day), (byWeek.get(weekStart(d.day)) ?? 0) + d.completed)
    return [...byWeek.entries()].map(([x, y]) => ({ x, y }))
  }, [shown])

  if (error) return <p className="error-text">{error}</p>
  if (!days) return null
  if (days.length === 0) {
    return (
      <section className="card wide">
        <h2>{t('Over time')}</h2>
        <p style={{ color: 'var(--text-dim)' }}>{t('Nothing yet. Mediarium keeps a note of your library once a day, and the charts start as soon as there is something in it.')}</p>
      </section>
    )
  }
  const firstMeasured = days.find((d) => d.libraryBytes >= 0)?.day
  const sizeNote = firstMeasured && shown.length > 0 && shown[0].day < firstMeasured ? t('Measured from {date}.', { date: new Date(firstMeasured + 'T00:00:00Z').toLocaleDateString(locale(), { day: 'numeric', month: 'long', year: 'numeric', timeZone: 'UTC' }) }) : ''
  return (
    <section className="card wide over-time">
      <h2>{t('Over time')}</h2>
      <div className="over-time-range">
        <div className="seg" role="radiogroup" aria-label={t('How far back')}>
          {RANGES.map((r) => (
            <button key={r.days} role="radio" aria-checked={range === r.days} className={range === r.days ? 'active' : ''} onClick={() => setRange(r.days)}>
              {r.label}
            </button>
          ))}
        </div>
      </div>
      <div className="over-time-charts">
        <div>
          <h3>{t('Library size')}</h3>
          {sizeNote && <small className="chart-note">{sizeNote}</small>}
          <TimeChart points={size} format={(v) => (v === 0 ? '0' : formatBytes(v))} label={t('Library size')} emptyText={t('Measured once a day from now on; the first point appears with the next note.')} />
        </div>
        <div>
          <h3>{t('Movies and shows in your library')}</h3>
          <TimeChart points={titles} format={(v) => Math.round(v).toLocaleString(locale())} label={t('Movies and shows')} whole />
        </div>
        <div>
          <h3>{t('Downloads per week')}</h3>
          <TimeChart points={weeks} kind="bar" format={(v) => Math.round(v).toLocaleString(locale())} label={t('Downloads finished')} whole />
        </div>
      </div>
    </section>
  )
}
