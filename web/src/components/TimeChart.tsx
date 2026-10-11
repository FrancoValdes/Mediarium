import { useEffect, useRef, useState } from 'react'
import { locale, t } from '../i18n'

// A small chart of one measure over time: a line (with a soft area under it)
// or bars. Hovering (or touching) shows the value of the nearest point. A
// table of the same numbers is one click away.

export interface TimePoint {
  x: string // "2026-10-06", or a week's first day
  y: number | null // null: no value that day (a gap)
}

const PAD = { top: 12, right: 12, bottom: 24, left: 52 }

function niceMax(v: number): number {
  if (v <= 0) return 1
  const pow = Math.pow(10, Math.floor(Math.log10(v)))
  for (const step of [1, 2, 2.5, 5, 10]) {
    if (v <= step * pow) return step * pow
  }
  return 10 * pow
}

const shortDate = (d: string) => new Date(d + 'T00:00:00Z').toLocaleDateString(locale(), { day: 'numeric', month: 'short', timeZone: 'UTC' })

export default function TimeChart({
  points,
  kind = 'line',
  format,
  label,
  whole = false,
  emptyText = t('Nothing to show yet.'),
}: {
  points: TimePoint[]
  kind?: 'line' | 'bar'
  format: (v: number) => string
  label: string
  // whole: the values are counts, so the scale keeps to whole numbers.
  whole?: boolean
  emptyText?: string
}) {
  const box = useRef<HTMLDivElement>(null)
  const [width, setWidth] = useState(600)
  const [hover, setHover] = useState<number | null>(null)
  useEffect(() => {
    const el = box.current
    if (!el) return
    const ro = new ResizeObserver(([e]) => setWidth(Math.max(260, Math.round(e.contentRect.width))))
    ro.observe(el)
    return () => ro.disconnect()
  }, [])

  const height = 180
  const values = points.map((p) => p.y).filter((v): v is number => v !== null)
  if (points.length === 0 || values.length === 0) return <p className="chart-empty">{emptyText}</p>
  let max = niceMax(Math.max(...values))
  if (whole && max < 2) max = 2
  const innerW = width - PAD.left - PAD.right
  const innerH = height - PAD.top - PAD.bottom
  const n = points.length
  const xAt = (i: number) => PAD.left + (kind === 'bar' ? (innerW / n) * (i + 0.5) : n === 1 ? innerW / 2 : (innerW * i) / (n - 1))
  const yAt = (v: number) => PAD.top + innerH - (innerH * v) / max

  // The line, broken where a day has no value.
  let path = ''
  let area = ''
  let run: [number, number][] = []
  const flush = () => {
    if (run.length === 0) return
    path += run.map(([x, y], i) => `${i === 0 ? 'M' : 'L'}${x.toFixed(1)},${y.toFixed(1)}`).join('')
    area += `M${run[0][0].toFixed(1)},${(PAD.top + innerH).toFixed(1)}` + run.map(([x, y]) => `L${x.toFixed(1)},${y.toFixed(1)}`).join('') + `L${run[run.length - 1][0].toFixed(1)},${(PAD.top + innerH).toFixed(1)}Z`
    run = []
  }
  points.forEach((p, i) => {
    if (p.y === null) flush()
    else run.push([xAt(i), yAt(p.y)])
  })
  flush()

  const barW = Math.max(2, innerW / n - 2)
  const allZero = Math.max(...values) === 0
  const ticks = allZero ? [0] : whole && max % 2 !== 0 ? [0, max] : [0, max / 2, max]

  function pick(clientX: number) {
    const el = box.current
    if (!el) return
    const x = clientX - el.getBoundingClientRect().left
    let best = 0
    let dist = Infinity
    points.forEach((p, i) => {
      const d = Math.abs(xAt(i) - x)
      if (d < dist && p.y !== null) {
        dist = d
        best = i
      }
    })
    setHover(best)
  }

  const hp = hover !== null ? points[hover] : null
  return (
    <figure className="time-chart" aria-label={label}>
      <div
        ref={box}
        className="time-chart-box"
        onMouseMove={(e) => pick(e.clientX)}
        onMouseLeave={() => setHover(null)}
        onTouchStart={(e) => pick(e.touches[0].clientX)}
        onTouchMove={(e) => pick(e.touches[0].clientX)}
      >
        <svg width={width} height={height} role="img" aria-label={label}>
          {ticks.map((t) => (
            <g key={t}>
              <line x1={PAD.left} x2={width - PAD.right} y1={yAt(t)} y2={yAt(t)} className="time-chart-grid" />
              <text x={PAD.left - 6} y={yAt(t)} className="time-chart-axis" textAnchor="end" dominantBaseline="middle">
                {format(t)}
              </text>
            </g>
          ))}
          <text x={PAD.left} y={height - 6} className="time-chart-axis">
            {shortDate(points[0].x)}
          </text>
          <text x={width - PAD.right} y={height - 6} className="time-chart-axis" textAnchor="end">
            {shortDate(points[n - 1].x)}
          </text>
          {kind === 'line' ? (
            <>
              <path d={area} className="time-chart-area" />
              <path d={path} className="time-chart-line" />
              {points.map((p, i) => {
                // A point with no neighbour draws no line, so show it as a dot.
                const alone = p.y !== null && (points[i - 1]?.y ?? null) === null && (points[i + 1]?.y ?? null) === null
                return alone ? <circle key={p.x} cx={xAt(i)} cy={yAt(p.y as number)} r={3.5} className="time-chart-dot" /> : null
              })}
            </>
          ) : (
            points.map((p, i) =>
              p.y ? (
                <rect key={p.x} x={xAt(i) - barW / 2} y={yAt(p.y)} width={barW} height={PAD.top + innerH - yAt(p.y)} rx={Math.min(4, barW / 2)} className={`time-chart-bar${hover === i ? ' on' : ''}`} />
              ) : null,
            )
          )}
          {hp && hp.y !== null && hover !== null && (
            <g>
              <line x1={xAt(hover)} x2={xAt(hover)} y1={PAD.top} y2={PAD.top + innerH} className="time-chart-cross" />
              {kind === 'line' && <circle cx={xAt(hover)} cy={yAt(hp.y)} r={4.5} className="time-chart-dot" />}
            </g>
          )}
        </svg>
        {hp && hp.y !== null && hover !== null && (
          <div className="time-chart-tip" style={{ left: Math.min(Math.max(xAt(hover), 70), width - 70) }}>
            <strong>{format(hp.y)}</strong>
            <span>{kind === 'bar' ? t('Week of {date}', { date: shortDate(hp.x) }) : shortDate(hp.x)}</span>
          </div>
        )}
      </div>
      <details className="time-chart-table">
        <summary>{t('Show as a table')}</summary>
        <table>
          <thead>
            <tr>
              <th>{kind === 'bar' ? t('Week of') : t('Day')}</th>
              <th>{label}</th>
            </tr>
          </thead>
          <tbody>
            {[...points].reverse().filter((p) => p.y !== null).slice(0, 120).map((p) => (
              <tr key={p.x}>
                <td>{shortDate(p.x)}</td>
                <td>{format(p.y as number)}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </details>
    </figure>
  )
}
