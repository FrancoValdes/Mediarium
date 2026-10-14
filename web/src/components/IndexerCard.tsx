import { useState } from 'react'
import { api, type IndexerConfig } from '../api'
import { timeAgo } from '../format'
import Icon from './Icon'
import { friendlyIndexerError } from './IndexerForm'
import SeedRules from './SeedRules'
import { useToast } from './Toast'
import { FieldError, FormProblem, useValidation } from '../useValidation'
import { apiKey as apiKeyCheck, firstError, maxLength, required, url as urlCheck } from '../validate'
import { plural, t, st } from '../i18n'
import { untilText } from '../delayText'
import TipLabel from './TipLabel'

// One saved indexer: name, type and address on top, actions underneath, the
// last test result on its own line, and an inline form to edit it.
export default function IndexerCard({ indexer: i, onChanged, onToggle, onRemove }: { indexer: IndexerConfig; onChanged: () => void; onToggle: () => void; onRemove: () => void }) {
  const toast = useToast()
  const [testing, setTesting] = useState(false)
  const [result, setResult] = useState<{ ok: boolean; message: string } | null>(null)
  const [editing, setEditing] = useState(false)
  const [name, setName] = useState(i.name)
  const [baseUrl, setBaseUrl] = useState(i.baseUrl)
  const [apiKey, setApiKey] = useState('')
  const [saving, setSaving] = useState(false)
  const site = i.kind === 'cardigann'
  const v = useValidation({
    name: firstError(required(name, t('Give this indexer a name.')), maxLength(name, 80, t('The name'))),
    ...(site
      ? {}
      : {
          baseUrl: firstError(required(baseUrl, t('Add the address of the indexer, for example {example}.', { example: 'https://api.example.com' })), urlCheck(baseUrl, { example: 'https://api.example.com' })),
          apiKey: apiKeyCheck(apiKey),
        }),
  })

  async function test() {
    setTesting(true)
    setResult(null)
    try {
      const r = await api.testIndexer(i.id)
      setResult({ ok: r.ok, message: r.ok ? r.message : friendlyIndexerError(r.message) })
    } catch (e) {
      setResult({ ok: false, message: e instanceof Error ? e.message : String(e) })
    } finally {
      setTesting(false)
    }
  }

  // Searches skip an indexer that keeps failing; this asks it again at once.
  const pausedUntil = i.pausedUntil ? new Date(i.pausedUntil) : null
  const paused = !!pausedUntil // the server only sends it while the pause lasts
  async function tryAgain() {
    try {
      await api.resumeIndexer(i.id)
      toast.success(t('{name} will be asked again by the next search.', { name: i.name }))
      onChanged()
    } catch (e) {
      toast.error(e instanceof Error ? e.message : String(e))
    }
  }

  async function setPriority(priority: number) {
    try {
      await api.setIndexerPriority(i.id, priority)
      toast.success(priority === 1 ? t('{name} is preferred now.', { name: i.name }) : priority === 3 ? t('{name} is only used as a last resort now.', { name: i.name }) : t('{name} is back to normal.', { name: i.name }))
      onChanged()
    } catch (e) {
      toast.error(e instanceof Error ? e.message : String(e))
    }
  }

  async function save() {
    if (!v.attempt()) return
    setSaving(true)
    try {
      await api.updateIndexer(i.id, { name: name.trim(), ...(site ? {} : { baseUrl: baseUrl.trim(), apiKey: apiKey.trim() }) })
      toast.success(t('{name} saved.', { name: name.trim() || i.name }))
      setEditing(false)
      setApiKey('')
      v.reset()
      onChanged()
    } catch (e) {
      toast.error(e instanceof Error ? e.message : String(e))
    } finally {
      setSaving(false)
    }
  }

  return (
    <div className={`indexer-card${i.enabled ? '' : ' is-off'}`}>
      <div className="indexer-top">
        <span className={`tile-ico proto-${i.protocol}`}>
          <Icon name={i.protocol === 'torrent' ? 'magnet' : 'server'} size={18} />
        </span>
        <div className="indexer-info">
          <div className="indexer-title">
            <strong title={i.name}>{i.name}</strong>
            <span className="badge">{i.protocol === 'torrent' ? 'Torrent' : 'Usenet'}</span>
            {!i.enabled && <span className="badge">{t('Off')}</span>}
          </div>
          <small className="indexer-url" title={i.baseUrl}>
            {i.baseUrl}
          </small>
          {paused && pausedUntil && (
            <small className="indexer-health bad">
              <TipLabel text={t('After 3 failures in a row an indexer is left out of searches for 5 minutes, then 10, 20, 40 and so on, up to 6 hours. Try again now clears it.')} learnMore="indexers#when-an-indexer-keeps-failing">
                {t('Paused until {time} after {failures} in a row, because {why}.', {
                  time: untilText(i.pausedUntil ?? ''),
                  failures: plural(i.failures ?? 0, '{n} failure', '{n} failures'),
                  why: i.pausedWhy ?? t('it returned an error'),
                })}
              </TipLabel>
            </small>
          )}
          {!result && i.lastTestAt && (
            <small className={`indexer-health ${i.lastTestError ? 'bad' : 'ok'}`}>
              {i.lastTestError ? t('Last test failed {when}: {error}', { when: timeAgo(i.lastTestAt), error: friendlyIndexerError(i.lastTestError) }) : t('Last test passed {when}', { when: timeAgo(i.lastTestAt) })}
            </small>
          )}
        </div>
        <label className="indexer-priority">
          <TipLabel text={t('Quality always comes first. Between two equally good releases, the one from the more preferred indexer wins.')} learnMore="indexers#priority-health-and-test-all">{t('Priority')}</TipLabel>
          <select value={i.priority ?? 2} onChange={(e) => void setPriority(Number(e.target.value))}>
            <option value={1}>{t('Preferred')}</option>
            <option value={2}>{t('Normal')}</option>
            <option value={3}>{t('Last resort')}</option>
          </select>
        </label>
      </div>

      <div className="indexer-actions">
        <button className="btn-sm" onClick={() => void test()} disabled={testing}>
          {testing ? t('Testing…') : t('Test')}
        </button>
        {paused && (
          <button className="btn-sm" onClick={() => void tryAgain()}>
            {t('Try again now')}
          </button>
        )}
        <button className="btn-sm" onClick={() => setEditing((v) => !v)} aria-expanded={editing}>
          {editing ? t('Cancel') : t('Edit')}
        </button>
        <button className="btn-sm" onClick={onToggle}>
          {i.enabled ? t('Disable') : t('Enable')}
        </button>
        <button className="btn-sm btn-danger" onClick={onRemove}>
          {t('Remove')}
        </button>
      </div>

      {i.protocol === 'torrent' && <SeedRules indexerId={i.id} />}

      {result && (
        <div className={`indexer-result ${result.ok ? 'ok' : 'bad'}`}>
          <Icon name={result.ok ? 'check' : 'warning'} size={14} /> {st(result.message)}
        </div>
      )}

      {editing && (
        <div className="indexer-edit grid-form">
          <label>
            {t('Name')}
            <input value={name} onChange={(e) => setName(e.target.value)} {...v.bind('name', name, setName)} />
            <FieldError v={v} name="name" />
          </label>
          {site ? (
            <small style={{ color: 'var(--text-dim)' }}>{t('This site was added from the list. To change its sign-in details, remove it and add it again.')}</small>
          ) : (
            <>
              <label>
                {t('Address')}
                <input value={baseUrl} onChange={(e) => setBaseUrl(e.target.value)} {...v.bind('baseUrl', baseUrl, setBaseUrl)} />
                <FieldError v={v} name="baseUrl" />
              </label>
              <label>
                {t('API key')}
                <input value={apiKey} onChange={(e) => setApiKey(e.target.value)} placeholder={t('Leave empty to keep the saved key')} autoComplete="off" {...v.bind('apiKey', apiKey, setApiKey)} />
                <FieldError v={v} name="apiKey" />
              </label>
            </>
          )}
          <div>
            <button className="primary btn-sm" onClick={() => void save()} disabled={saving}>
              {saving ? t('Saving…') : t('Save changes')}
            </button>
            <FormProblem v={v} />
          </div>
        </div>
      )}
    </div>
  )
}
