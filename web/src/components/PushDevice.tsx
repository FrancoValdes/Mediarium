import { useState } from 'react'
import { deviceName, pushSupport, subscribeThisDevice } from '../push'
import { t } from '../i18n'

// The "This device" part of the Push notification form: asks the browser for
// permission, subscribes it with the server's key and hands the subscription
// to the form. Push needs a secure page (https, or localhost) and, on an
// iPhone or iPad, Mediarium added to the Home Screen.

export default function PushDevice({ value, stored, onChange }: { value: string; stored: boolean; onChange: (subscription: string, name: string) => void }) {
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const support = pushSupport()

  async function allow() {
    setBusy(true)
    setError('')
    try {
      onChange(await subscribeThisDevice(), deviceName())
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(false)
    }
  }

  if (!support.ok) return <p className="notice notice-warn" style={{ margin: 0 }}>{support.why}</p>
  return (
    <div className="push-device">
      {value ? (
        <p className="push-ready">{t('This device is ready. Send a test, then add it.')}</p>
      ) : stored ? (
        <p className="push-ready">{t('A device is saved. To move it to this device, press the button.')}</p>
      ) : null}
      <button type="button" className={value ? '' : 'primary'} disabled={busy} onClick={() => void allow()}>
        {busy ? t('Asking the browser…') : value || stored ? t('Use this device') : t('Allow notifications on this device')}
      </button>
      {error && <p className="error-text" style={{ margin: 0 }}>{error}</p>}
    </div>
  )
}
