import { useCallback, useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { api, ApiError, type SystemStats } from '../api'
import { formatBytes } from '../format'
import { useLive } from '../useLive'
import { locale, plural, t, tx } from '../i18n'
import Icon from './Icon'

const pct = (used: number, total: number) => (total > 0 ? Math.round((used / total) * 100) : 0)

function uptime(seconds: number): string {
  const d = Math.floor(seconds / 86400)
  const h = Math.floor((seconds % 86400) / 3600)
  const m = Math.floor((seconds % 3600) / 60)
  return d > 0 ? t('{d}d {h}h', { d, h }) : h > 0 ? t('{h}h {m}m', { h, m }) : t('{m}m', { m })
}

// The same disk often holds several folders; show each disk once, naming
// the folders on it.
export function uniqueDisks(stats: SystemStats) {
  const out: { labels: string[]; freeBytes: number; totalBytes: number; usedBytes: number }[] = []
  for (const d of stats.disks) {
    const same = out.find((o) => o.totalBytes === d.totalBytes && Math.abs(o.freeBytes - d.freeBytes) < 64 * 1024 * 1024)
    if (same) same.labels.push(d.label)
    else out.push({ labels: [d.label], freeBytes: d.freeBytes, totalBytes: d.totalBytes, usedBytes: d.usedBytes })
  }
  return out
}

export function useServerStats(ms = 10000) {
  const [stats, setStats] = useState<SystemStats | null>(null)
  const [denied, setDenied] = useState(false)
  const load = useCallback(() => {
    api
      .systemStats()
      .then((s) => {
        setStats(s)
        setDenied(false)
      })
      .catch((e) => {
        // Only an answer of "not for you" hides the card. A refresh that fails for another reason keeps what is showing.
        if (e instanceof ApiError && (e.status === 401 || e.status === 403 || e.status === 404)) setDenied(true)
      })
  }, [])
  useEffect(load, [load])
  useLive(load, ms)
  return { stats, denied }
}

export type Level = 'ok' | 'warn' | 'bad'
const levelOf = (p: number): Level => (p >= 90 ? 'bad' : p >= 75 ? 'warn' : 'ok')
const LEVEL_TEXT: Record<Level, string> = { ok: t('Everything looks fine'), warn: t('Getting busy or full'), bad: t('Running low on something') }

// Everything the two Server cards show, worked out once: how busy the
// processor and memory are, the storage in use, and one overall status
// (the worst of the three).
export function summarize(stats: SystemStats) {
  // Under 10% show one decimal, so a lightly used server reads 0.4% instead of a flat 0%.
  const roundPct = (n: number) => (n < 10 ? Math.round(n * 10) / 10 : Math.round(n))
  const cpu = stats.cpu ? roundPct(stats.cpu.percent) : undefined
  const appCpu = stats.app.cpuPercent !== undefined ? roundPct(stats.app.cpuPercent) : undefined
  const mem = stats.memory ? { ...stats.memory, pct: pct(stats.memory.usedBytes, stats.memory.totalBytes) } : undefined
  let storage = stats.storage
  if (!storage) {
    const disks = uniqueDisks(stats)
    if (disks.length > 0) {
      storage = {
        usedBytes: disks.reduce((n, d) => n + d.usedBytes, 0),
        freeBytes: disks.reduce((n, d) => n + d.freeBytes, 0),
        totalBytes: disks.reduce((n, d) => n + d.totalBytes, 0),
      }
    }
  }
  const store = storage && storage.totalBytes > 0 ? { ...storage, pct: pct(storage.usedBytes, storage.totalBytes) } : undefined
  const levels: Level[] = [cpu !== undefined ? levelOf(cpu) : 'ok', mem ? levelOf(mem.pct) : 'ok', store ? levelOf(store.pct) : 'ok']
  const level: Level = levels.includes('bad') ? 'bad' : levels.includes('warn') ? 'warn' : 'ok'
  return { cpu, appCpu, mem, store, level }
}

function StatusDot({ level }: { level: Level }) {
  return <span className={`srv-dot ${level}`} role="img" aria-label={LEVEL_TEXT[level]} title={LEVEL_TEXT[level]} />
}

function Bar({ used, total, label, detail, sub }: { used: number; total: number; label: string; detail: string; sub?: string }) {
  const p = pct(used, total)
  return (
    <div className="srv-bar">
      <div className="srv-bar-top">
        <span>{label}</span>
        <b>{p}%</b>
      </div>
      <div className={`srv-track${p >= 90 ? ' hot' : p >= 75 ? ' warm' : ''}`}>
        <div style={{ width: `${p}%` }} />
      </div>
      <small className="srv-bar-detail">{detail}</small>
      {sub && <small className="srv-bar-detail">{sub}</small>}
    </div>
  )
}

// A thin bar that grows to its width once it has appeared, and again whenever
// the figure changes. Green until 75%, amber until 90%, then red.
function MiniBar({ value }: { value: number }) {
  const [shown, setShown] = useState(0)
  useEffect(() => {
    const id = requestAnimationFrame(() => setShown(Math.max(0, Math.min(100, value))))
    return () => cancelAnimationFrame(id)
  }, [value])
  return (
    <span className={`info-bar ${levelOf(value)}`} aria-hidden="true">
      <span style={{ width: `${shown}%` }} />
    </span>
  )
}

const NL = String.fromCharCode(10)
const exact = (n: number) => t('{n} bytes', { n })

// The compact "Server" card on the dashboard: a heading, then CPU, memory and
// storage each with its figures and a small coloured bar, and a status dot.
// Pressing it opens the details in place (load, uptimes, what Mediarium itself
// uses, one bar per folder); pressing again closes them. Nothing is shown for
// accounts that may not see the server (members).
export function ServerInfoCard({ stats, denied, to }: { stats: SystemStats | null; denied: boolean; to?: string }) {
  const [open, setOpen] = useState(false)
  if (denied) return null
  if (!stats) return <div className="skeleton info-card" style={{ minHeight: 150 }} />
  const s = summarize(stats)
  const tip = [
    s.cpu !== undefined
      ? plural(stats.cpu?.cores ?? 0, 'CPU: {pct}% of {n} core', 'CPU: {pct}% of {n} cores', { pct: stats.cpu?.percent.toFixed(1) ?? '' }) +
        (stats.app.cpuPercent !== undefined ? ', ' + t('Mediarium {pct}%', { pct: stats.app.cpuPercent.toFixed(1) }) : '')
      : '',
    s.mem ? t('Memory: {used} in use, {free} available, {total} in all', { used: exact(s.mem.usedBytes), free: exact(s.mem.totalBytes - s.mem.usedBytes), total: exact(s.mem.totalBytes) }) : '',
    s.store ? t('Storage: {used} in use, {free} free, {total} in all', { used: exact(s.store.usedBytes), free: exact(s.store.freeBytes), total: exact(s.store.totalBytes) }) : '',
  ]
    .filter(Boolean)
    .join(NL)
  return (
    <div className={`info-card${open ? ' open' : ''}`}>
      <div className="info-summary" onClick={() => setOpen((v) => !v)} title={open ? undefined : tip}>
        <h2 className="info-head">
          <button type="button" className="info-toggle" aria-expanded={open} aria-controls="server-details" title={open ? t('Hide the details') : t('Show more details')}>
            <span className="info-ico">
              <Icon name="monitor" size={18} />
            </span>
            <span className="info-name">{t('Server')}</span>
            <StatusDot level={s.level} />
            <Icon name={open ? 'chevron-up' : 'chevron-down'} size={16} />
          </button>
        </h2>
        <div className="info-rows">
          <div className="info-row">
            <span className="info-label">CPU</span>
            <span className="info-value">{s.cpu !== undefined ? (s.appCpu !== undefined ? t('{cpu}% (Mediarium {app}%)', { cpu: s.cpu, app: s.appCpu }) : `${s.cpu.toLocaleString(locale())}%`) : t('Not available')}</span>
            {s.cpu !== undefined && <MiniBar value={s.cpu} />}
          </div>
          <div className="info-row">
            <span className="info-label">{t('Memory')}</span>
            <span className="info-value">{s.mem ? t('{used} in use · {free} available', { used: formatBytes(s.mem.usedBytes), free: formatBytes(Math.max(0, s.mem.totalBytes - s.mem.usedBytes)) }) : t('Not available')}</span>
            {s.mem && <MiniBar value={s.mem.pct} />}
          </div>
          <div className="info-row">
            <span className="info-label">{t('Storage')}</span>
            <span className="info-value">{s.store ? t('{used} in use · {free} free', { used: formatBytes(s.store.usedBytes), free: formatBytes(s.store.freeBytes) }) : t('Not available')}</span>
            {s.store && <MiniBar value={s.store.pct} />}
          </div>
        </div>
      </div>
      {open && (
        <div className="info-details" id="server-details">
          <div className="info-block">
            <h4>{t('Running for')}</h4>
            {stats.load && <p>{tx('Load {value}', { value: <b>{stats.load.map((l) => l.toFixed(2)).join(' · ')}</b> })}</p>}
            {stats.uptimeSeconds ? <p>{tx('Server up {time}', { time: <b>{uptime(stats.uptimeSeconds)}</b> })}</p> : null}
            <p>{tx('Mediarium up {time}', { time: <b>{uptime(stats.app.uptimeSeconds)}</b> })}</p>
          </div>
          <div className="info-block">
            <h4>{t('Mediarium itself')}</h4>
            <p>
              {stats.app.limitBytes
                ? tx('Memory {used} of a {limit} limit', { used: <b>{formatBytes(stats.app.memoryBytes)}</b>, limit: formatBytes(stats.app.limitBytes) })
                : tx('Memory {used}', { used: <b>{formatBytes(stats.app.memoryBytes)}</b> })}
            </p>
            {stats.app.cpuPercent !== undefined && (
              <p>
                CPU <b>{stats.app.cpuPercent.toFixed(1)}%</b>
              </p>
            )}
            {stats.cpu && (
              <p>{tx('Processor {cores}', { cores: <b>{plural(stats.cpu.cores, '{n} core', '{n} cores')}</b> })}</p>
            )}
          </div>
          <div className="info-block info-disks">
            <h4>{t('Folders')}</h4>
            {stats.disks.length === 0 && <p>{t('No folder sizes available.')}</p>}
            {stats.disks.map((d) => {
              const p = pct(d.usedBytes, d.totalBytes)
              return (
                <div key={d.label + d.path} className="info-disk" title={`${d.path}${NL}${t('{free} free of {total}', { free: exact(d.freeBytes), total: exact(d.totalBytes) })}`}>
                  <span>{d.label}</span>
                  <small>{t('{free} free of {total}', { free: formatBytes(d.freeBytes), total: formatBytes(d.totalBytes) })}</small>
                  <MiniBar value={p} />
                </div>
              )
            })}
          </div>
          {to && (
            <Link to={to} className="info-more">
              {t('More details')} <Icon name="open" size={13} />
            </Link>
          )}
        </div>
      )}
    </div>
  )
}

// Settings, System → Server: CPU, memory and storage side by side, with the
// smaller facts (load, uptimes, what Mediarium itself uses, each folder's
// disk) in a row underneath. Updated live.
export default function ServerStatsCard() {
  const { stats, denied } = useServerStats(5000)
  if (denied) return null
  const s = stats ? summarize(stats) : null
  return (
    <fieldset className="group usenet span-all">
      <legend>
        <Icon name="monitor" size={14} /> {t('Server')} {s && <StatusDot level={s.level} />}
      </legend>
      {!stats || !s ? (
        <div className="skeleton" style={{ height: 120 }} />
      ) : (
        <>
          <div className="srv-bars">
            {stats.cpu ? (
              <Bar
                used={stats.cpu.percent}
                total={100}
                label="CPU"
                detail={plural(stats.cpu.cores, '{pct}% of {n} core', '{pct}% of {n} cores', { pct: s.cpu ?? 0 })}
                sub={s.appCpu !== undefined ? t('Mediarium uses {pct}%', { pct: s.appCpu }) : undefined}
              />
            ) : (
              <small>{t("CPU use isn't available on this system.")}</small>
            )}
            {s.mem ? <Bar used={s.mem.usedBytes} total={s.mem.totalBytes} label={t('Memory')} detail={t('{used} in use', { used: formatBytes(s.mem.usedBytes) })} sub={t('{free} available of {total}', { free: formatBytes(Math.max(0, s.mem.totalBytes - s.mem.usedBytes)), total: formatBytes(s.mem.totalBytes) })} /> : <small>{t("Memory figures aren't available on this system.")}</small>}
            {s.store ? (
              <Bar used={s.store.usedBytes} total={s.store.totalBytes} label={t('Storage')} detail={t('{used} in use', { used: formatBytes(s.store.usedBytes) })} sub={t('{free} free of {total}', { free: formatBytes(s.store.freeBytes), total: formatBytes(s.store.totalBytes) })} />
            ) : (
              <small>{t('No folder sizes available.')}</small>
            )}
          </div>
          <div className="srv-facts">
            {stats.load && <span>{tx('Load {value}', { value: <b>{stats.load.map((l) => l.toFixed(2)).join(' · ')}</b> })}</span>}
            {stats.uptimeSeconds ? <span>{tx('Server up {time}', { time: <b>{uptime(stats.uptimeSeconds)}</b> })}</span> : null}
            <span>{tx('Mediarium up {time}', { time: <b>{uptime(stats.app.uptimeSeconds)}</b> })}</span>
            <span>
              {stats.app.limitBytes
                ? tx('Mediarium memory {used} of a {limit} limit', { used: <b>{formatBytes(stats.app.memoryBytes)}</b>, limit: formatBytes(stats.app.limitBytes) })
                : tx('Mediarium memory {used}', { used: <b>{formatBytes(stats.app.memoryBytes)}</b> })}
            </span>
            {uniqueDisks(stats).map((d) => (
              <span key={d.labels.join()}>
                {d.labels.join(', ')} {tx('{free} free of {total}', { free: <b>{formatBytes(d.freeBytes)}</b>, total: formatBytes(d.totalBytes) })}
              </span>
            ))}
          </div>
        </>
      )}
    </fieldset>
  )
}
