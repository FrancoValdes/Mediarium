import { useEffect, useRef, useState } from 'react'
import { api, type ListenBrainzStatus } from '../api'
import { useModules } from '../ModulesContext'
import { plural, t, tx } from '../i18n'
import Icon from './Icon'
import { useMusicPlayer } from './musicPlayerContext'
import Switch from './Switch'
import { useToast } from './Toast'

// Your profile: send what you play in the music player to your own ListenBrainz
// account, using your own user token.
export default function ListenBrainzCard() {
  const { on } = useModules()
  const player = useMusicPlayer()
  const toast = useToast()
  const [st, setSt] = useState<ListenBrainzStatus | null>(null)
  const [token, setToken] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const box = useRef<HTMLFieldSetElement>(null)
  const musicOn = on('music')

  useEffect(() => {
    if (!musicOn) return
    let live = true
    api
      .listenBrainz()
      .then((s) => live && setSt(s))
      .catch(() => undefined)
    return () => {
      live = false
    }
  }, [musicOn])

  useEffect(() => {
    if (st && window.location.hash === '#listenbrainz') box.current?.scrollIntoView({ block: 'start' })
  }, [st])

  async function run(work: () => Promise<void>) {
    setBusy(true)
    setError('')
    try {
      await work()
      player?.refreshScrobbling()
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(false)
    }
  }

  if (!musicOn || !st) return null
  return (
    <fieldset className="group" id="listenbrainz" ref={box}>
      <legend>
        <Icon name="headphones" size={16} /> {t('ListenBrainz')}
      </legend>
      {!st.connected ? (
        <form
          onSubmit={(e) => {
            e.preventDefault()
            void run(async () => {
              const r = await api.saveListenBrainz({ token })
              setSt(r)
              setToken('')
              toast.success(t('ListenBrainz connected.'))
            })
          }}
        >
          <p style={{ marginTop: 0 }}>
            {tx('Send what you play in the music player to your ListenBrainz account. Copy your user token from {link} and paste it here.', {
              link: (
                <a href="https://listenbrainz.org/settings/" target="_blank" rel="noreferrer">
                  listenbrainz.org/settings
                </a>
              ),
            })}
          </p>
          <div className="lb-row">
            <input type="password" value={token} onChange={(e) => setToken(e.target.value)} placeholder={t('Your ListenBrainz user token')} aria-label={t('Your ListenBrainz user token')} autoComplete="off" spellCheck={false} disabled={busy} />
            <button type="submit" className="primary btn-with-icon" disabled={busy || token.trim() === ''}>
              <Icon name="check" size={15} /> {t('Connect')}
            </button>
          </div>
          {error && <p className="error-text">{error}</p>}
        </form>
      ) : (
        <>
          <p style={{ marginTop: 0 }}>
            {tx('Connected as {name}.', { name: <strong>{st.user || 'ListenBrainz'}</strong> })}
          </p>
          <Switch
            checked={st.enabled}
            disabled={busy}
            onChange={(v) =>
              void run(async () => {
                setSt(await api.saveListenBrainz({ enabled: v }))
              })
            }
            label={t('Send my plays to ListenBrainz')}
            description={t('A track counts after you have heard half of it, or four minutes. What is playing now is shown there too.')}
          />
          <p style={{ color: 'var(--text-dim)', fontSize: '0.85rem', margin: '10px 0' }}>
            {plural(st.submitted, '{n} play sent so far.', '{n} plays sent so far.')} {st.pending > 0 && plural(st.pending, '{n} is waiting to be sent.', '{n} are waiting to be sent.')}
          </p>
          {st.lastError && <p className="error-text">{st.lastError}</p>}
          {error && <p className="error-text">{error}</p>}
          <div className="form-actions" style={{ justifyContent: 'flex-start' }}>
            <button
              className="btn-with-icon btn-danger"
              disabled={busy}
              onClick={() =>
                void run(async () => {
                  await api.forgetListenBrainz()
                  setSt({ connected: false, enabled: true, submitted: 0, pending: 0 })
                  toast.success(t('ListenBrainz disconnected.'))
                })
              }
            >
              <Icon name="x" size={15} /> {t('Disconnect')}
            </button>
          </div>
        </>
      )}
    </fieldset>
  )
}
