import { useEffect, useRef, useState, type ReactNode } from 'react'
import { api } from '../api'
import { t, tx } from '../i18n'
import { firstError, numberRange } from '../validate'
import { useToast } from './Toast'
import InfoTip from './InfoTip'

type Key = 'huntIntervalHours' | 'releaseCheckMinutes'

// One "every … units" number box. It saves by itself a moment after you stop
// typing, or when you leave the box, but only when the number is allowed.
// sentence puts the box inside its sentence, so each language can word it.
function IntervalField({ id, sentence, field, fallback, min, max, saved, tip }: { id: string; sentence: (box: ReactNode) => ReactNode; field: Key; fallback: number; min: number; max: number; saved: string; tip: string }) {
  const toast = useToast()
  const [stored, setStored] = useState<number | null>(null)
  const [text, setText] = useState(String(fallback))
  const [busy, setBusy] = useState(false)
  const timer = useRef<number | undefined>(undefined)

  useEffect(() => {
    api
      .getSettings()
      .then((s) => {
        const n = s[field] ?? fallback
        setStored(n)
        setText(String(n))
      })
      .catch(() => undefined)
    return () => window.clearTimeout(timer.current)
  }, [field, fallback])

  const error = firstError(text.trim() === '' && t('Enter a number from {min} to {max}.', { min, max }), numberRange(text, t('The number'), { min, max }))

  async function save(value: string) {
    window.clearTimeout(timer.current)
    if (stored === null || error || Number(value) === stored) return
    setBusy(true)
    try {
      const s = await api.putSettings({ [field]: Number(value) })
      const n = s[field] ?? Number(value)
      setStored(n)
      setText(String(n))
      toast.success(saved.replace('{n}', String(n)))
    } catch (e) {
      toast.error(t('Not saved: {error}', { error: e instanceof Error ? e.message : String(e) }))
      setText(String(stored))
    } finally {
      setBusy(false)
    }
  }

  function edit(value: string) {
    setText(value)
    window.clearTimeout(timer.current)
    timer.current = window.setTimeout(() => void save(value), 1200)
  }

  return (
    <div>
      <div className="tip-beside">
      <label htmlFor={id} style={{ display: 'flex', flexWrap: 'wrap', alignItems: 'center', gap: 8 }}>
        {sentence(
          <input
            id={id}
            inputMode="numeric"
            value={text}
            disabled={stored === null || busy}
            onChange={(e) => edit(e.target.value)}
            onBlur={(e) => void save(e.target.value)}
            onKeyDown={(e) => e.key === 'Enter' && void save(text)}
            aria-invalid={error ? true : undefined}
            style={{ width: 80 }}
          />,
        )}
      </label>
      <InfoTip text={tip} />
      </div>
      {error && <small style={{ color: 'var(--danger)' }}>{error}</small>}
    </div>
  )
}

// How often the two automatic loops run. Both take effect without a restart.
export default function AutomationIntervals() {
  return (
    <div style={{ display: 'flex', flexWrap: 'wrap', gap: '12px 32px', marginTop: 16 }}>
      <IntervalField id="hunt-hours" sentence={(box) => tx('Look for missing items every {box} hours', { box })} field="huntIntervalHours" fallback={6} min={1} max={168} saved={t('Saved: Mediarium looks for missing items every {n} hours.')} tip={t('Searches your indexers for every title that is missing or could be upgraded. A shorter time finds things sooner but sends more requests.')} />
      <IntervalField id="release-minutes" sentence={(box) => tx('Check for new releases every {box} minutes', { box })} field="releaseCheckMinutes" fallback={15} min={5} max={1440} saved={t('Saved: Mediarium checks for new releases every {n} minutes.')} tip={t('Reads the newest releases on each indexer and grabs any that match something you are waiting for.')} />
    </div>
  )
}
