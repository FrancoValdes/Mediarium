import type { ReactNode } from 'react'
import { t } from '../i18n'
import TipLabel from './TipLabel'

// A labelled on/off switch. It has no Save button by design: use it for
// settings that apply immediately, and confirm with a toast when the change
// has been stored. showState adds a plain "On" / "Off" next to it, in a
// colour that matches, for the switches on the settings pages.
// `tip` adds an info button after the label (one plain sentence, see
// InfoTip); `learnMore` adds a "Learn more" link to a guide topic in it.
export default function Switch({
  checked,
  onChange,
  label,
  description,
  disabled,
  showState,
  tip,
  learnMore,
}: {
  checked: boolean
  onChange: (next: boolean) => void
  label: ReactNode
  description?: ReactNode
  disabled?: boolean
  showState?: boolean
  tip?: string
  learnMore?: string
}) {
  return (
    <label className={`switch-row${disabled ? ' disabled' : ''}${showState ? ' has-state' : ''}`}>
      <span className="switch">
        <input type="checkbox" role="switch" checked={checked} disabled={disabled} onChange={(e) => onChange(e.target.checked)} />
        <span className="switch-track" aria-hidden="true">
          <span className="switch-thumb" />
        </span>
      </span>
      {showState && (
        <span className={`switch-state ${checked ? 'on' : 'off'}`} aria-hidden="true">
          {checked ? t('On') : t('Off')}
        </span>
      )}
      <span className="switch-text">
        <span className="switch-label">
          {tip ? <TipLabel text={tip} learnMore={learnMore}>{label}</TipLabel> : label}
        </span>
        {description && <span className="switch-desc">{description}</span>}
      </span>
    </label>
  )
}
