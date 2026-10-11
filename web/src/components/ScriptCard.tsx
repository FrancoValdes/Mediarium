import { useCallback, useEffect, useState } from 'react'
import { api, type ScriptRun, type ScriptsState } from '../api'
import { DOCS_URL } from '../docs'
import { timeAgo } from '../format'
import { t, tx } from '../i18n'
import Icon from './Icon'
import { useToast } from './Toast'
import TipLabel from './TipLabel'

// Settings > System: a script of your own to run after each import. Only
// files in the scripts folder inside the config folder can be picked, and
// only here (not with an API key). Off until one is picked.
export default function ScriptCard() {
  const toast = useToast()
  const [state, setState] = useState<ScriptsState | null>(null)
  const [script, setScript] = useState('')
  const [minutes, setMinutes] = useState(5)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')

  const load = useCallback(() => {
    api
      .getScripts()
      .then((s) => {
        setState(s)
        setScript(s.script)
        setMinutes(Math.max(1, Math.round(s.timeoutSec / 60)))
      })
      .catch((e) => setError(e instanceof Error ? e.message : String(e)))
  }, [])
  useEffect(load, [load])

  async function save() {
    setBusy(true)
    setError('')
    try {
      const s = await api.putScripts({ script, timeoutSec: Math.round(minutes * 60) })
      setState(s)
      toast.success(s.script ? t('{script} will run after each import.', { script: s.script }) : t('No script runs after imports.'))
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(false)
    }
  }

  async function tryIt() {
    setBusy(true)
    setError('')
    try {
      const run = await api.testScript()
      setState((cur) => (cur ? { ...cur, lastRun: run } : cur))
      if (run.problem) toast.error(`${run.script}: ${run.problem}`)
      else toast.success(t('{script} ran fine.', { script: run.script }))
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(false)
    }
  }

  const saved = state?.script ?? ''
  const changed = state !== null && (script !== saved || Math.round(minutes * 60) !== state.timeoutSec)
  return (
    <fieldset className="group">
      <legend>
        <Icon name="flask" size={14} /> {t('Run a script after each import')}
      </legend>
      <p style={{ color: 'var(--text-dim)', marginTop: 0 }}>
        {tx('For your own extra steps, such as telling another app or copying a file somewhere. Put the script in {folder}, make it executable, and pick it here. It is told what was imported in {vars} variables. Only scripts in that folder can be picked, and only from this page.', {
          folder: <code>{state?.folder ?? '/config/scripts'}</code>,
          vars: <code>MEDIARIUM_*</code>,
        })}{' '}
        <a href={`${DOCS_URL}/scripts.md`} target="_blank" rel="noreferrer">
          {t('What a script gets')}
        </a>
      </p>
      <div style={{ display: 'flex', gap: 10, flexWrap: 'wrap', alignItems: 'end' }}>
        <label style={{ display: 'grid', gap: 4, flex: '1 1 220px', maxWidth: 320 }}>
          <TipLabel text={t('Runs after every finished import and is told which title and file it was. Off until you pick one.')} learnMore="media-servers#running-a-script-after-each-import">{t('Script')}</TipLabel>
          <select value={script} onChange={(e) => setScript(e.target.value)} disabled={busy || state === null}>
            <option value="">{t('None (off)')}</option>
            {state?.scripts.map((name) => (
              <option key={name} value={name}>
                {name}
              </option>
            ))}
            {saved && !state?.scripts.includes(saved) && <option value={saved}>{t('{name} (missing)', { name: saved })}</option>}
          </select>
        </label>
        <label style={{ display: 'grid', gap: 4, width: 170 }}>
          <TipLabel text={t('The script is stopped if it runs longer than this, so a stuck script cannot hold up your imports.')}>{t('Time limit (minutes)')}</TipLabel>
          <input type="number" min={1} max={60} value={minutes} onChange={(e) => setMinutes(Math.min(60, Math.max(1, Number(e.target.value) || 1)))} disabled={busy || state === null} />
        </label>
        <button className="primary" onClick={() => void save()} disabled={busy || !changed}>
          {t('Save')}
        </button>
        <button onClick={() => void tryIt()} disabled={busy || !saved || changed} title={t('Runs it once with a made-up movie and MEDIARIUM_EVENT=test')}>
          {t('Try it')}
        </button>
        <button className="ghost btn-with-icon" onClick={load} disabled={busy} title={t('Look in the folder again')}>
          <Icon name="refresh" size={14} /> {t('Refresh list')}
        </button>
      </div>
      {state !== null && state.scripts.length === 0 && <p className="field-hint">{t('The folder has no executable scripts yet.')}</p>}
      {state?.lastRun && <LastRun run={state.lastRun} />}
      {error && <p className="error-text">{error}</p>}
    </fieldset>
  )
}

function LastRun({ run }: { run: ScriptRun }) {
  const when = timeAgo(run.at)
  const line =
    run.event === 'test'
      ? t('Last run: {script}, a try, {when}.', { script: run.script, when })
      : run.title
        ? t('Last run: {script}, after importing {title}, {when}.', { script: run.script, title: run.title, when })
        : t('Last run: {script}, after an import, {when}.', { script: run.script, when })
  return (
    <div className="field-hint" style={{ marginTop: 10 }}>
      <span style={{ color: run.problem ? 'var(--danger)' : undefined }}>
        {line} {run.problem || t('It took {n} s and finished fine.', { n: run.seconds })}
      </span>
      {run.output && <pre style={{ whiteSpace: 'pre-wrap', maxHeight: 200, overflow: 'auto', marginTop: 6 }}>{run.output}</pre>}
    </div>
  )
}
