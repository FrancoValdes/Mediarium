import { useCallback, useEffect, useMemo, useState } from 'react'
import Dropdown from './Dropdown'
import { useToast } from './Toast'
import { useConfirm } from './ConfirmProvider'
import { api, type CustomFormat, type FormatTestResult, type QualityProfile, type TrashFormat, type TrashProfile } from '../api'
import { plural, t, tx, st } from '../i18n'
import TipLabel from './TipLabel'

type Panel = 'list' | 'trash' | 'paste' | 'test'
type App = 'radarr' | 'sonarr'

const APP_LABEL: Record<App, string> = { radarr: t('Movies'), sonarr: t('TV') }
const errText = (e: unknown) => (e instanceof Error ? e.message : String(e))

// Custom formats: named conditions on a release name (HDR, a codec, a
// release group, a streaming service…) that quality profiles give a score.
// They come from the TRaSH Guides, from Radarr/Sonarr JSON, and are tested
// here against any release name.
export default function CustomFormatsSection({ rev = 0, onChanged }: { rev?: number; onChanged?: () => void }) {
  const toast = useToast()
  const confirm = useConfirm()
  const [panel, setPanel] = useState<Panel>('list')
  const [formats, setFormats] = useState<CustomFormat[]>([])
  const [profiles, setProfiles] = useState<QualityProfile[]>([])
  const [error, setError] = useState('')

  const load = useCallback(() => {
    api
      .listCustomFormats()
      .then((r) => setFormats(r.formats))
      .catch((e) => setError(errText(e)))
    api
      .listProfiles()
      .then((r) => setProfiles(r.profiles))
      .catch(() => setProfiles([]))
  }, [])
  useEffect(load, [load, rev])

  const changed = () => {
    load()
    onChanged?.()
  }

  async function remove(f: CustomFormat) {
    const body = f.usedBy.length ? <p>{t('Its score is taken out of {profiles}.', { profiles: f.usedBy.join(', ') })}</p> : <p>{t('No profile uses it.')}</p>
    if (!(await confirm({ title: t('Delete "{name}"?', { name: f.name }), body, confirmLabel: t('Delete format'), danger: true }))) return
    try {
      await api.deleteCustomFormat(f.id)
      toast.success(t('Deleted {name}.', { name: f.name }))
      changed()
    } catch (e) {
      toast.error(errText(e))
    }
  }

  return (
    <section className="card wide">
      <h2><TipLabel help="quality#custom-formats">{t('Custom formats')}</TipLabel></h2>
      <p style={{ color: 'var(--text-dim)' }}>
        {t('A custom format spots something in a release name, like HDR, a codec or a release group. Each quality profile gives the formats it cares about a score: releases scoring higher win, and a negative score keeps a release out. Most people start from the TRaSH Guides.')}
      </p>
      {error && <p className="error-text">{error}</p>}
      <div className="seg" role="tablist" aria-label={t('Custom formats')} style={{ marginBottom: 14 }}>
        <button role="tab" aria-selected={panel === 'list'} className={panel === 'list' ? 'active' : ''} onClick={() => setPanel('list')}>
          {t('Your formats')} <small>({formats.length})</small>
        </button>
        <button role="tab" aria-selected={panel === 'trash'} className={panel === 'trash' ? 'active' : ''} onClick={() => setPanel('trash')}>
          {t('From the TRaSH Guides')}
        </button>
        <button role="tab" aria-selected={panel === 'paste'} className={panel === 'paste' ? 'active' : ''} onClick={() => setPanel('paste')}>
          {t('Paste JSON')}
        </button>
        <button role="tab" aria-selected={panel === 'test'} className={panel === 'test' ? 'active' : ''} onClick={() => setPanel('test')}>
          {t('Test a release')}
        </button>
      </div>
      {panel === 'list' && <FormatList formats={formats} onRemove={remove} onRenamed={changed} onStart={() => setPanel('trash')} />}
      {panel === 'trash' && <TrashPanel profiles={profiles} onImported={changed} />}
      {panel === 'paste' && (
        <PastePanel
          onImported={() => {
            changed()
            setPanel('list')
          }}
        />
      )}
      {panel === 'test' && <TestPanel profiles={profiles} hasFormats={formats.length > 0} />}
    </section>
  )
}

function FormatList({ formats, onRemove, onRenamed, onStart }: { formats: CustomFormat[]; onRemove: (f: CustomFormat) => void; onRenamed: () => void; onStart: () => void }) {
  const toast = useToast()
  const [editing, setEditing] = useState<{ id: number; name: string } | null>(null)

  async function rename() {
    if (!editing) return
    try {
      await api.renameCustomFormat(editing.id, editing.name)
      setEditing(null)
      onRenamed()
    } catch (e) {
      toast.error(errText(e))
    }
  }

  if (formats.length === 0) {
    return (
      <p>
        {t('No custom formats yet.')} <button onClick={onStart}>{t('Browse the TRaSH Guides…')}</button>
      </p>
    )
  }
  return (
    <div className="table-scroll">
      <table className="formats-table">
        <thead>
          <tr>
            <th>{t('Name')}</th>
            <th>{t('For')}</th>
            <th>{t('Conditions')}</th>
            <th style={{ whiteSpace: 'nowrap' }}>{t('Scored by')}</th>
            <th>
              <span className="sr-only">{t('Actions')}</span>
            </th>
          </tr>
        </thead>
        <tbody>
          {formats.map((f) => (
            <tr key={f.id}>
              <td>
                {editing?.id === f.id ? (
                  <form
                    style={{ display: 'flex', gap: 6 }}
                    onSubmit={(e) => {
                      e.preventDefault()
                      void rename()
                    }}
                  >
                    <input autoFocus aria-label={t('New name')} value={editing.name} maxLength={100} onChange={(e) => setEditing({ ...editing, name: e.target.value })} />
                    <button className="primary" type="submit">
                      {t('Save')}
                    </button>
                    <button type="button" onClick={() => setEditing(null)}>
                      {t('Cancel')}
                    </button>
                  </form>
                ) : (
                  <>
                    {f.name} {f.trashId && <span className="badge">TRaSH</span>}
                  </>
                )}
                {f.problems?.map((p) => (
                  <div key={p} className="format-problem">
                    {p}
                  </div>
                ))}
              </td>
              <td>{APP_LABEL[f.app]}</td>
              <td
                className="format-conditions"
                title={f.conditions
                  .map((c) => {
                    const name = c.negate ? t('not {name}', { name: c.name }) : c.name
                    return c.required ? t('{name} (required)', { name }) : name
                  })
                  .join('\n')}
              >
                {plural(f.conditions.length, '{n} condition', '{n} conditions')}
              </td>
              <td style={{ color: 'var(--text-dim)', fontSize: '0.85rem' }}>{f.usedBy.length ? f.usedBy.join(', ') : t('no profile yet')}</td>
              <td style={{ whiteSpace: 'nowrap' }}>
                <button onClick={() => setEditing({ id: f.id, name: f.name })}>{t('Rename')}</button> <button onClick={() => onRemove(f)}>{t('Delete')}</button>
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}

function AppSwitch({ app, onChange }: { app: App; onChange: (a: App) => void }) {
  return (
    <div className="seg" role="radiogroup" aria-label={t('Movies or TV')}>
      {(['radarr', 'sonarr'] as App[]).map((a) => (
        <button key={a} role="radio" aria-checked={app === a} className={app === a ? 'active' : ''} onClick={() => onChange(a)}>
          {a === 'radarr' ? t('Movies (Radarr)') : t('TV (Sonarr)')}
        </button>
      ))}
    </div>
  )
}

function TrashPanel({ profiles, onImported }: { profiles: QualityProfile[]; onImported: () => void }) {
  const toast = useToast()
  const [app, setApp] = useState<App>('radarr')
  const [data, setData] = useState<{ formats: TrashFormat[]; profiles: TrashProfile[] } | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [reload, setReload] = useState(0)
  const [filter, setFilter] = useState('')
  const [picked, setPicked] = useState<Set<string>>(new Set())
  const [scoreIn, setScoreIn] = useState('')
  const [busy, setBusy] = useState(false)

  useEffect(() => {
    let live = true
    api
      .trashGuides(app)
      .then((d) => live && setData(d))
      .catch((e) => live && setError(errText(e)))
      .finally(() => live && setLoading(false))
    return () => {
      live = false
    }
  }, [app, reload])

  // Reads the list again (another app, after an import, or after an error).
  function load(a: App) {
    setLoading(true)
    setError('')
    setPicked(new Set())
    if (a !== app) {
      setData(null)
      setApp(a)
    } else setReload((n) => n + 1)
  }

  const shown = useMemo(() => {
    const q = filter.trim().toLowerCase()
    return (data?.formats ?? []).filter((f) => !q || f.name.toLowerCase().includes(q))
  }, [data, filter])

  function toggle(id: string) {
    const next = new Set(picked)
    if (next.has(id)) next.delete(id)
    else next.add(id)
    setPicked(next)
  }

  async function importFormats() {
    setBusy(true)
    try {
      const r = await api.importTrashFormats(app, [...picked], scoreIn ? Number(scoreIn) : undefined)
      const changes = [r.added && t('{n} added', { n: r.added }), r.updated && t('{n} updated', { n: r.updated })].filter(Boolean).join(', ')
      toast.success(
        scoreIn
          ? t('Custom formats {changes} and scored in {profile}.', { changes, profile: profiles.find((p) => String(p.id) === scoreIn)?.name ?? '' })
          : t('Custom formats {changes}.', { changes }),
      )
      onImported()
      load(app)
    } catch (e) {
      toast.error(errText(e))
    } finally {
      setBusy(false)
    }
  }

  async function importProfile(tp: TrashProfile) {
    setBusy(true)
    try {
      const r = await api.importTrashProfile(app, tp.trashId)
      const vars = { name: r.profile.name, n: r.profile.formatScores?.length ?? 0, missing: r.formats.missing?.length ?? 0 }
      toast.success(
        vars.missing
          ? t('Created the "{name}" profile with {n} scored formats. {missing} of its formats weren\'t found.', vars)
          : t('Created the "{name}" profile with {n} scored formats.', vars),
      )
      onImported()
      load(app)
    } catch (e) {
      toast.error(errText(e))
    } finally {
      setBusy(false)
    }
  }

  return (
    <div>
      <div style={{ display: 'flex', flexWrap: 'wrap', gap: 12, alignItems: 'center', marginBottom: 12 }}>
        <AppSwitch app={app} onChange={load} />
        <small style={{ color: 'var(--text-dim)' }}>
          {tx('Read from the {link} on GitHub. Importing a format again updates it.', {
            link: (
              <a href="https://trash-guides.info/" target="_blank" rel="noreferrer">
                TRaSH Guides
              </a>
            ),
          })}
        </small>
      </div>
      {loading && <p style={{ color: 'var(--text-dim)' }}>{t('Reading the TRaSH Guides… the first time takes a few seconds.')}</p>}
      {error && (
        <p className="error-text">
          {error} <button onClick={() => load(app)}>{t('Try again')}</button>
        </p>
      )}
      {data && (
        <div className="trash-columns">
          <fieldset className="group">
            <legend>{t('Ready-made profiles')}</legend>
            <p style={{ marginTop: 0 }}>{t("Creates a new quality profile with the qualities, formats and scores TRaSH suggests. Your own profiles aren't changed.")}</p>
            <ul className="trash-profiles">
              {data.profiles.map((tp) => (
                <li key={tp.trashId}>
                  <div>
                    <strong>{tp.name}</strong>
                    <div className="trash-meta">
                      {tp.qualities.join(', ')} · {plural(tp.formats, '{n} format', '{n} formats')}
                      {tp.minFormatScore ? ` · ${t('minimum {n}', { n: String(tp.minFormatScore) })}` : ''}
                    </div>
                  </div>
                  <button onClick={() => void importProfile(tp)} disabled={busy}>
                    {t('Create profile')}
                  </button>
                </li>
              ))}
            </ul>
          </fieldset>
          <fieldset className="group">
            <legend>{t('Formats')}</legend>
            <input type="search" placeholder={t('Filter {n} formats', { n: data.formats.length })} aria-label={t('Filter formats')} value={filter} onChange={(e) => setFilter(e.target.value)} style={{ width: '100%', marginBottom: 8 }} />
            <ul className="trash-formats">
              {shown.map((f) => (
                <li key={f.trashId}>
                  <label>
                    <input type="checkbox" checked={picked.has(f.trashId)} onChange={() => toggle(f.trashId)} />
                    <span>{f.name}</span>
                    {f.defaultScore !== 0 && <small className="trash-score">{f.defaultScore > 0 ? `+${f.defaultScore}` : f.defaultScore}</small>}
                    {f.importedId > 0 && <span className="badge downloaded">{t('added')}</span>}
                  </label>
                </li>
              ))}
            </ul>
            <div className="trash-import-bar">
              <label className="field" style={{ minWidth: 220 }}>
                <span>{t("Give them TRaSH's default score in")}</span>
                <Dropdown
                  label={t('Profile to score them in')}
                  value={scoreIn}
                  options={[{ value: '', label: t('No profile (just add them)') }, ...profiles.map((p) => ({ value: String(p.id), label: p.name }))]}
                  onChange={setScoreIn}
                />
              </label>
              <button className="primary" disabled={picked.size === 0 || busy} onClick={() => void importFormats()}>
                {picked.size === 0 ? t('Pick formats to add') : plural(picked.size, 'Add {n} format', 'Add {n} formats')}
              </button>
            </div>
          </fieldset>
        </div>
      )}
    </div>
  )
}

function PastePanel({ onImported }: { onImported: () => void }) {
  const toast = useToast()
  const [app, setApp] = useState<App>('radarr')
  const [json, setJson] = useState('')
  const [busy, setBusy] = useState(false)

  async function submit() {
    setBusy(true)
    try {
      const r = await api.importCustomFormats(json, app)
      toast.success([r.added && t('{n} added', { n: r.added }), r.updated && t('{n} updated', { n: r.updated })].filter(Boolean).join(', ') + '.')
      setJson('')
      onImported()
    } catch (e) {
      toast.error(errText(e))
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="grid-form" style={{ maxWidth: 760 }}>
      <AppSwitch app={app} onChange={setApp} />
      <label>
        {t('One format or a list, as JSON')}
        <textarea rows={10} value={json} onChange={(e) => setJson(e.target.value)} placeholder='{"name": "HDR", "specifications": [ … ]}' spellCheck={false} style={{ fontFamily: 'var(--font-mono)', fontSize: '0.8rem' }} />
        <small style={{ color: 'var(--text-dim)' }}>
          {t('Use the Export button in a Radarr or Sonarr custom format, or copy a file from the TRaSH Guides. A format with the same name replaces the one you have.')}
        </small>
      </label>
      <div>
        <button className="primary" disabled={!json.trim() || busy} onClick={() => void submit()}>
          {t('Import')}
        </button>
      </div>
    </div>
  )
}

function TestPanel({ profiles, hasFormats }: { profiles: QualityProfile[]; hasFormats: boolean }) {
  const [title, setTitle] = useState('')
  const [profileId, setProfileId] = useState('')
  const [result, setResult] = useState<FormatTestResult | null>(null)
  const [error, setError] = useState('')

  async function run() {
    setError('')
    try {
      setResult(await api.testCustomFormats(title, profileId ? Number(profileId) : undefined))
    } catch (e) {
      setResult(null)
      setError(errText(e))
    }
  }

  return (
    <div className="grid-form" style={{ maxWidth: 760 }}>
      {!hasFormats && <p style={{ color: 'var(--text-dim)', margin: 0 }}>{t('You have no custom formats yet, so nothing will match.')}</p>}
      <form
        className="format-test-form"
        onSubmit={(e) => {
          e.preventDefault()
          void run()
        }}
      >
        <label style={{ flex: '3 1 320px' }}>
          {t('Release name')}
          <input value={title} onChange={(e) => setTitle(e.target.value)} placeholder="Night.Harbour.2021.1080p.BluRay.DDP5.1.x264-GRP" spellCheck={false} />
        </label>
        <label className="field" style={{ flex: '1 1 180px' }}>
          <span>{t('Score with profile')}</span>
          <Dropdown label={t('Profile')} value={profileId} options={[{ value: '', label: t('No profile') }, ...profiles.map((p) => ({ value: String(p.id), label: p.name }))]} onChange={setProfileId} />
        </label>
        <button className="primary" type="submit" disabled={!title.trim()}>
          {t('Test')}
        </button>
      </form>
      {error && <p className="error-text">{error}</p>}
      {result && (
        <div className="format-test-result">
          <p style={{ margin: 0 }}>
            {tx('Quality: {quality}', { quality: <strong>{result.quality}</strong> })}
            {result.score !== undefined && (
              <>
                {' '}
                · {tx('Score: {score}', { score: <strong>{result.score}</strong> })} ·{' '}
                {result.accepted ? <span className="badge downloaded">{t('would be taken')}</span> : <span className="badge conflict">{t('would be skipped')}</span>}
              </>
            )}
          </p>
          {result.reason && <p style={{ margin: '4px 0 0', color: 'var(--text-dim)' }}>{st(result.reason)}</p>}
          {result.matched.length === 0 ? (
            <p style={{ color: 'var(--text-dim)' }}>{t('No custom format matches this name.')}</p>
          ) : (
            <ul className="format-test-matches">
              {result.matched.map((m) => (
                <li key={m.id}>
                  {m.name}
                  {m.score !== undefined && <span className="trash-score">{m.score > 0 ? `+${m.score}` : m.score}</span>}
                </li>
              ))}
            </ul>
          )}
        </div>
      )}
    </div>
  )
}
