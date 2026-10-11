import { useCallback, useEffect, useState, type ReactNode } from 'react'
import WizdomCard from '../../components/WizdomCard'
import { api, type Settings as SettingsData, type SubtitleAddon, type SubtitleSourceId, type SubtitleSourceStatus } from '../../api'
import ServiceKeyCard from '../../components/ServiceKeyCard'
import { OPENSUBTITLES_COPY, SUBDL_COPY } from '../../components/serviceCopy'
import Switch from '../../components/Switch'
import TestButton from '../../components/TestButton'
import { useToast } from '../../components/Toast'
import { SUBTITLE_LANGUAGES, languageName } from '../../languages'
import { SOURCE_FIELD, anySourceOn, isSearchedSource, noSourceReason, sourceById } from '../../subtitleSources'
import { PROFILE_CHOICES, choiceLabel, choicesFromSaved, profilesToSave, type ProfileChoice } from '../../subtitleProfiles'
import { useModules } from '../../ModulesContext'
import { useAutosaveSetting } from '../../useAutosave'
import { FieldError, FormProblem, useValidation } from '../../useValidation'
import { firstError, maxLength, required } from '../../validate'
import { t } from '../../i18n'
import '../../subtitleSources.css'
import TipLabel from '../../components/TipLabel'

function SubtitlesSection({ sources }: { sources: SubtitleSourceStatus[] | undefined }) {
  const [languages, setLanguages] = useState<string[]>(['en'])
  // The kind of subtitle each language wants; a language not listed wants a normal or SDH one.
  const [kinds, setKinds] = useState<Record<string, ProfileChoice>>({})
  const [auto, setAuto] = useState(false)
  const [upgrade, setUpgrade] = useState(true)
  const [countEmbedded, setCountEmbedded] = useState(true)
  const [status, setStatus] = useState('')
  const [sweeping, setSweeping] = useState(false)
  // Saving before the current choices have arrived would replace them with the defaults.
  const [loaded, setLoaded] = useState(false)
  const [saving, setSaving] = useState(false)
  const [loadError, setLoadError] = useState('')
  const toast = useToast()
  const v = useValidation({ languages: languages.length === 0 ? t('Pick at least one language to keep subtitles for.') : null })
  // Sources not loaded yet: don't claim anything is missing.
  const sourcesKnown = !!sources && sources.length > 0
  const anyOn = anySourceOn(sources)
  // Only OpenSubtitles can match a subtitle to the exact video file.
  const osOn = isSearchedSource(sources, 'opensubtitles')

  useEffect(() => {
    api
      .getSettings()
      .then((s) => {
        setLanguages(s.subtitleLanguages?.length ? s.subtitleLanguages : ['en'])
        setKinds(choicesFromSaved(s.subtitleProfiles))
        setAuto(s.subtitleAutoDownload ?? false)
        setUpgrade(s.subtitleUpgrade ?? true)
        setCountEmbedded(s.subtitleCountEmbedded ?? true)
        setLoaded(true)
      })
      .catch((e) => setLoadError(e instanceof Error ? e.message : String(e)))
  }, [])

  function toggleLanguage(code: string) {
    setLanguages((cur) => (cur.includes(code) ? cur.filter((c) => c !== code) : [...cur, code]))
  }

  async function save() {
    if (!v.attempt()) return
    setSaving(true)
    try {
      await api.putSettings({ subtitleLanguages: languages, subtitleProfiles: profilesToSave(kinds), subtitleAutoDownload: auto, subtitleUpgrade: upgrade, subtitleCountEmbedded: countEmbedded })
      toast.success(t('Saved.'))
    } catch (e) {
      toast.error(e instanceof Error ? e.message : String(e))
    } finally {
      setSaving(false)
    }
  }

  async function sweep() {
    setSweeping(true)
    setStatus(t('Searching for missing subtitles…'))
    try {
      setStatus((await api.subtitleSweep()).message)
    } catch (e) {
      setStatus(e instanceof Error ? e.message : String(e))
    } finally {
      setSweeping(false)
    }
  }

  return (
    <section className="card grid-form">
      <h2><TipLabel help="subtitles">{t('Languages')}</TipLabel></h2>
      <p style={{ color: 'var(--text-dim)' }}>{t('Subtitles are saved next to each movie and episode, in the languages you pick.')}</p>
      <fieldset style={{ border: '1px solid var(--border)', borderRadius: 'var(--radius)', padding: 12 }}>
        <legend>{t('Languages to keep')}</legend>
        <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(190px, 1fr))', gap: 6 }}>
          {SUBTITLE_LANGUAGES.map((l) => (
            <label key={l.code} style={{ display: 'flex', gap: 6, alignItems: 'center' }}>
              <input type="checkbox" checked={languages.includes(l.code)} onChange={() => toggleLanguage(l.code)} />
              {l.name}
            </label>
          ))}
        </div>
        <FieldError v={v} name="languages" />
      </fieldset>
      {languages.length > 0 && (
        <fieldset style={{ border: '1px solid var(--border)', borderRadius: 'var(--radius)', padding: 12 }}>
          <legend><TipLabel help="subtitles#normal-sdh-and-forced-subtitles">{t('Which kind of subtitle')}</TipLabel></legend>
          <p style={{ color: 'var(--text-dim)', margin: '0 0 10px' }}>
            {t('Normal is the plain dialogue. SDH also describes sounds, for people who are deaf or hard of hearing. A forced track holds only the lines spoken in another language, such as a few words of a foreign tongue in a film. Plex, Jellyfin and Emby pick up the .sdh and .forced names.')}
          </p>
          <div className="subtitle-kinds">
            {languages.map((code) => (
              <div key={code} className="subtitle-kind-row">
                <span>{languageName(code)}</span>
                <select aria-label={t('Kind of subtitle for {language}', { language: languageName(code) })} value={kinds[code] ?? 'either'} onChange={(e) => setKinds((cur) => ({ ...cur, [code]: e.target.value as ProfileChoice }))}>
                  {PROFILE_CHOICES.map((c) => (
                    <option key={c} value={c}>
                      {choiceLabel(c)}
                    </option>
                  ))}
                </select>
              </div>
            ))}
          </div>
        </fieldset>
      )}
      <label style={{ display: 'flex', gap: 6, alignItems: 'center' }}>
        <input type="checkbox" checked={auto} onChange={(e) => setAuto(e.target.checked)} />
        <TipLabel text={t('Fetches subtitles right after an import, then looks every six hours for missing ones, always within the OpenSubtitles daily limit.')} learnMore="subtitles#daily-limits-and-the-free-account">{t('Download after each import, and look again every few hours for anything missing')}</TipLabel>
      </label>
      <label style={{ display: 'flex', gap: 6, alignItems: 'center', marginLeft: 22, opacity: auto && osOn ? 1 : 0.55 }}>
        <input type="checkbox" checked={upgrade} disabled={!auto || !osOn} onChange={(e) => setUpgrade(e.target.checked)} />
        <TipLabel text={t('A subtitle made for your exact file is in sync. If one turns up within a month it replaces the first pick. Ones you chose or edited are left alone.')} learnMore="subtitles#subtitles-made-for-your-exact-file">{t('For a month after, swap a subtitle for one made for that exact video file if one turns up')}</TipLabel>
      </label>
      {sourcesKnown && !osOn && (
        <small style={{ color: 'var(--text-dim)', marginLeft: 22 }}>{t('Only OpenSubtitles can match the exact video file, so this needs OpenSubtitles switched on.')}</small>
      )}
      <label style={{ display: 'flex', gap: 6, alignItems: 'center' }}>
        <input type="checkbox" checked={countEmbedded} onChange={(e) => setCountEmbedded(e.target.checked)} />
        <TipLabel text={t('A language that is already inside the file counts as there, so nothing is downloaded for it. Only the track list is read, never the video.')} learnMore="subtitles#subtitles-inside-the-video">{t("Count subtitles already inside the video file (MKV and MP4), so they aren't downloaded again")}</TipLabel>
      </label>
      <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap' }}>
        <button className="primary" onClick={save} disabled={!loaded || saving}>
          {t('Save languages')}
        </button>
        <button disabled={(sourcesKnown && !anyOn) || sweeping} onClick={sweep}>
          {t('Search for missing subtitles now')}
        </button>
      </div>
      {sourcesKnown && !anyOn && <span style={{ color: 'var(--text-dim)' }}>{t('No subtitle source is switched on, so there is nothing to search. Switch one on under Subtitle sources.')}</span>}
      {status && <span style={{ color: 'var(--text-dim)' }}>{status}</span>}
      {loadError && <p className="error-text">{t('Could not load your current choices: {error}', { error: loadError })}</p>}
    </section>
  )
}

// Your OpenSubtitles.com account (optional), inside the OpenSubtitles source.
function OpenSubtitlesAccount({ s, reload }: { s: SettingsData; reload: () => void }) {
  const [username, setUsername] = useState(s.openSubtitlesAccountName ?? '')
  const [password, setPassword] = useState('')
  const [saving, setSaving] = useState(false)
  const toast = useToast()
  // Blank username and password together mean "no account". A password on its own is a mistake.
  const v = useValidation({
    username: firstError(password ? required(username, t('Add your OpenSubtitles username to go with this password.')) : null, maxLength(username, 100, t('The username'))),
  })

  async function saveAccount() {
    if (!v.attempt()) return
    setSaving(true)
    try {
      await api.putSettings({ openSubtitlesUsername: username.trim(), openSubtitlesPassword: password })
      setPassword('')
      toast.success(username.trim() ? t('OpenSubtitles account saved.') : t('OpenSubtitles account removed.'))
      reload()
    } catch (e) {
      toast.error(e instanceof Error ? e.message : String(e))
    } finally {
      setSaving(false)
    }
  }

  return (
    <div className="grid-form" style={{ marginTop: 18, paddingTop: 16, borderTop: '1px solid var(--border)' }}>
      <h3 style={{ margin: 0 }}><TipLabel help="subtitles#daily-limits-and-the-free-account">{t('Your OpenSubtitles.com account (optional)')}</TipLabel></h3>
      <p style={{ color: 'var(--text-dim)', margin: 0 }}>{t('A free account raises the daily limit from about 5 downloads to about 20.')}</p>
      <label>
        {t('Username')}
        <input value={username} onChange={(e) => setUsername(e.target.value)} autoComplete="off" {...v.bind('username', username, setUsername)} />
        <FieldError v={v} name="username" />
      </label>
      <label>
        {t('Password')}
        <input
          type="password"
          value={password}
          onChange={(e) => setPassword(e.target.value)}
          placeholder={s.hasOpenSubtitlesAccount ? t('leave blank to keep the saved password') : ''}
          autoComplete="new-password"
        />
      </label>
      <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap', alignItems: 'center' }}>
        <button className="primary" onClick={saveAccount} disabled={saving || (!username.trim() && !s.hasOpenSubtitlesAccount)}>
          {username.trim() ? t('Save account') : t('Remove account')}
        </button>
        <TestButton
          label={t('Test login')}
          disabled={!s.hasOpenSubtitlesApiKey}
          run={async () => {
            if (!username.trim() && password) return { ok: false, message: t('Add your OpenSubtitles username to go with this password.') }
            return api.testService({ service: 'opensubtitles', username: username.trim(), password })
          }}
        />
      </div>
      <FormProblem v={v} />
    </div>
  )
}

const SOURCE_NAME: Record<SubtitleSourceId, () => string> = {
  opensubtitles: () => 'OpenSubtitles',
  subdl: () => 'SubDL',
  addons: () => t('Use add-ons'),
}

const SOURCE_BLURB: Record<SubtitleSourceId, () => string> = {
  opensubtitles: () => t('The biggest subtitle site. Needs a free API key. About 5 downloads a day, or about 20 with a free account.'),
  subdl: () => t('A second subtitle site. Needs a free API key. About 2,000 searches and a few hundred downloads a day.'),
  addons: () => t('Subtitle sites Mediarium has no built-in support for, such as subs.ro, through Stremio add-ons. No key needed here. The limits depend on each site.'),
}

function sourceStatusText(s: SubtitleSourceStatus): string {
  switch (s.state) {
    case 'on':
      return t('On')
    case 'limit':
      return t('On, but the daily limit is reached today')
    case 'off':
      return t('Switched off')
    default:
      return s.id === 'addons' ? t('No add-on added yet') : t('Needs a key')
  }
}

// One row per source: the switch, what it is, where it stands, and (for the two
// sites) the place to enter its key. The keys live only here.
function SourceRow({
  status,
  busy,
  onToggle,
  children,
  keyLabel,
}: {
  status: SubtitleSourceStatus
  busy: boolean
  onToggle: (next: boolean) => void
  children?: ReactNode
  keyLabel?: string
}) {
  const searched = status.state === 'on' || status.state === 'limit'
  return (
    <li className={`source-row is-${status.state}`} id={`source-${status.id}`}>
      <Switch
        checked={searched}
        onChange={onToggle}
        disabled={!status.ready || busy}
        label={SOURCE_NAME[status.id]()}
        description={SOURCE_BLURB[status.id]()}
      />
      <p className={`source-state state-${status.state}`}>
        <span className="source-dot" aria-hidden="true" />
        {sourceStatusText(status)}
      </p>
      {status.id === 'addons' && (
        <small className="source-extra">
          {status.count
            ? t('{n} added. Add or remove them in the Subtitle add-ons card below.', { n: status.count })
            : t('Add one in the Subtitle add-ons card below, then switch this on.')}
        </small>
      )}
      {children && (
        <details className="source-key" open={status.state === 'needs_key'}>
          <summary>{keyLabel}</summary>
          <div>{children}</div>
        </details>
      )}
    </li>
  )
}

function SourcesCard({ s, reload }: { s: SettingsData | null; reload: () => void }) {
  const [busy, setBusy] = useState<SubtitleSourceId | ''>('')
  const toast = useToast()
  if (!s) return <section className="card"><p>{t('Loading…')}</p></section>
  const sources = s.subtitleSources ?? []
  const status = (id: SubtitleSourceId) => sourceById(sources, id)
  const reason = noSourceReason(sources)

  async function toggle(id: SubtitleSourceId, next: boolean) {
    setBusy(id)
    try {
      await api.putSettings({ [SOURCE_FIELD[id]]: next })
      const name = id === 'addons' ? t('Add-ons') : SOURCE_NAME[id]()
      toast.success(next ? t('Saved: {name} is on.', { name }) : t('Saved: {name} is off.', { name }))
      reload()
    } catch (e) {
      toast.error(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy('')
    }
  }

  const os = status('opensubtitles')
  const subdl = status('subdl')
  const addons = status('addons')
  return (
    <section className="card grid-form sources-card span-all">
      <h2><TipLabel help="subtitles#choosing-your-sources">{t('Subtitle sources')}</TipLabel></h2>
      <p style={{ color: 'var(--text-dim)', margin: 0 }}>
        {t('Choose where Mediarium looks for subtitles. Every source that is on is searched, and the best fit for your video file wins. A source that is off is never asked and its limits do not count.')}
      </p>
      {reason && (
        <div className="notice notice-warn" role="status" style={{ margin: 0 }}>
          <strong>{t('No subtitle source is switched on.')}</strong>{' '}
          {reason === 'all_off' ? t('Mediarium cannot look for subtitles until you switch one on.') : t('Add a key for OpenSubtitles or SubDL, or add an add-on, then switch it on.')}
        </div>
      )}
      <ul className="source-list">
        {os && (
          <SourceRow status={os} busy={busy === 'opensubtitles'} onToggle={(n) => void toggle('opensubtitles', n)} keyLabel={t('Key and account')}>
            <ServiceKeyCard
              embedded
              service="opensubtitles"
              {...OPENSUBTITLES_COPY}
              builtIn={!!s.openSubtitlesKeyBuiltIn}
              configured={!!s.hasOpenSubtitlesApiKey}
              onSaved={reload}
              allowOverride
              usingOwnKey={!!s.openSubtitlesUsingOwnKey}
            >
              <OpenSubtitlesAccount key={s.openSubtitlesAccountName ?? ''} s={s} reload={reload} />
            </ServiceKeyCard>
          </SourceRow>
        )}
        {subdl && (
          <SourceRow status={subdl} busy={busy === 'subdl'} onToggle={(n) => void toggle('subdl', n)} keyLabel={t('API key')}>
            <ServiceKeyCard embedded service="subdl" {...SUBDL_COPY} builtIn={false} configured={!!s.hasSubdlApiKey} onSaved={reload} />
          </SourceRow>
        )}
        {addons && <SourceRow status={addons} busy={busy === 'addons'} onToggle={(n) => void toggle('addons', n)} />}
      </ul>
    </section>
  )
}

function AddonsCard({ onChanged }: { onChanged: () => void }) {
  const [list, setList] = useState<SubtitleAddon[] | null>(null)
  const [address, setAddress] = useState('')
  const [adding, setAdding] = useState(false)
  const [loadError, setLoadError] = useState('')
  const toast = useToast()

  const load = useCallback(() => {
    api
      .subtitleAddons()
      .then(setList)
      .catch((e) => setLoadError(e instanceof Error ? e.message : String(e)))
  }, [])
  useEffect(load, [load])

  async function add() {
    setAdding(true)
    try {
      const added = await api.addSubtitleAddon(address.trim())
      setAddress('')
      toast.success(t('{name} added.', { name: added.name }))
      load()
      onChanged()
    } catch (e) {
      toast.error(e instanceof Error ? e.message : String(e))
    } finally {
      setAdding(false)
    }
  }

  async function remove(a: SubtitleAddon) {
    try {
      await api.removeSubtitleAddon(a.id)
      toast.success(t('{name} removed.', { name: a.name }))
      load()
      onChanged()
    } catch (e) {
      toast.error(e instanceof Error ? e.message : String(e))
    }
  }

  return (
    <section className="card grid-form addon-card">
      <h2><TipLabel help="subtitles#subtitle-add-ons">{t('Subtitle add-ons')}</TipLabel></h2>
      <p style={{ color: 'var(--text-dim)', margin: 0 }}>
        {t('Bring in a subtitle site Mediarium has no built-in support for, such as subs.ro or a Hebrew or Arabic site. Many of them offer an add-on (the Stremio kind): open its page, copy the add-on address, and paste it here.')}
      </p>
      {loadError && <p className="error-text">{loadError}</p>}
      {list && list.length > 0 && (
        <ul className="addon-list">
          {list.map((a) => (
            <li key={a.id}>
              <span className="addon-name">
                <strong>{a.name}</strong>
                <span className="addon-host">{a.host}</span>
              </span>
              <span className="addon-actions">
                <TestButton run={() => api.testSubtitleAddon(a.id)} />
                <button onClick={() => void remove(a)}>{t('Remove')}</button>
              </span>
            </li>
          ))}
        </ul>
      )}
      <label>
        <TipLabel text={t('The address of a Stremio-style subtitle add-on, usually ending in manifest.json. It can hold your key, so Mediarium keeps it encrypted.')}>{t('Add-on address')}</TipLabel>
        <input
          value={address}
          onChange={(e) => setAddress(e.target.value)}
          placeholder="https://example.com/manifest.json"
          autoComplete="off"
          spellCheck={false}
          onKeyDown={(e) => {
            if (e.key === 'Enter' && address.trim() && !adding) void add()
          }}
        />
      </label>
      <div>
        <button className="primary" onClick={() => void add()} disabled={adding || !address.trim()}>
          {adding ? t('Checking…') : t('Add add-on')}
        </button>
      </div>
      <p style={{ color: 'var(--text-dim)', margin: 0, fontSize: '0.85rem' }}>
        {t("An add-on's address can include your own key for that site, so Mediarium keeps it encrypted and only shows the host.")}
      </p>
    </section>
  )
}

// The master switch, at the top.
function SubtitlesSwitch({ enabled, onChange, disabled }: { enabled: boolean; onChange: (next: boolean) => void; disabled: boolean }) {
  return (
    <section className="card span-all">
      <Switch
        checked={enabled}
        onChange={onChange}
        disabled={disabled}
        label={t('Subtitles for my movies and shows')}
        description={t('Finds and saves subtitles next to each movie and episode.')}
        showState
      />
    </section>
  )
}

export default function SubtitleSettings() {
  const { refresh } = useModules()
  const sw = useAutosaveSetting<boolean>(
    (s) => s.subtitlesEnabled ?? false,
    (v) => ({ subtitlesEnabled: v }),
    (saved) => (saved ? t('Saved: subtitles are on.') : t('Saved: subtitles are off.')),
    false,
  )
  async function flip(next: boolean) {
    await sw.change(next)
    void refresh() // the rest of the app shows or hides its subtitle parts
  }

  // The sources card, the add-ons card and the buttons below all read from one copy of the settings.
  const [settings, setSettings] = useState<SettingsData | null>(null)
  const reload = useCallback(() => {
    api
      .getSettings()
      .then(setSettings)
      .catch(() => undefined)
  }, [])
  useEffect(reload, [reload])

  const details = (
    <div className="settings-stack span-all">
      <SourcesCard s={settings} reload={reload} />
      <AddonsCard onChanged={reload} />
      <WizdomCard />
      <SubtitlesSection sources={settings?.subtitleSources} />
    </div>
  )
  return (
    <div className="settings-stack">
      <SubtitlesSwitch enabled={sw.value} onChange={(v) => void flip(v)} disabled={!sw.loaded || sw.saving} />
      {sw.loaded &&
        (sw.value ? (
          details
        ) : (
          <details className="card files-card span-all">
            <summary>
              <h2 style={{ margin: 0, display: 'inline' }}>{t('Account, languages and automatic downloads')}</h2>
            </summary>
            <p style={{ color: 'var(--text-dim)' }}>{t('Turn subtitles on to change these.')}</p>
            <fieldset disabled className="plain-fieldset">
              {details}
            </fieldset>
          </details>
        ))}
    </div>
  )
}
