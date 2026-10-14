import { useEffect, useRef, useState } from 'react'
import { Link } from 'react-router-dom'
import { api, isAdmin, type PlexWatchlistStatus } from '../api'
import { useAuth } from '../AuthContext'
import { timeAgo } from '../format'
import { t, tx } from '../i18n'
import { pollPin } from '../pinPoll'
import Icon from './Icon'
import { MediaServerMark } from './mediaServerBrand'
import Switch from './Switch'
import { useToast } from './Toast'

// Your profile: connect your Plex account so what you put on your Plex
// watchlist is added (or asked for) by itself.
export default function PlexWatchlistCard() {
  const { user } = useAuth()
  const admin = isAdmin(user)
  const asks = !admin && user?.permissions?.addDirect === false
  const toast = useToast()
  const [st, setSt] = useState<PlexWatchlistStatus | null>(null)
  const [waiting, setWaiting] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const stopPoll = useRef<() => void>(() => undefined)

  useEffect(() => {
    api.plexWatchlist().then(setSt).catch(() => setSt({ connected: false, autoAdd: true, added: 0, requested: 0 }))
    return () => stopPoll.current()
  }, [])

  async function connect() {
    setError('')
    try {
      const p = await api.plexWatchlistPin()
      setWaiting(true)
      window.open(p.authUrl, 'plex-sign-in', 'width=620,height=760,noopener,noreferrer')
      stopPoll.current()
      stopPoll.current = pollPin({
        check: async () => {
          const r = await api.plexWatchlistPinStatus(p.pinId)
          return r.done && r.status ? r.status : undefined
        },
        lifeSeconds: p.expiresIn,
        onDone: (status) => {
          setWaiting(false)
          setSt(status)
          toast.success(t('Plex watchlist connected.'))
        },
        onExpired: () => {
          setWaiting(false)
          setError(t('The sign-in expired. Try again.'))
        },
        onError: (e) => {
          setWaiting(false)
          setError(e instanceof Error ? e.message : String(e))
        },
      })
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    }
  }

  async function run(work: () => Promise<void>) {
    setBusy(true)
    setError('')
    try {
      await work()
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(false)
    }
  }

  if (!st) return null
  return (
    <fieldset className="group">
      <legend>
        <MediaServerMark kind="plex" size={28} /> {t('Plex watchlist')}
      </legend>
      {!st.connected ? (
        <>
          <p style={{ marginTop: 0 }}>
            {asks
              ? t('Connect your Plex account and anything you add to your Plex watchlist is sent to an administrator as a request, from any Plex app.')
              : t('Connect your Plex account and anything you add to your Plex watchlist is added here and searched for, from any Plex app.')}
          </p>
          <div>
            <button className="ms-plex-btn" onClick={() => void connect()} disabled={waiting}>
              <MediaServerMark kind="plex" size={20} /> {waiting ? t('Waiting for you to approve in the Plex window…') : t('Connect my Plex watchlist')}
            </button>
          </div>
        </>
      ) : (
        <>
          <p style={{ marginTop: 0 }}>
            {tx('Connected as {name}. What was already on the list is on {discover}, ready to add.', {
              name: <strong>{st.name || 'Plex'}</strong>,
              discover: <Link to="/discover">{t('Discover')}</Link>,
            })}
          </p>
          <Switch
            checked={st.autoAdd}
            disabled={busy}
            onChange={(v) => void run(async () => setSt(await api.setPlexWatchlist(v)))}
            label={asks ? t('Ask for what I add to my watchlist') : t('Add what I add to my watchlist')}
            description={t('Checked every 20 minutes. Titles already in the library are left alone.')}
          />
          <p style={{ color: 'var(--text-dim)', fontSize: '0.85rem', margin: '10px 0' }}>
            {st.checkedAt ? t('Last checked {when}.', { when: timeAgo(st.checkedAt) }) : t('Not checked yet.')}{' '}
            {st.added > 0 && t('{n} added so far.', { n: st.added })} {st.requested > 0 && t('{n} asked for so far.', { n: st.requested })}
          </p>
          {st.lastError && <p className="error-text">{st.lastError}</p>}
          <div className="form-actions" style={{ justifyContent: 'flex-start' }}>
            <button
              className="btn-with-icon"
              disabled={busy}
              onClick={() =>
                void run(async () => {
                  const r = await api.checkPlexWatchlist()
                  setSt(r.status)
                  toast.success(r.added + r.requested > 0 ? t('{added} added, {requested} asked for.', { added: r.added, requested: r.requested }) : t('Nothing new on your watchlist.'))
                })
              }
            >
              <Icon name="refresh" size={15} /> {t('Check now')}
            </button>
            <button
              disabled={busy}
              onClick={() =>
                void run(async () => {
                  await api.disconnectPlexWatchlist()
                  setSt({ connected: false, autoAdd: true, added: 0, requested: 0 })
                })
              }
            >
              {t('Disconnect')}
            </button>
          </div>
        </>
      )}
      {error && <p className="error-text">{error}</p>}
    </fieldset>
  )
}
