import { useCallback, useEffect, useState } from 'react'
import { api, type RequestNotices } from '../api'
import { t } from '../i18n'
import { deviceName, pushSupport, subscribeThisDevice } from '../push'
import Icon from './Icon'
import Switch from './Switch'
import { useToast } from './Toast'

// Your profile: whether you hear about your requests and reports, and on which
// phones and browsers. Shown once an administrator has switched the messages on.
export default function RequestNoticesCard() {
  const toast = useToast()
  const [st, setSt] = useState<RequestNotices | null>(null)
  const [busy, setBusy] = useState(false)
  const support = pushSupport()

  const load = useCallback(() => {
    api.requestNotices().then(setSt).catch(() => setSt(null))
  }, [])
  useEffect(load, [load])

  async function run(work: () => Promise<RequestNotices>, done?: string) {
    setBusy(true)
    try {
      setSt(await work())
      if (done) toast.success(done)
    } catch (e) {
      toast.error(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(false)
    }
  }

  if (!st || !st.serverOn) return null
  return (
    <fieldset className="group">
      <legend>
        <Icon name="mail" size={14} /> {t('Messages about your requests')}
      </legend>
      <p style={{ marginTop: 0, color: 'var(--text-dim)' }}>
        {t('You get a message when a request you made is approved, declined or ready to watch, and when a problem you reported is answered.')}
      </p>
      <Switch
        checked={st.enabled}
        disabled={busy}
        onChange={(v) => void run(() => api.putRequestNotices(v), v ? t('You will get these messages.') : t('You will not get these messages.'))}
        label={t('Send me these messages')}
        showState
      />
      {st.enabled && (
        <>
          <p className="field-hint" style={{ margin: '10px 0 6px' }}>
            {st.hasEmail && st.emailReady
              ? t('By email, to the address on your account.')
              : st.hasEmail
                ? t("Email isn't set up on this server, so nothing is sent by email.")
                : t('Add an email address to your account above to get them by email.')}
          </p>
          <ul className="file-list" style={{ marginBottom: 10 }}>
            {st.devices.map((d) => (
              <li key={d.id}>
                <span className="file-ico">
                  <Icon name="monitor" size={16} />
                </span>
                <div className="file-name">
                  <span>{d.name || t('A device')}</span>
                </div>
                <button className="btn-sm danger-ghost" disabled={busy} onClick={() => void run(() => api.removeNoticeDevice(d.id), t('Device removed.'))}>
                  {t('Remove')}
                </button>
              </li>
            ))}
          </ul>
          {support.ok ? (
            <button
              disabled={busy}
              onClick={() =>
                void run(async () => api.addNoticeDevice(await subscribeThisDevice(), deviceName()), t('This device will get these messages.'))
              }
            >
              {t('Also send them to this device')}
            </button>
          ) : (
            <p className="field-hint">{support.why}</p>
          )}
        </>
      )}
    </fieldset>
  )
}
