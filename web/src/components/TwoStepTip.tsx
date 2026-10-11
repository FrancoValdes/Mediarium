import { useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { api } from '../api'
import { t } from '../i18n'
import Icon from './Icon'

const KEY = 'mediarium-two-step-tip-hidden'

function hidden(): boolean {
  try {
    return localStorage.getItem(KEY) === '1'
  } catch {
    return false
  }
}

// On the dashboard, once Mediarium can be reached from outside the home: a
// small suggestion to switch on two-step sign-in. Never forced, and gone for
// good (in this browser) once closed or once it is on.
export default function TwoStepTip() {
  const [reason, setReason] = useState('')
  const [closed, setClosed] = useState(hidden)

  useEffect(() => {
    if (closed) return
    api
      .twoStep()
      .then((s) => setReason(s.enabled ? '' : (s.suggested ?? '')))
      .catch(() => undefined)
  }, [closed])

  if (closed || !reason) return null
  return (
    <section className="card two-step-tip" aria-label={t('Protect your account')}>
      <Icon name="shield" size={20} />
      <p>
        <strong>{t('Protect your account with two-step sign-in.')}</strong> {reason} {t('A code from your phone keeps your account safe even if the password leaks.')}
      </p>
      <Link className="btn primary btn-sm" to="/settings/security">
        {t('Set it up')}
      </Link>
      <button
        type="button"
        className="icon-btn"
        aria-label={t('Hide this suggestion')}
        title={t('Hide this')}
        onClick={() => {
          try {
            localStorage.setItem(KEY, '1')
          } catch {
            // best effort
          }
          setClosed(true)
        }}
      >
        <Icon name="x" size={16} />
      </button>
    </section>
  )
}
