import { Fragment, useCallback, useEffect, useState } from 'react'
import FallbackEditor from './FallbackEditor'
import Icon from './Icon'
import { useToast } from './Toast'
import { profileBlurb, sortProfiles } from './qualityBlurb'
import { api, type CustomFormat, type PreferredTerm, type QualityProfile } from '../api'
import { addAnimeTerms } from '../animeGroups'
import { useConfirm } from './ConfirmProvider'
import { firstError, maxLength, required } from '../validate'
import { FieldError, FormProblem, useValidation } from '../useValidation'
import { useReveal } from '../useReveal'
import { plural, t, tx } from '../i18n'
import TipLabel from './TipLabel'

interface Draft {
  id?: number
  name: string
  allowed: string[]
  cutoff: string
  upgradeAllowed: boolean
  // One term per line; preferred lines are "term = score".
  mustContain: string
  mustNotContain: string
  preferred: string
  fallback: number[]
  // The largest release, in GB, as typed ("" = no limit).
  maxSize: string
  // Custom format scores as typed, by format id ("" or 0 = not scored).
  formatScores: Record<number, string>
  minFormatScore: string
  cutoffFormatScore: string
  noSeasonPacks: boolean
  skipBad: boolean
}

const linesOf = (text: string) => text.split('\n').map((l) => l.trim()).filter(Boolean)

function parsePreferred(text: string): PreferredTerm[] {
  return linesOf(text).map((line) => {
    const idx = line.lastIndexOf('=')
    if (idx < 0) return { term: line, score: 10 }
    const score = Number(line.slice(idx + 1).trim())
    return { term: line.slice(0, idx).trim(), score: Number.isFinite(score) ? score : 0 }
  })
}

// Limits the server applies (internal/quality/repo.go, formats_profile.go).
const MAX_FORMAT_SCORE = 1000000
const wholeNumber = (s: string) => /^[+-]?\d+$/.test(s.trim())

function formatScoreProblem(d: Draft | null, formats: CustomFormat[]): string | null {
  if (!d) return null
  for (const f of formats) {
    const s = (d.formatScores[f.id] ?? '').trim()
    if (s !== '' && (!wholeNumber(s) || Math.abs(Number(s)) > MAX_FORMAT_SCORE)) return t('The score for "{name}" must be a whole number, like 1500 or -10000.', { name: f.name })
  }
  if (d.minFormatScore.trim() !== '' && !wholeNumber(d.minFormatScore)) return t('The minimum score must be a whole number, like 0 or -500.')
  if (d.cutoffFormatScore.trim() !== '' && (!wholeNumber(d.cutoffFormatScore) || Number(d.cutoffFormatScore) < 0)) return t('The upgrade-until score must be 0 or more.')
  return null
}

// Limits the server applies (internal/quality/repo.go).
const MAX_TERMS = 50
const MAX_TERM_LEN = 60
const MAX_SCORE = 10000

// The "line too long" message for each kind of term list.
const tooLong: Record<'required' | 'excluded' | 'preferred', (start: string) => string> = {
  required: (start) => t('Each required term line can be at most {max} characters. "{start}..." is too long.', { max: MAX_TERM_LEN, start }),
  excluded: (start) => t('Each excluded term line can be at most {max} characters. "{start}..." is too long.', { max: MAX_TERM_LEN, start }),
  preferred: (start) => t('Each preferred term line can be at most {max} characters. "{start}..." is too long.', { max: MAX_TERM_LEN, start }),
}

// One term per line, no more than 50, each short and free of the | character.
function termLines(text: string, what: 'required' | 'excluded' | 'preferred'): string | null {
  const lines = linesOf(text)
  if (lines.length > MAX_TERMS) return t('Use at most {max} lines here. You have {n}.', { max: MAX_TERMS, n: lines.length })
  const long = lines.find((l) => l.length > MAX_TERM_LEN)
  if (long) return tooLong[what](long.slice(0, 24))
  if (lines.some((l) => l.includes('|'))) return t("Terms can't contain the | character. Put each term on its own line instead.")
  return null
}

// Preferred lines look like "x265 = 50". A line with no = counts as a score of 10.
function preferredLines(text: string): string | null {
  const lines = linesOf(text)
  const tooMany = termLines(text, 'preferred')
  if (tooMany) return tooMany
  for (const line of lines) {
    const idx = line.lastIndexOf('=')
    if (idx < 0) continue
    const term = line.slice(0, idx).trim()
    const score = line.slice(idx + 1).trim()
    if (!term) return t('"{line}" has a score but no term. Write it like x265 = 50.', { line })
    if (!/^[+-]?\d+$/.test(score) || Math.abs(Number(score)) > MAX_SCORE) return t('The score in "{line}" must be a whole number between -{max} and {max}, like x265 = 50.', { line, max: MAX_SCORE })
  }
  return null
}

const draftFrom = (p: QualityProfile): Draft => ({
  id: p.id,
  name: p.name,
  allowed: p.allowed,
  cutoff: p.cutoff,
  upgradeAllowed: p.upgradeAllowed,
  mustContain: p.mustContain.join('\n'),
  mustNotContain: p.mustNotContain.join('\n'),
  preferred: p.preferred.map((x) => `${x.term} = ${x.score}`).join('\n'),
  fallback: p.fallback ?? [],
  maxSize: p.maxSizeGB ? String(p.maxSizeGB) : '',
  formatScores: Object.fromEntries((p.formatScores ?? []).map((f) => [f.formatId, String(f.score)])),
  minFormatScore: p.minFormatScore ? String(p.minFormatScore) : '',
  cutoffFormatScore: p.cutoffFormatScore ? String(p.cutoffFormatScore) : '',
  noSeasonPacks: !!p.noSeasonPacks,
  skipBad: p.skipBadReleases !== false,
})

const blankDraft = (tiers: string[]): Draft => ({
  name: '',
  allowed: tiers.filter((t) => t === 'WEBDL-1080p' || t === 'Bluray-1080p'),
  cutoff: 'Bluray-1080p',
  upgradeAllowed: false,
  mustContain: '',
  mustNotContain: '',
  preferred: '',
  fallback: [],
  maxSize: '',
  formatScores: {},
  minFormatScore: '',
  cutoffFormatScore: '',
  noSeasonPacks: false,
  skipBad: true,
})

// "1080p copy", or "1080p copy 2" when that name is taken.
function copyName(name: string, profiles: QualityProfile[]): string {
  const taken = new Set(profiles.map((p) => p.name.toLowerCase()))
  let n = t('{name} copy', { name }).slice(0, 60)
  for (let i = 2; taken.has(n.toLowerCase()); i++) n = t('{name} copy {n}', { name, n: String(i) }).slice(0, 60)
  return n
}

const tierLabel = (tier: string) => (tier === 'Unknown' ? t('Unknown (untagged releases)') : tier)

// Editable quality profiles, Radarr/Sonarr style: which qualities are
// acceptable, the cutoff at which upgrading stops, and whether upgrades
// happen at all (off unless switched on). The default profile applies to
// anything without its own.
export default function QualityProfilesSection({ rev = 0, onChanged, advanced = false }: { rev?: number; onChanged?: () => void; advanced?: boolean }) {
  const confirm = useConfirm()
  const [profiles, setProfiles] = useState<QualityProfile[]>([])
  const [formats, setFormats] = useState<CustomFormat[]>([])
  const [tiers, setTiers] = useState<string[]>([])
  const [defaultId, setDefaultId] = useState(0)
  const [draft, setDraft] = useState<Draft | null>(null)
  const [error, setError] = useState('')
  const [saving, setSaving] = useState(false)
  const orderedFor = (d: Draft) => tiers.filter((t) => d.allowed.includes(t))
  const errors = {
    name: firstError(required(draft?.name, t('Give this profile a name, for example Movies 1080p.')), maxLength(draft?.name ?? '', 60, t('The name'))),
    allowed: draft && draft.allowed.length === 0 ? t('Tick at least one quality to accept.') : null,
    cutoff: draft && draft.allowed.length > 0 && !orderedFor(draft).includes(draft.cutoff) ? t('Choose the quality to stop upgrading at.') : null,
    mustContain: termLines(draft?.mustContain ?? '', 'required'),
    mustNotContain: termLines(draft?.mustNotContain ?? '', 'excluded'),
    preferred: preferredLines(draft?.preferred ?? ''),
    maxSize: draft && draft.maxSize.trim() !== '' && !(Number(draft.maxSize) > 0 && Number(draft.maxSize) <= 1000) ? t('Write a size from 0.1 to 1000 GB, or leave it empty for no limit.') : null,
    formatScores: formatScoreProblem(draft, formats),
  }
  const v = useValidation(errors)
  // Counts the times a form has been opened, so it can be scrolled into view
  // and given the cursor each time (see useReveal).
  const [opened, setOpened] = useState(0)
  const formRef = useReveal<HTMLDivElement>(opened)
  function openDraft(d: Draft | null) {
    v.reset()
    setDraft(d)
    if (d) setOpened((n) => n + 1)
  }

  const load = useCallback(() => {
    api
      .listProfiles()
      .then((r) => {
        setProfiles(sortProfiles(r.profiles))
        setTiers(r.tiers)
        setDefaultId(r.defaultId)
      })
      .catch((e) => setError(e instanceof Error ? e.message : String(e)))
    api
      .listCustomFormats()
      .then((r) => setFormats(r.formats))
      .catch(() => setFormats([]))
  }, [])
  useEffect(load, [load, rev])

  async function save() {
    if (!draft || !v.attempt()) return
    setError('')
    setSaving(true)
    try {
      const body = {
        name: draft.name.trim(),
        allowed: draft.allowed,
        cutoff: draft.cutoff,
        upgradeAllowed: draft.upgradeAllowed,
        mustContain: linesOf(draft.mustContain),
        mustNotContain: linesOf(draft.mustNotContain),
        preferred: parsePreferred(draft.preferred),
        fallback: draft.fallback,
        maxSizeGB: draft.maxSize.trim() === '' ? 0 : Number(draft.maxSize),
        formatScores: Object.entries(draft.formatScores)
          .filter(([, s]) => s.trim() !== '' && Number(s) !== 0)
          .map(([id, s]) => ({ formatId: Number(id), score: Number(s) })),
        minFormatScore: draft.minFormatScore.trim() === '' ? 0 : Number(draft.minFormatScore),
        cutoffFormatScore: draft.cutoffFormatScore.trim() === '' ? 0 : Number(draft.cutoffFormatScore),
        noSeasonPacks: draft.noSeasonPacks,
        skipBadReleases: draft.skipBad,
      }
      if (draft.id) await api.updateProfile(draft.id, body)
      else await api.createProfile(body)
      openDraft(null)
      load()
      onChanged?.()
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setSaving(false)
    }
  }

  async function remove(p: QualityProfile) {
    if (!(await confirm({ title: t('Delete the "{name}" profile?', { name: p.name }), body: <p>{t('Movies and shows using it switch to the default profile.')}</p>, confirmLabel: t('Delete profile'), danger: true }))) return
    setError('')
    try {
      await api.deleteProfile(p.id)
      load()
      onChanged?.()
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    }
  }

  async function makeDefault(id: number) {
    setError('')
    try {
      await api.putSettings({ defaultProfileId: id })
      load()
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    }
  }

  function toggleTier(t: string) {
    if (!draft) return
    const allowed = draft.allowed.includes(t) ? draft.allowed.filter((x) => x !== t) : [...draft.allowed, t]
    // Keep the cutoff valid: if its tier was just unticked, fall back to the best remaining one.
    const ordered = tiers.filter((x) => allowed.includes(x))
    const cutoff = ordered.includes(draft.cutoff) ? draft.cutoff : (ordered[ordered.length - 1] ?? '')
    setDraft({ ...draft, allowed, cutoff })
  }

  const orderedAllowed = draft ? tiers.filter((t) => draft.allowed.includes(t)) : []
  const defaultProfile = profiles.find((p) => p.id === defaultId)

  const form = draft && (
    <div className="profile-form" ref={formRef}>
      <h3>{draft.id ? t('Edit profile') : t('New profile')}</h3>
      <div className="grid-form profile-form-col">
        <label>
          {t('Name')}
          <input value={draft.name} onChange={(e) => setDraft({ ...draft, name: e.target.value })} {...v.bind('name', draft.name, (name) => setDraft({ ...draft, name }))} />
          <FieldError v={v} name="name" />
        </label>
        <fieldset className="profile-fieldset">
          <legend><TipLabel text={t('Only releases in these qualities are downloaded. A release in any other quality is skipped.')}>{t('Accepted qualities')}</TipLabel></legend>
          <div className="profile-tiers">
            {tiers.map((t) => (
              <label key={t}>
                <input type="checkbox" checked={draft.allowed.includes(t)} onChange={() => toggleTier(t)} />
                {tierLabel(t)}
              </label>
            ))}
          </div>
          <FieldError v={v} name="allowed" />
        </fieldset>
        <label>
          <TipLabel text={t('Once a title has a file at this quality or better, Mediarium stops looking for upgrades. It only matters when better versions are on.')}>{t('Stop upgrading once this quality is reached')}</TipLabel>
          <select value={draft.cutoff} onChange={(e) => setDraft({ ...draft, cutoff: e.target.value })} {...v.bind('cutoff')}>
            {orderedAllowed.map((t) => (
              <option key={t} value={t}>
                {tierLabel(t)}
              </option>
            ))}
          </select>
          <FieldError v={v} name="cutoff" />
        </label>
        <label>
          {t('Largest download (GB)')}
          <input inputMode="decimal" value={draft.maxSize} placeholder={t('no limit')} style={{ maxWidth: 140 }} onChange={(e) => setDraft({ ...draft, maxSize: e.target.value })} {...v.bind('maxSize', draft.maxSize, (maxSize) => setDraft({ ...draft, maxSize }))} />
          <small style={{ color: 'var(--text-dim)' }}>{t('Bigger releases are skipped. Leave empty for no limit. Fakes that are far too small are always skipped.')}</small>
          <FieldError v={v} name="maxSize" />
        </label>
        <label className="check-hint">
          <input type="checkbox" checked={draft.upgradeAllowed} onChange={(e) => setDraft({ ...draft, upgradeAllowed: e.target.checked })} />
          <span>
            {t('Keep looking for better versions after it is downloaded')}
            <small>{t('Swaps a download for a better one until it reaches the quality above.')}</small>
          </span>
        </label>
        <label className="check-hint">
          <input type="checkbox" checked={draft.skipBad} onChange={(e) => setDraft({ ...draft, skipBad: e.target.checked })} />
          <span>
            {t('Skip bad releases')}
            <small>{t('Leaves out whole Blu-ray discs, 3D, upscaled copies, burned-in subtitles and groups known for poor re-encodes. Prefers fixed releases (PROPER, REPACK).')}</small>
          </span>
        </label>
        <label className="check-hint">
          <input type="checkbox" checked={draft.noSeasonPacks} onChange={(e) => setDraft({ ...draft, noSeasonPacks: e.target.checked })} />
          <span>
            {t('Never download season packs')}
            <small>{t('TV episodes are fetched one by one, even when a whole season is out.')}</small>
          </span>
        </label>
      </div>
      <div className="grid-form profile-form-col">
        <fieldset className="profile-fieldset">
          <legend><TipLabel text={t('If nothing fits this profile and the title has no file yet, these profiles are tried in order. A better match replaces that file later.')} learnMore="quality#fallback-profiles">{t('If nothing is found at this quality, also try')}</TipLabel></legend>
          <FallbackEditor profiles={profiles} selfId={draft.id} value={draft.fallback} onChange={(fallback) => setDraft({ ...draft, fallback })} />
        </fieldset>
        <details open={!!(draft.mustContain || draft.mustNotContain || draft.preferred)}>
          <summary style={{ cursor: 'pointer' }}>{t('Release restrictions & preferred terms (optional)')}</summary>
          <div className="grid-form" style={{ marginTop: 8 }}>
            <p style={{ color: 'var(--text-dim)', fontSize: '0.85rem', margin: 0 }}>
              {t('One term per line. Matched anywhere in the release title, ignoring capitals, with dots and dashes counting as spaces.')}
            </p>
            <label>
              {t('Must contain at least one of')}
              <textarea rows={3} value={draft.mustContain} onChange={(e) => setDraft({ ...draft, mustContain: e.target.value })} placeholder="x265" {...v.bind('mustContain', draft.mustContain, (mustContain) => setDraft({ ...draft, mustContain }))} />
              <FieldError v={v} name="mustContain" />
            </label>
            <label>
              {t('Must not contain any of')}
              <textarea rows={3} value={draft.mustNotContain} onChange={(e) => setDraft({ ...draft, mustNotContain: e.target.value })} placeholder={'HDR\nhardcoded'} {...v.bind('mustNotContain', draft.mustNotContain, (mustNotContain) => setDraft({ ...draft, mustNotContain }))} />
              <FieldError v={v} name="mustNotContain" />
            </label>
            <label>
              {t('Preferred terms, with a score (higher wins between releases of the same quality)')}
              <textarea rows={3} value={draft.preferred} onChange={(e) => setDraft({ ...draft, preferred: e.target.value })} placeholder={'x265 = 50\nREPACK = 10\nDUBBED = -100'} {...v.bind('preferred', draft.preferred, (preferred) => setDraft({ ...draft, preferred }))} />
              <FieldError v={v} name="preferred" />
            </label>
            <div className="anime-terms-help">
              <p style={{ color: 'var(--text-dim)', fontSize: '0.85rem', margin: 0 }}>
                {t("For anime, the release group often matters more than anything else: the same episode comes out from several groups, and they differ in speed, in how it is encoded and in which languages it keeps. Give the groups you like a higher score. A term such as Dual Audio works the same way. The button adds a few well-known groups to start from; change their scores to what suits you.")}
              </p>
              <button type="button" className="btn-sm" onClick={() => setDraft({ ...draft, preferred: addAnimeTerms(draft.preferred) })}>
                {t('Add anime groups')}
              </button>
            </div>
          </div>
        </details>
        {advanced && <details open={Object.values(draft.formatScores).some((s) => s.trim() !== '' && Number(s) !== 0) || draft.minFormatScore !== '' || draft.cutoffFormatScore !== ''}>
          <summary style={{ cursor: 'pointer' }}>{t('Custom format scores (optional)')}</summary>
          {formats.length === 0 ? (
            <p style={{ color: 'var(--text-dim)', fontSize: '0.85rem' }}>{t('No custom formats yet. Import some under Custom formats below, from the TRaSH Guides or from Radarr or Sonarr.')}</p>
          ) : (
            <div className="grid-form" style={{ marginTop: 8 }}>
              <p style={{ color: 'var(--text-dim)', fontSize: '0.85rem', margin: 0 }}>
                {t('A release gets the score of every format it matches. Between releases of the same quality, the higher total wins. Leave a format empty to ignore it.')}
              </p>
              <div className="format-scores">
                {formats.map((f) => (
                  <label key={f.id} className="format-score-row">
                    <span>
                      {f.name} <small>{f.app === 'sonarr' ? t('TV') : t('movies')}</small>
                    </span>
                    <input
                      inputMode="numeric"
                      aria-label={t('Score for {name}', { name: f.name })}
                      value={draft.formatScores[f.id] ?? ''}
                      placeholder="0"
                      onChange={(e) => setDraft({ ...draft, formatScores: { ...draft.formatScores, [f.id]: e.target.value } })}
                    />
                  </label>
                ))}
              </div>
              <div className="format-thresholds">
                <label>
                  {t('Minimum score')}
                  <input inputMode="numeric" value={draft.minFormatScore} placeholder="0" onChange={(e) => setDraft({ ...draft, minFormatScore: e.target.value })} />
                  <small>{t('Releases scoring less are skipped.')}</small>
                </label>
                <label>
                  {t('Upgrade until score')}
                  <input inputMode="numeric" value={draft.cutoffFormatScore} placeholder={t('0 (never)')} onChange={(e) => setDraft({ ...draft, cutoffFormatScore: e.target.value })} />
                  <small>{t('With better versions on, a higher-scoring release replaces a file below this.')}</small>
                </label>
              </div>
              <FieldError v={v} name="formatScores" />
            </div>
          )}
        </details>}
      </div>
      <div className="profile-form-actions">
        <button className="primary" onClick={save} disabled={saving}>
          {t('Save profile')}
        </button>
        <button onClick={() => openDraft(null)}>{t('Cancel')}</button>
        <FormProblem v={v} verb={t('save this profile')} />
      </div>
    </div>
  )

  return (
    <section className="card wide">
      <h2><TipLabel help="quality">{t('Quality profiles')}</TipLabel></h2>
      <p style={{ color: 'var(--text-dim)' }}>
        {t('A profile sets which releases are acceptable and whether Mediarium keeps looking for a better version. Movies and shows use the default unless you pick another on their page.')}
      </p>
      {error && <p className="error-text">{error}</p>}

      <table className="profiles-table" style={{ marginBottom: 16 }}>
        <thead>
          <tr>
            <th>{t('Name')}</th>
            <th>{t('Qualities')}</th>
            <th style={{ whiteSpace: 'nowrap' }}><TipLabel text={t('The cutoff. Once a title has a file at this quality or better, Mediarium stops looking for upgrades.')}>{t('Stops upgrading at')}</TipLabel></th>
            <th style={{ whiteSpace: 'nowrap' }}><TipLabel text={t('On: a title with a file below the cutoff keeps being searched for a better one. Off: the first acceptable release is kept.')}>{t('Better versions')}</TipLabel></th>
            <th>
              <span className="sr-only">{t('Actions')}</span>
            </th>
          </tr>
        </thead>
        <tbody>
          {profiles.map((p) => (
            <Fragment key={p.id}>
            <tr className={[draft?.id === p.id ? 'editing' : '', p.id === defaultId ? 'is-default' : ''].filter(Boolean).join(' ') || undefined}>
              <td>
                {p.name} {p.id === defaultId && <span className="badge downloaded">{t('default')}</span>}
                {p.inUse > 0 && <div style={{ color: 'var(--text-dim)', fontSize: '0.75rem' }}>{t('used by {n}', { n: p.inUse })}</div>}
                <div style={{ color: 'var(--text-dim)', fontSize: '0.8rem', maxWidth: 320 }}>{profileBlurb(p)}</div>
                {(p.fallback ?? []).length > 0 && (
                  <div className="fallback-summary">
                    {t('If nothing is found: {list}', { list: (p.fallback ?? []).map((id) => profiles.find((x) => x.id === id)?.name).filter(Boolean).join(' → ') })}
                  </div>
                )}
              </td>
              <td style={{ color: 'var(--text-dim)', fontSize: '0.85rem' }}>
                {p.allowed.map(tierLabel).join(', ')}
                {(p.mustContain.length > 0 || p.mustNotContain.length > 0 || p.preferred.length > 0) && (
                  <div>
                    {p.mustContain.length > 0 && <span>{t('requires: {list}.', { list: p.mustContain.join(', ') })} </span>}
                    {p.mustNotContain.length > 0 && <span>{t('excludes: {list}.', { list: p.mustNotContain.join(', ') })} </span>}
                    {p.preferred.length > 0 && <span>{t('prefers: {list}.', { list: p.preferred.map((x) => `${x.term} (${x.score})`).join(', ') })}</span>}
                  </div>
                )}
                {((p.formatScores ?? []).length > 0 || p.noSeasonPacks || p.skipBadReleases === false) && (
                  <div>
                    {p.skipBadReleases === false && <span>{t("Doesn't skip bad releases.")} </span>}
                    {(p.formatScores ?? []).length > 0 && (
                      <span>
                        {[
                          plural((p.formatScores ?? []).length, '{n} custom format', '{n} custom formats'),
                          p.minFormatScore ? t('minimum {score}', { score: p.minFormatScore }) : '',
                          p.cutoffFormatScore ? t('upgrades until {score}', { score: p.cutoffFormatScore }) : '',
                        ].filter(Boolean).join(', ')}.{' '}
                      </span>
                    )}
                    {p.noSeasonPacks && <span>{t('No season packs.')}</span>}
                  </div>
                )}
              </td>
              <td style={{ whiteSpace: 'nowrap' }}>{tierLabel(p.cutoff)}</td>
              <td>{p.upgradeAllowed ? t('on') : t('off')}</td>
              <td style={{ whiteSpace: 'nowrap' }}>
                <button onClick={() => openDraft(draftFrom(p))}>{t('Edit')}</button>{' '}
                <button onClick={() => openDraft({ ...draftFrom(p), id: undefined, name: copyName(p.name, profiles) })}>{t('Duplicate')}</button>{' '}
                {p.id !== defaultId && <button onClick={() => makeDefault(p.id)}>{t('Make default')}</button>}{' '}
                {p.id !== defaultId && <button onClick={() => remove(p)}>{t('Delete')}</button>}
              </td>
            </tr>
            {draft?.id === p.id && (
              <tr className="profile-edit-row">
                <td colSpan={5}>{form}</td>
              </tr>
            )}
            </Fragment>
          ))}
        </tbody>
      </table>

      {defaultProfile && (
        <DefaultFallback key={defaultProfile.id} profile={defaultProfile} profiles={profiles} onSaved={load} />
      )}

      {draft && !draft.id && form}
      {!draft && <button onClick={() => openDraft(blankDraft(tiers))}>{t('New profile…')}</button>}
    </section>
  )
}

// The default profile's fallback order, right under the list where the
// default is chosen: pick the default first, then its fallbacks.
function DefaultFallback({ profile, profiles, onSaved }: { profile: QualityProfile; profiles: QualityProfile[]; onSaved: () => void }) {
  const toast = useToast()
  const [value, setValue] = useState<number[]>(profile.fallback ?? [])
  const [saving, setSaving] = useState(false)
  const changed = JSON.stringify(value) !== JSON.stringify(profile.fallback ?? [])

  async function save() {
    setSaving(true)
    try {
      await api.updateProfile(profile.id, {
        name: profile.name,
        allowed: profile.allowed,
        cutoff: profile.cutoff,
        upgradeAllowed: profile.upgradeAllowed,
        mustContain: profile.mustContain,
        mustNotContain: profile.mustNotContain,
        preferred: profile.preferred,
        fallback: value,
      })
      toast.success(value.length ? plural(value.length, 'Saved: {n} fallback quality.', 'Saved: {n} fallback qualities.') : t('Saved: no fallback, it waits for {name}.', { name: profile.name }))
      onSaved()
    } catch (e) {
      toast.error(e instanceof Error ? e.message : String(e))
    } finally {
      setSaving(false)
    }
  }

  return (
    <fieldset className="group quality-fallback">
      <legend>
        <Icon name="sliders" size={14} /> {t('When {name} (your default) finds nothing', { name: profile.name })}
      </legend>
      <p>
        {tx('Tick the qualities to try instead and drag them into order. A title grabbed this way is upgraded once a {name} release appears.', { name: <strong>{profile.name}</strong> })}
      </p>
      <FallbackEditor profiles={profiles} selfId={profile.id} value={value} onChange={setValue} />
      <div style={{ marginTop: 12 }}>
        <button className="primary" onClick={() => void save()} disabled={!changed || saving}>
          {saving ? t('Saving…') : t('Save order')}
        </button>
      </div>
    </fieldset>
  )
}
