import { useState } from 'react'
import { api, type SeedRule } from '../api'
import { t } from '../i18n'
import { useToast } from './Toast'
import '../torrentcontrols.css'
import TipLabel from './TipLabel'

const text = (n: number | null | undefined) => (n === null || n === undefined ? '' : String(n))

// An empty box follows the global limit (null); a number, including 0, is this
// indexer's own. Anything else is no number.
function parse(s: string): number | null | undefined {
  const v = s.trim().replace(',', '.')
  if (v === '') return null
  const n = Number(v)
  return Number.isFinite(n) && n >= 0 ? n : undefined
}

// How long the built-in torrent client seeds what came from one torrent
// indexer. Private trackers set their own rules, so the global limit in
// Settings is not always right for them.
export default function SeedRules({ indexerId }: { indexerId: number }) {
  const toast = useToast()
  const [loaded, setLoaded] = useState(false)
  const [saved, setSaved] = useState<SeedRule>({ ratio: null, hours: null })
  const [ratio, setRatio] = useState('')
  const [hours, setHours] = useState('')
  const [busy, setBusy] = useState(false)

  function adopt(rule: SeedRule) {
    setSaved(rule)
    setRatio(text(rule.ratio))
    setHours(text(rule.hours))
  }

  function load() {
    if (loaded) return
    api
      .seedRules()
      .then((all) => {
        adopt(all[String(indexerId)] ?? { ratio: null, hours: null })
        setLoaded(true)
      })
      .catch(() => undefined)
  }

  const r = parse(ratio)
  const h = parse(hours)
  const bad = r === undefined || h === undefined
  const changed = !bad && (r !== saved.ratio || h !== saved.hours)

  async function save() {
    if (bad) return
    setBusy(true)
    try {
      adopt(await api.setSeedRule(indexerId, { ratio: r, hours: h }))
      toast.success(r === null && h === null ? t('This indexer follows the global seeding limit.') : t('Seeding rule saved.'))
    } catch (e) {
      toast.error(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(false)
    }
  }

  return (
    <details className="qdetails" onToggle={(e) => e.currentTarget.open && load()}>
      <summary><TipLabel text={t('How long the built-in torrent client keeps sharing what it got from this tracker. Private trackers often ask for a minimum ratio or time.')} learnMore="downloads#seeding-rules-for-one-indexer">{t('Seeding rules')}</TipLabel></summary>
      <div className="seed-rule">
        <label>
          <small>{t('Seed until ratio')}</small>
          <input inputMode="decimal" value={ratio} placeholder={t('Global')} onChange={(e) => setRatio(e.target.value)} aria-invalid={r === undefined} />
        </label>
        <label>
          <small>{t('or for hours')}</small>
          <input inputMode="decimal" value={hours} placeholder={t('Global')} onChange={(e) => setHours(e.target.value)} aria-invalid={h === undefined} />
        </label>
        <button className="btn-sm" disabled={busy || !loaded || !changed} onClick={() => void save()}>
          {t('Save')}
        </button>
      </div>
      <small className="seed-rule">{t('Torrents from this indexer stop seeding at the first one reached. Leave a box empty to use the limit in Settings, or type 0 for no limit of that kind. It applies to torrents that start downloading from now on.')}</small>
    </details>
  )
}
