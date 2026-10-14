import { useEffect, useRef, useState } from 'react'
import { api, type FoundMediaServer, type PlexAccountServer } from '../api'
import Icon from './Icon'
import { MEDIA_SERVER_BRAND, MediaServerMark } from './mediaServerBrand'
import { useToast } from './Toast'
import { FieldError, FormProblem, useValidation } from '../useValidation'
import { required } from '../validate'
import { t, tx } from '../i18n'
import { pollPin } from '../pinPoll'

// The networks to search, typed as 192.168.1 or 192.168.1.0/24, separated by spaces or commas.
function subnetsProblem(value: string): string | null {
  const list = value.split(/[\s,]+/).filter(Boolean)
  const bad = t('Enter your network like 192.168.1 or 192.168.1.0/24. Use spaces or commas between several.')
  if (list.length > 8) return t('That is a lot of networks. Search at most 8 at a time.')
  for (const item of list) {
    const m = /^(\d{1,3})\.(\d{1,3})\.(\d{1,3})(?:\.(\d{1,3}))?(?:\/(\d{1,2}))?$/.exec(item)
    if (!m) return bad
    if ([m[1], m[2], m[3], m[4]].some((o) => o !== undefined && Number(o) > 255)) return bad
    if (m[5] !== undefined && (Number(m[5]) < 24 || Number(m[5]) > 32)) return t('Networks can be a /24 or smaller, for example 192.168.1.0/24.')
  }
  return null
}

const errText = (e: unknown) => (e instanceof Error ? e.message : String(e))

// Search the home network for Plex, Jellyfin and Emby. Inside Docker the
// search cannot see your network's range by itself, so a range can be typed.
export function FindServers({ onPick, onManual }: { onPick: (s: FoundMediaServer) => void; onManual?: () => void }) {
  const [subnet, setSubnet] = useState('')
  const [busy, setBusy] = useState(false)
  const [found, setFound] = useState<FoundMediaServer[] | null>(null)
  const [note, setNote] = useState('')
  const [error, setError] = useState('')
  const v = useValidation({ subnet: subnetsProblem(subnet) })

  async function search() {
    if (!v.attempt()) return
    setBusy(true)
    setError('')
    setNote('')
    try {
      const nets = subnet
        .split(/[\s,]+/)
        .map((s) => s.trim())
        .filter(Boolean)
        .map((s) => (/^\d+\.\d+\.\d+$/.test(s) ? `${s}.0/24` : s))
      const r = await api.discoverMediaServers(nets)
      setFound(r.found)
      setNote(r.note ?? (r.found.length === 0 ? t('Nothing found on {networks}. If your server is on another network, type it above (for example 10.0.0), or add the server by its address.', { networks: r.scanned.join(', ') }) : ''))
    } catch (e) {
      setError(errText(e))
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="ms-find">
      <div className="ms-find-row">
        <button className="btn-with-icon" onClick={() => void search()} disabled={busy}>
          <Icon name="search" size={15} /> {busy ? t('Searching your network…') : t('Find my media servers')}
        </button>
        <input value={subnet} onChange={(e) => setSubnet(e.target.value)} placeholder={t('Your network, optional (e.g. 192.168.1)')} aria-label={t('Your home network')} {...v.bind('subnet', subnet, setSubnet)} />
      </div>
      <FieldError v={v} name="subnet" />
      {busy && <small className="ms-find-hint">{t('This takes up to about 10 seconds.')}</small>}
      {error && <p className="error-text" style={{ margin: 0 }}>{error}</p>}
      {found && found.length > 0 && (
        <ul className="ms-found">
          {found.map((f) => (
            <li key={`${f.kind}-${f.address}`}>
              <MediaServerMark kind={f.kind} size={26} />
              <span className="ms-found-name">
                <strong>{f.name || MEDIA_SERVER_BRAND[f.kind].label}</strong>
                <small>
                  {MEDIA_SERVER_BRAND[f.kind].label}
                  {f.version ? ` ${f.version}` : ''} · {f.address}
                </small>
              </span>
              {f.alreadyAdded ? (
                <span className="badge downloaded">{t('Added')}</span>
              ) : (
                <button className="btn-sm primary" onClick={() => onPick(f)}>
                  {t('Connect')}
                </button>
              )}
            </li>
          ))}
        </ul>
      )}
      {note && <small className="ms-find-hint">{note}</small>}
      {found && found.length === 0 && onManual && (
        <div>
          <button className="btn-sm" onClick={onManual}>
            {t('Add one by its address')}
          </button>
        </div>
      )}
    </div>
  )
}

// Sign in with Plex: plex.tv confirms who you are and hands Mediarium a key,
// then lists your Plex servers to pick from. No token to copy.
export function PlexSignIn({ onAdded }: { onAdded: () => void }) {
  const toast = useToast()
  const [pin, setPin] = useState<{ pinId: number; code: string; authUrl: string } | null>(null)
  const [servers, setServers] = useState<PlexAccountServer[] | null>(null)
  const [error, setError] = useState('')
  const [adding, setAdding] = useState('')
  const stopPoll = useRef<() => void>(() => undefined)

  useEffect(() => () => stopPoll.current(), [])

  const starting = useRef(false)
  async function start() {
    if (starting.current) return // a second press before the first answer would open a second window
    starting.current = true
    setError('')
    setServers(null)
    try {
      const p = await api.plexPin()
      setPin(p)
      window.open(p.authUrl, 'plex-sign-in', 'width=620,height=760,noopener,noreferrer')
      stopPoll.current()
      stopPoll.current = pollPin({
        check: async () => {
          const st = await api.plexPinStatus(p.pinId)
          return st.done || st.expired ? st : undefined
        },
        lifeSeconds: p.expiresIn,
        onDone: (st) => {
          if (st.done) setServers(st.servers)
          else {
            setPin(null)
            setError(st.error ?? t('The sign-in expired. Try again.'))
          }
        },
        onExpired: () => {
          setPin(null)
          setError(t('The sign-in expired. Try again.'))
        },
        onError: (e) => {
          setPin(null)
          setError(errText(e))
        },
      })
    } catch (e) {
      setError(errText(e))
    } finally {
      starting.current = false
    }
  }

  async function add(s: PlexAccountServer) {
    if (!pin) return
    setAdding(s.machineIdentifier)
    try {
      await api.plexPinAdd(pin.pinId, s.machineIdentifier)
      toast.success(t('{name} added. New downloads will show up in {server} on their own.', { name: s.name, server: 'Plex' }))
      onAdded()
    } catch (e) {
      toast.error(errText(e))
    } finally {
      setAdding('')
    }
  }

  if (servers) {
    return (
      <div className="ms-signin">
        <p className="ms-signin-ok">
          <Icon name="check" size={15} /> {t('Signed in to Plex. Pick the server that holds your library:')}
        </p>
        {servers.length === 0 && <p style={{ margin: 0 }}>{t('This Plex account has no servers.')}</p>}
        <ul className="ms-found">
          {servers.map((s) => (
            <li key={s.machineIdentifier}>
              <MediaServerMark kind="plex" size={26} />
              <span className="ms-found-name">
                <strong>{s.name}</strong>
                <small>
                  {s.owned ? t('Your server') : t('Shared with you')}
                  {s.version ? ` · ${s.version}` : ''} · {s.connections.find((c) => c.local)?.uri ?? s.connections[0]?.uri ?? ''}
                </small>
              </span>
              {s.alreadyAdded ? (
                <span className="badge downloaded">{t('Added')}</span>
              ) : (
                <button className="btn-sm primary" onClick={() => void add(s)} disabled={!!adding}>
                  {adding === s.machineIdentifier ? t('Adding…') : t('Add')}
                </button>
              )}
            </li>
          ))}
        </ul>
      </div>
    )
  }

  return (
    <div className="ms-signin">
      <button className="ms-plex-btn" onClick={() => void start()} disabled={!!pin}>
        <MediaServerMark kind="plex" size={22} /> {pin ? t('Waiting for you to approve in the Plex window…') : t('Sign in with Plex')}
      </button>
      {pin && (
        <small className="ms-find-hint">
          {tx('A plex.tv window opened. Sign in there and approve Mediarium. No window? {link} (code {code}).', {
            link: (
              <a href={pin.authUrl} target="_blank" rel="noreferrer">
                {t('Open it here')}
              </a>
            ),
            code: pin.code,
          })}
        </small>
      )}
      {!pin && <small className="ms-find-hint">{t("This is the easiest way. There's no token to find or copy, and Mediarium lists your Plex servers for you.")}</small>}
      {error && <p className="error-text" style={{ margin: 0 }}>{error}</p>}
    </div>
  )
}

// Jellyfin Quick Connect (approve a code in Jellyfin) or, for Jellyfin and
// Emby, signing in once with an administrator's username and password. The
// password is only used to get a key; it is never stored.
// `checkAddress` shows a problem with the address box (above this form) and
// returns false when the address is not usable yet.
export function JellyfinEmbySignIn({ kind, baseUrl, checkAddress, onAdded }: { kind: 'jellyfin' | 'emby'; baseUrl: string; checkAddress: () => boolean; onAdded: () => void }) {
  const toast = useToast()
  const [qc, setQc] = useState<{ id: string; code: string } | null>(null)
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const stopPoll = useRef<() => void>(() => undefined)
  const label = MEDIA_SERVER_BRAND[kind].label
  const v = useValidation({ username: required(username, t('Add the username of a {server} administrator.', { server: label })) })

  useEffect(() => () => stopPoll.current(), [])
  useEffect(() => {
    stopPoll.current()
    setQc(null)
    setError('')
  }, [baseUrl, kind])

  async function quickConnect() {
    if (!checkAddress()) return
    setError('')
    try {
      const r = await api.jellyfinQuickConnect(baseUrl.trim())
      setQc({ id: r.id, code: r.code })
      stopPoll.current()
      stopPoll.current = pollPin({
        check: async () => {
          const st = await api.jellyfinQuickConnectStatus(r.id)
          return st.done || st.expired ? st : undefined
        },
        lifeSeconds: r.expiresIn,
        onDone: (st) => {
          setQc(null)
          if (st.done) {
            toast.success(t('{name} added. New downloads will show up in {server} on their own.', { name: st.server.name, server: 'Jellyfin' }))
            onAdded()
          } else setError(st.error ?? t('The code expired. Try again.'))
        },
        onExpired: () => {
          setQc(null)
          setError(t('The code expired. Try again.'))
        },
        onError: (e) => {
          setQc(null)
          setError(errText(e))
        },
      })
    } catch (e) {
      setError(errText(e))
    }
  }

  async function login() {
    const addressOk = checkAddress()
    if (!v.attempt() || !addressOk) return
    setBusy(true)
    setError('')
    try {
      const s = await api.mediaServerLogin({ kind, baseUrl: baseUrl.trim(), username: username.trim(), password })
      setPassword('')
      toast.success(t('{name} added. New downloads will show up in {server} on their own.', { name: s.name, server: label }))
      onAdded()
    } catch (e) {
      setError(errText(e))
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="ms-signin">
      {kind === 'jellyfin' && (
        <>
          <div className="ms-way">
            <strong className="ms-way-title">{t('Option 1: Quick Connect')} <span className="ms-way-tag">{t('easiest, no password')}</span></strong>
            <ol className="ms-way-steps">
              <li>{t('Type the address of your Jellyfin server above.')}</li>
              <li>{tx('Press {button}. A 6-digit code shows up here.', { button: <strong>{t('Use Quick Connect')}</strong> })}</li>
              <li>{tx('Open Jellyfin in your browser, click your profile picture (top right), choose {qc}, type the code and confirm. Mediarium is added by itself a few seconds later.', { qc: <strong>Quick Connect</strong> })}</li>
            </ol>
            <button className="primary btn-with-icon" onClick={() => void quickConnect()} disabled={!!qc}>
              <Icon name="key" size={15} /> {qc ? t('Waiting for approval…') : t('Use Quick Connect')}
            </button>
            {qc && (
              <div className="ms-qc">
                <span>{tx('Now type this code in Jellyfin, under your profile picture → {qc}:', { qc: <strong>Quick Connect</strong> })}</span>
                <strong className="ms-qc-code">{qc.code}</strong>
              </div>
            )}
          </div>
          <div className="ms-or">{t('or, if Quick Connect is switched off on your server')}</div>
          <strong className="ms-way-title">{t('Option 2: Sign in with an administrator account')}</strong>
          <small className="ms-find-hint">{t('Use the username and password you log into Jellyfin with, as long as that account is an administrator. It is only used once, to get a key.')}</small>
        </>
      )}
      {kind === 'emby' && (
        <strong className="ms-way-title">{t('Sign in with an administrator account')} <span className="ms-way-tag">{t('use the login you use for Emby')}</span></strong>
      )}
      <div className="form-cols">
        <label>
          {t('{server} username', { server: label })}
          <input value={username} onChange={(e) => setUsername(e.target.value)} autoComplete="off" {...v.bind('username', username, setUsername)} />
          <FieldError v={v} name="username" />
        </label>
        <label>
          {t('Password')}
          <input type="password" value={password} onChange={(e) => setPassword(e.target.value)} autoComplete="new-password" />
        </label>
      </div>
      <div>
        <button className={kind === 'emby' ? 'primary' : ''} onClick={() => void login()} disabled={busy}>
          {busy ? t('Signing in…') : t('Sign in to {server}', { server: label })}
        </button>
        <small className="ms-find-hint" style={{ display: 'block', marginTop: 6 }}>
          {t("Used once to get a key for Mediarium. The password isn't stored.")}
        </small>
      </div>
      <FormProblem v={v} verb={t('sign in')} />
      {error && <p className="error-text" style={{ margin: 0 }}>{error}</p>}
    </div>
  )
}
