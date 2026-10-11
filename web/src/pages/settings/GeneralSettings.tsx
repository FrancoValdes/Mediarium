import { useEffect, useState } from 'react'
import { api, isAdmin } from '../../api'
import { useAuth } from '../../AuthContext'
import Icon from '../../components/Icon'
import { UsageStatsCard } from '../../components/UsageStats'
import PublicAddress from '../../components/PublicAddress'
import ThemeToggle from '../../components/ThemeToggle'
import { PushCard, UpdatesCard } from '../../components/UpdateCards'
import { LANGUAGES, chooseLanguage, currentLanguage, locale, t, type Lang } from '../../i18n'

// The language the app itself is shown in (not the audio or subtitle language).
// It is kept per browser; choosing one reloads the page in place so every
// label comes back translated.
function LanguageCard() {
  return (
    <fieldset className="group">
      <legend>
        <Icon name="globe" size={14} /> {t('App language')}
      </legend>
      <div style={{ display: 'flex', flexWrap: 'wrap', alignItems: 'center', gap: 14 }}>
        <select aria-label={t('App language')} value={currentLanguage()} onChange={(e) => chooseLanguage(e.target.value as Lang)}>
          {LANGUAGES.map((l) => (
            <option key={l.code} value={l.code}>
              {l.name}
            </option>
          ))}
        </select>
        <span style={{ color: 'var(--text-dim)' }}>{t('The language the menus and buttons of Mediarium are shown in. It is kept in this browser. Audio and subtitle languages are set elsewhere.')}</span>
      </div>
    </fieldset>
  )
}

// Light or dark, kept per browser.
function LookCard() {
  return (
    <fieldset className="group">
      <legend>
        <Icon name="sun" size={14} /> {t('Light or dark')}
      </legend>
      <div style={{ display: 'flex', flexWrap: 'wrap', alignItems: 'center', gap: 14 }}>
        <ThemeToggle />
        <span style={{ color: 'var(--text-dim)' }}>{t('Switches Mediarium between its dark and light look. It is kept in this browser.')}</span>
      </div>
    </fieldset>
  )
}

// Dates and times are shown in the time zone of the browser. The server's own
// time zone (for quiet hours, overnight updates and the like) is the TZ set on the container.
function TimeZoneCard() {
  const zone = (() => {
    try {
      return Intl.DateTimeFormat().resolvedOptions().timeZone
    } catch {
      return ''
    }
  })()
  return (
    <fieldset className="group">
      <legend>
        <Icon name="clock" size={14} /> {t('Time zone')}
      </legend>
      <p style={{ margin: 0, color: 'var(--text-dim)' }}>
        {zone ? t('Dates and times are shown in the time zone of this browser: {zone}.', { zone }) : t('Dates and times are shown in the time zone of this browser.')}{' '}
        {t('Hours you set for quiet hours, overnight updates or download times follow the time zone of the server. You set it with TZ in your compose file.')}
      </p>
    </fieldset>
  )
}

// What you accepted when you set Mediarium up.
function LegalCard() {
  const [accepted, setAccepted] = useState<string | null>(null)
  useEffect(() => {
    api.getSettings().then((s) => setAccepted(s.legalAcknowledgedAt ?? '')).catch(() => undefined)
  }, [])
  return (
    <fieldset className="group torrent">
      <legend>
        <Icon name="shield" size={14} /> {t('Legal notice')}
      </legend>
      <p style={{ marginTop: 0 }}>
        {t("Mediarium is an automation and organising tool for content you have the legal right to obtain. It doesn't host, index or supply any content. You alone are responsible for what you download and for following the laws of your country and the terms of your providers.")}
      </p>
      {accepted !== null && (
        <p style={{ margin: 0, color: 'var(--text-dim)' }}>{accepted ? t('You accepted this on {date}.', { date: new Date(accepted).toLocaleDateString(locale()) }) : t('Not yet accepted.')}</p>
      )}
    </fieldset>
  )
}

// Settings > General: what is about the app as a whole. Everyone sees the
// language and the look; the address of the server, updates and the legal
// notice are for administrators.
export default function GeneralSettings() {
  const { user } = useAuth()
  const admin = isAdmin(user)
  return (
    <div className="settings-stack">
      <LanguageCard />
      <LookCard />
      <TimeZoneCard />
      {admin && <PublicAddress />}
      {admin && <UpdatesCard />}
      {admin && (
        <details className="how-details span-all">
          <summary>{t('Advanced: updates pushed through the API')}</summary>
          <PushCard />
        </details>
      )}
      {admin && <UsageStatsCard />}
      {admin && <LegalCard />}
    </div>
  )
}
