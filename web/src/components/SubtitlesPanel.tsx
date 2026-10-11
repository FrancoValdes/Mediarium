import { useCallback, useEffect, useMemo, useState } from 'react'
import { api, type SubtitleQuota, type SubtitleResult, type SubtitleSlot } from '../api'
import { t } from '../i18n'
import { languageName } from '../languages'
import { slotName } from '../subtitleProfiles'
import { SUBTITLE_FIRST_DIRECTION, sortSubtitles, type SortDir, type SubtitleSortKey } from '../releaseSort'
import Icon from './Icon'
import QuotaNote from './QuotaNote'
import NoSourceNote from './NoSourceNote'
import { noSourceInQuota } from '../subtitleSources'

// Subtitles for one downloaded movie or episode: which of the configured
// languages are already next to the file, plus a manual search that lists
// candidates best-fit first (subtitles timed for the same release group,
// source and resolution as the video score highest).
export default function SubtitlesPanel({ kind, id }: { kind: 'movie' | 'episode'; id: number }) {
  const [languages, setLanguages] = useState<string[]>([])
  const [present, setPresent] = useState<string[]>([])
  const [embedded, setEmbedded] = useState<string[]>([])
  const [slots, setSlots] = useState<SubtitleSlot[]>([])
  const [lang, setLang] = useState('')
  const [results, setResults] = useState<SubtitleResult[] | null>(null)
  const [searching, setSearching] = useState(false)
  const [sort, setSort] = useState<{ key: SubtitleSortKey; dir: SortDir } | null>(null)
  const [status, setStatus] = useState('')
  const [error, setError] = useState('')
  const [quota, setQuota] = useState<SubtitleQuota | null>(null)
  useEffect(() => {
    api.subtitleQuota().then(setQuota).catch(() => undefined)
  }, [status])

  const loadStatus = useCallback(() => {
    const req = kind === 'movie' ? api.movieSubtitleStatus(id) : api.episodeSubtitleStatus(id)
    req
      .then((s) => {
        setLanguages(s.languages)
        setPresent(s.present)
        setEmbedded(s.embedded ?? [])
        setSlots(s.slots ?? [])
        setLang((cur) => cur || s.languages[0] || 'en')
      })
      .catch((e) => setError(e instanceof Error ? e.message : String(e)))
  }, [kind, id])
  useEffect(loadStatus, [loadStatus])

  async function search(language: string, variant = '') {
    setLang(language)
    setSearching(true)
    setResults(null)
    setError('')
    setStatus('')
    try {
      setResults(await (kind === 'movie' ? api.searchSubtitles(id, language, variant) : api.searchEpisodeSubtitles(id, language, variant)))
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setSearching(false)
    }
  }

  async function download(pick: SubtitleResult) {
    setStatus(t('Downloading…'))
    setError('')
    try {
      const res = await (kind === 'movie' ? api.downloadSubtitle(id, pick, lang) : api.downloadEpisodeSubtitle(id, pick, lang))
      setStatus(t('Saved to {path}', { path: res.path }))
      loadStatus()
    } catch (e) {
      setStatus('')
      setError(e instanceof Error ? e.message : String(e))
    }
  }

  const sorted = useMemo(() => sortSubtitles(results ?? [], sort?.key ?? null, sort?.dir ?? 1), [results, sort])
  function clickSort(key: SubtitleSortKey) {
    setSort((cur) => {
      if (!cur || cur.key !== key) return { key, dir: SUBTITLE_FIRST_DIRECTION[key] }
      if (cur.dir === SUBTITLE_FIRST_DIRECTION[key]) return { key, dir: (-SUBTITLE_FIRST_DIRECTION[key]) as SortDir }
      return null
    })
  }
  const head = (key: SubtitleSortKey, label: string) => (
    <th aria-sort={sort?.key === key ? (sort.dir === 1 ? 'ascending' : 'descending') : 'none'}>
      <button type="button" className="th-sort" onClick={() => clickSort(key)}>
        {label}
        <span className="th-arrow" aria-hidden="true">
          {sort?.key === key ? (sort.dir === 1 ? '▲' : '▼') : ''}
        </span>
      </button>
    </th>
  )
  const sourceName = (r: SubtitleResult) => (r.source === 'subdl' ? 'SubDL' : r.source === 'addon' ? t('Add-on') : 'OpenSubtitles')

  return (
    <div>
      <QuotaNote quota={quota} />
      <NoSourceNote quota={quota} />
      <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap', alignItems: 'center', marginBottom: 8 }}>
        {slots.length > 0
          ? slots.map((sl) => (
              <span key={`${sl.language}.${sl.kind}`} className={`badge ${sl.present ? 'downloaded' : 'missing'}`} title={sl.embedded ? t('A subtitle track inside the video file') : undefined}>
                {slotName(sl.kind === 'forced' ? `${sl.language}.forced` : sl.language, languageName)} {sl.embedded ? t('✓ in the video') : sl.present ? '✓' : t('missing')}
              </span>
            ))
          : languages.map((l) => (
              <span key={l} className={`badge ${present.includes(l) ? 'downloaded' : 'missing'}`} title={embedded.includes(l) ? t('A subtitle track inside the video file') : undefined}>
                {languageName(l)} {embedded.includes(l) ? t('✓ in the video') : present.includes(l) ? '✓' : t('missing')}
              </span>
            ))}
      </div>
      <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap', alignItems: 'center' }}>
        {languages.map((l) => (
          <button key={l} disabled={searching || noSourceInQuota(quota)} onClick={() => search(l)}>
            {t('Search {language}', { language: languageName(l) })}
          </button>
        ))}
        {languages
          .filter((l) => slots.some((sl) => sl.language === l && sl.kind === 'forced'))
          .map((l) => (
            <button key={`${l}.forced`} disabled={searching || noSourceInQuota(quota)} onClick={() => search(l, 'forced')} title={t('Only subtitles with the lines spoken in another language')}>
              {t('Search {language} forced', { language: languageName(l) })}
            </button>
          ))}
      </div>
      {error && <p className="error-text">{error}</p>}
      {status && <p style={{ color: 'var(--text-dim)' }}>{status}</p>}
      {searching && <p style={{ color: 'var(--text-dim)' }}>{t('Searching…')}</p>}
      {results !== null && results.length === 0 && (
        <p style={{ color: 'var(--text-dim)' }}>
          {t('No {language} subtitles found.', { language: languageName(lang) })}{' '}
          <button className="btn-sm" onClick={() => setResults(null)}>
            {t('Close')}
          </button>
        </p>
      )}
      {results !== null && results.length > 0 && (
        <>
        <div className="rel-tools" style={{ marginTop: 8 }}>
          <span className="rel-count">{t('{n} subtitles found', { n: results.length })}</span>
          <button type="button" className="btn-sm btn-with-icon rel-close" onClick={() => setResults(null)}>
            <Icon name="x" size={14} /> {t('Close')}
          </button>
        </div>
        <table style={{ marginTop: 8 }}>
          <thead>
            <tr>
              {head('release', t('Release'))}
              {head('source', t('From'))}
              {head('rating', t('Rating'))}
              {head('fit', t('Fit'))}
              <th></th>
            </tr>
          </thead>
          <tbody>
            {sorted.map((r) => (
              <tr key={r.link ?? r.fileId}>
                <td style={{ wordBreak: 'break-all' }}>
                  {r.release || '—'}{' '}
                  {r.variant === 'sdh' && <span className="badge" title={t('Also describes sounds, for people who are deaf or hard of hearing')}>{t('SDH')}</span>}
                  {r.variant === 'forced' && <span className="badge" title={t('Only the lines spoken in another language')}>{t('forced')}</span>}{' '}
                  {r.hashMatch ? <span className="badge downloaded" title={t('Timed for this exact video file')}>{t('made for this file')}</span> : r === results[0] && <span className="badge downloaded">{t('best match')}</span>}
                </td>
                <td>{sourceName(r)}</td>
                <td>{r.source ? '—' : r.rating}</td>
                <td>{Math.round(r.score)}</td>
                <td>
                  <button onClick={() => download(r)} disabled={status === t('Downloading…')}>
                    {t('Download')}
                  </button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
        </>
      )}
    </div>
  )
}
