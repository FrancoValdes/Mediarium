import { useEffect, useState } from 'react'
import { useToast } from './Toast'
import { api, type ProxySettings } from '../api'
import { t } from '../i18n'
import TipLabel from './TipLabel'

type Form = Omit<ProxySettings, 'hasPassword' | 'port'> & { port: string; password: string }

const toForm = (p: ProxySettings): Form => ({ ...p, port: p.port ? String(p.port) : '', password: '' })

// A SOCKS5 or HTTP proxy for torrents, indexers and/or Usenet: the lighter
// alternative to the built-in VPN for people who already run one (a VPN
// container's SOCKS5 port, a seedbox).
export default function ProxyCard() {
  const toast = useToast()
  const [saved, setSaved] = useState<ProxySettings | null>(null)
  const [form, setForm] = useState<Form | null>(null)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState<'' | 'save' | 'test'>('')
  const [test, setTest] = useState<{ ok: boolean; message: string } | null>(null)

  useEffect(() => {
    api
      .getProxy()
      .then((p) => {
        setSaved(p)
        setForm(toForm(p))
      })
      .catch((e) => setError(e instanceof Error ? e.message : String(e)))
  }, [])

  if (!form) return error ? <section className="card"><h2>{t('Proxy')}</h2><p className="error-text">{error}</p></section> : null

  const on = form.type !== ''
  const body = () => ({
    type: form.type,
    host: form.host.trim(),
    port: Number(form.port) || 0,
    username: form.username.trim(),
    // An empty password field keeps the saved one.
    ...(form.password !== '' ? { password: form.password } : {}),
    torrents: form.torrents,
    indexers: form.indexers,
    usenet: form.usenet,
  })

  async function save() {
    setBusy('save')
    setTest(null)
    try {
      const p = await api.putProxy(body())
      setSaved(p)
      setForm(toForm(p))
      toast.success(p.type ? t('Saved: the proxy is in use for what you ticked.') : t('Saved: no proxy.'))
    } catch (e) {
      toast.error(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy('')
    }
  }

  async function runTest() {
    setBusy('test')
    setTest(null)
    try {
      setTest(await api.testProxy(body()))
    } catch (e) {
      setTest({ ok: false, message: e instanceof Error ? e.message : String(e) })
    } finally {
      setBusy('')
    }
  }

  const nothingTicked = on && !form.torrents && !form.indexers && !form.usenet

  return (
    <section className="card grid-form">
      <h2><TipLabel help="downloads#a-socks5-or-http-proxy-instead">{t('Proxy')}</TipLabel></h2>
      <p style={{ color: 'var(--text-dim)', margin: 0 }}>
        {t('Already run a proxy, such as the SOCKS5 port of a VPN container or a seedbox? Send torrents, indexer searches or Usenet through it. A connected VPN above still takes over for torrents.')}
      </p>
      <div className="seg" role="radiogroup" aria-label={t('Proxy type')}>
        {(
          [
            ['', t('No proxy')],
            ['socks5', 'SOCKS5'],
            ['http', 'HTTP'],
          ] as const
        ).map(([v, label]) => (
          <button key={v} role="radio" aria-checked={form.type === v} className={form.type === v ? 'active' : ''} onClick={() => setForm({ ...form, type: v })}>
            {label}
          </button>
        ))}
      </div>
      {on && (
        <>
          <div className="proxy-address">
            <label>
              {t('Host')}
              <input value={form.host} onChange={(e) => setForm({ ...form, host: e.target.value })} placeholder={t('192.168.1.20 or gluetun')} spellCheck={false} />
            </label>
            <label>
              {t('Port')}
              <input inputMode="numeric" value={form.port} onChange={(e) => setForm({ ...form, port: e.target.value })} placeholder={form.type === 'http' ? '8888' : '1080'} />
            </label>
          </div>
          <div className="proxy-address">
            <label>
              {t('User name (optional)')}
              <input value={form.username} onChange={(e) => setForm({ ...form, username: e.target.value })} autoComplete="off" spellCheck={false} />
            </label>
            <label>
              {t('Password (optional)')}
              <input
                type="password"
                value={form.password}
                onChange={(e) => setForm({ ...form, password: e.target.value })}
                autoComplete="new-password"
                placeholder={saved?.hasPassword && saved.username === form.username ? t('Saved. Leave empty to keep it.') : ''}
              />
            </label>
          </div>
          <fieldset className="profile-fieldset">
            <legend>{t('Send through the proxy')}</legend>
            <div className="proxy-uses">
              <label className="check-hint">
                <input type="checkbox" checked={form.torrents} onChange={(e) => setForm({ ...form, torrents: e.target.checked })} />
                <span>
                  {t('Torrents')}
                  <small>{t('Peers and trackers. Like the VPN: no direct connections, no DHT, and no incoming peers.')}</small>
                </span>
              </label>
              <label className="check-hint">
                <input type="checkbox" checked={form.indexers} onChange={(e) => setForm({ ...form, indexers: e.target.checked })} />
                <span>
                  {t('Indexers')}
                  <small>{t('Searches, and fetching NZB and torrent files.')}</small>
                </span>
              </label>
              <label className="check-hint">
                <input type="checkbox" checked={form.usenet} onChange={(e) => setForm({ ...form, usenet: e.target.checked })} />
                <span>
                  Usenet
                  <small>{t("Connections to your Usenet servers. Usually not needed: they're encrypted already.")}</small>
                </span>
              </label>
            </div>
          </fieldset>
          {nothingTicked && <p className="notice notice-warn" style={{ margin: 0 }}>{t('Nothing is ticked, so nothing goes through the proxy yet.')}</p>}
        </>
      )}
      {test && <p className={test.ok ? 'notice' : 'notice notice-warn'} style={{ margin: 0 }}>{test.message}</p>}
      <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap' }}>
        <button className="primary" onClick={() => void save()} disabled={busy !== ''}>
          {busy === 'save' ? t('Saving…') : t('Save')}
        </button>
        {on && (
          <button onClick={() => void runTest()} disabled={busy !== '' || !form.host.trim() || !form.port}>
            {busy === 'test' ? t('Testing…') : t('Test')}
          </button>
        )}
      </div>
    </section>
  )
}
