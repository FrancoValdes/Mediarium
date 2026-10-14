import { sortProfiles } from './qualityBlurb'
import { useEffect, useState } from 'react'
import { defaultProfileFor } from '../profileDefaults'
import { api, isAdmin, type QualityProfile, type SourcePref } from '../api'
import { useAuth } from '../AuthContext'
import { t } from '../i18n'
import InfoTip from './InfoTip'
import HelpLink from './HelpLink'
import { useToast } from './Toast'

const SOURCE_NAME: Record<string, string> = { both: t('Usenet and torrents'), usenet: t('Usenet only'), torrent: t('Torrents only') }

// Per-item download choices for a movie or a show: which quality profile it
// follows and which downloaders it may use. "Default" follows Settings.
export default function ProfilePicker(props: { kind: 'movie' | 'series'; itemId: number; children?: React.ReactNode }) {
  const { user } = useAuth()
  if (isAdmin(user)) return <Picker {...props} />
  // Without the pickers (not an administrator) the tags still show.
  return props.children ? <div className="picker-row">{props.children}</div> : null
}

function Picker({ kind, itemId, children }: { kind: 'movie' | 'series'; itemId: number; children?: React.ReactNode }) {
  const [profiles, setProfiles] = useState<QualityProfile[]>([])
  const [defaultId, setDefaultId] = useState(0)
  const [defaultSources, setDefaultSources] = useState<SourcePref>('both')
  const [current, setCurrent] = useState<number | null>(null)
  const [sources, setSources] = useState<SourcePref>('')
  const [noUpgrade, setNoUpgrade] = useState(false)
  // More than one library folder: which one the next files go to ('' = the main one).
  const [folders, setFolders] = useState<string[]>([])
  const [root, setRoot] = useState('')
  const toast = useToast()

  useEffect(() => {
    api.listProfiles().then((r) => {
      setProfiles(sortProfiles(r.profiles))
      setDefaultId(defaultProfileFor(kind, r))
    }).catch(() => undefined)
    api.getSettings().then((s) => setDefaultSources(s.defaultSources ?? 'both')).catch(() => undefined)
    const load = kind === 'movie' ? api.getMovie(itemId) : api.getSeries(itemId)
    load
      .then((item) => {
        setCurrent(item.profileId ?? 0)
        setSources(item.sources ?? '')
        setNoUpgrade(!!item.noUpgrade)
        setRoot(item.rootPath ?? '')
      })
      .catch(() => undefined)
    api
      .libraryFolders()
      .then((f) => setFolders(kind === 'movie' ? f.movies : f.tv))
      .catch(() => undefined)
  }, [kind, itemId])

  async function chooseFolder(next: string) {
    const previous = root
    setRoot(next)
    try {
      if (kind === 'movie') await api.setMovieFolder(itemId, next)
      else await api.setSeriesFolder(itemId, next)
      toast.success(t('Saved. New files go to that folder; the ones already there stay where they are.'))
    } catch (e) {
      setRoot(previous)
      toast.error(e instanceof Error ? e.message : String(e))
    }
  }

  async function chooseProfile(id: number) {
    const previous = current
    setCurrent(id)
    try {
      const res = kind === 'movie' ? await api.setMovieProfile(itemId, id) : await api.setSeriesProfile(itemId, id)
      if (res?.searching) {
        setNoUpgrade(false)
        toast.success(t('Quality profile saved. Looking for a release in that quality now.'))
      } else {
        toast.success(t('Quality profile saved.'))
      }
    } catch (e) {
      setCurrent(previous)
      toast.error(e instanceof Error ? e.message : String(e))
    }
  }

  async function chooseNoUpgrade(next: boolean) {
    const previous = noUpgrade
    setNoUpgrade(next)
    try {
      if (kind === 'movie') await api.setMovieNoUpgrade(itemId, next)
      else await api.setSeriesNoUpgrade(itemId, next)
      toast.success(next ? t('Keeping what you have.') : t('Looking for better versions.'))
    } catch (e) {
      setNoUpgrade(previous)
      toast.error(e instanceof Error ? e.message : String(e))
    }
  }

  async function chooseSources(next: SourcePref) {
    const previous = sources
    setSources(next)
    try {
      if (kind === 'movie') await api.setMovieSources(itemId, next)
      else await api.setSeriesSources(itemId, next)
      toast.success(next ? t('Downloading from: {source}.', { source: SOURCE_NAME[next] }) : t('Using the default download source.'))
    } catch (e) {
      setSources(previous)
      toast.error(e instanceof Error ? e.message : String(e))
    }
  }

  if (current === null || profiles.length === 0) return null
  const defaultName = profiles.find((p) => p.id === defaultId)?.name ?? t('default')

  return (
    <div className="picker-row">
      <label>
        <span className="has-tip">
          {t('Quality profile')}
          <HelpLink topic="quality" text={t('Decides which releases are good enough for this title. Moving it to a higher profile than the file you have makes Mediarium look for that quality straight away.')} />
        </span>
        <select value={current} onChange={(e) => void chooseProfile(Number(e.target.value))}>
          <option value={0}>{t('Default ({name})', { name: defaultName })}</option>
          {profiles.map((p) => (
            <option key={p.id} value={p.id}>
              {p.name}
            </option>
          ))}
        </select>
      </label>
      <label>
        <span className="has-tip">
          {t('Download from')}
          <InfoTip text={t('Which downloaders may fetch this title. "Default" follows your settings; Usenet only or Torrents only rules the other one out for this title alone.')} />
        </span>
        <select value={sources} onChange={(e) => void chooseSources(e.target.value as SourcePref)}>
          <option value="">{t('Default ({name})', { name: SOURCE_NAME[defaultSources] ?? t('Usenet and torrents') })}</option>
          <option value="both">{SOURCE_NAME.both}</option>
          <option value="usenet">{SOURCE_NAME.usenet}</option>
          <option value="torrent">{SOURCE_NAME.torrent}</option>
        </select>
      </label>
      <label>
        <span className="has-tip">
          {t('Better versions')}
          <InfoTip text={t("Look for better versions swaps the file for a better release when the quality profile allows it, until it reaches the profile's cutoff. Leave what I have alone keeps the file as it is.")} />
        </span>
        <select value={noUpgrade ? 'leave' : 'look'} onChange={(e) => void chooseNoUpgrade(e.target.value === 'leave')}>
          <option value="look">{t('Look for better versions')}</option>
          <option value="leave">{t('Leave what I have alone')}</option>
        </select>
      </label>
      {folders.length > 1 && (
        <label>
          <span className="has-tip">
            {t('Keep it in')}
            <InfoTip text={t('The library folder new files go to. Files that are already there stay where they are.')} />
          </span>
          <select value={root} onChange={(e) => void chooseFolder(e.target.value)}>
            <option value="">{t('{folder} (main)', { folder: folders[0] })}</option>
            {folders.slice(1).map((f) => (
              <option key={f} value={f}>
                {f}
              </option>
            ))}
          </select>
        </label>
      )}
      {children}
    </div>
  )
}
