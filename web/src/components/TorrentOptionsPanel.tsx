import { useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { api, type TorrentCategory, type TorrentOptions } from '../api'
import { useLive } from '../useLive'
import { t } from '../i18n'
import Switch from './Switch'
import { useToast } from './Toast'
import '../torrentcontrols.css'
import TipLabel from './TipLabel'

const KINDS: ['movie' | 'tv' | 'music' | 'book', string][] = [
  ['movie', t('Movies')],
  ['tv', t('TV shows')],
  ['music', t('Music')],
  ['book', t('Books')],
]

const MAX_CATEGORIES = 12

// What the router did with the listen port, in words.
function mapText(o: TorrentOptions, listenPort: number): string {
  const m = o.portMapping
  switch (m.state) {
    case 'off':
      return t('Not asking the router to open the port.')
    case 'tunnel':
      return t('Not used while a VPN or proxy carries the torrent traffic.')
    case 'idle':
      return t('Checked when the first torrent starts.')
    case 'working':
      return t('Looking for the router…')
    case 'mapped':
      return t('Open: the router forwards port {port} ({method}).', { port: m.port ?? listenPort, method: m.method ?? '' })
    default:
      return t('Not mapped: no router opened port {port}. If peers cannot reach you, forward it on the router yourself.', { port: listenPort })
  }
}

// The options of the built-in torrent client: the result of opening the port
// on the router, the defaults for new torrents, the upload limit and the
// categories torrents are filed under. The options live in Settings >
// Downloading > Torrents ('settings'); the Download clients tab in Activity
// only shows the router port and links there ('activity').
export default function TorrentOptionsPanel({ listenPort, variant = 'activity' }: { listenPort: number; variant?: 'activity' | 'settings' }) {
  const toast = useToast()
  const [opts, setOpts] = useState<TorrentOptions | null>(null)
  const [upload, setUpload] = useState('')
  const [cats, setCats] = useState<TorrentCategory[]>([])
  const [byKind, setByKind] = useState<TorrentOptions['categoryByKind']>({})
  const [busy, setBusy] = useState(false)

  const adopt = (o: TorrentOptions) => {
    setOpts(o)
    setUpload(o.uploadLimitKB > 0 ? String(o.uploadLimitKB) : '')
    setCats(o.categories)
    setByKind(o.categoryByKind)
  }
  useEffect(() => {
    api.torrentOptions().then(adopt).catch(() => undefined)
  }, [])
  // The router answer changes while torrents start and stop; nothing else is replaced.
  useLive(() => api.torrentOptions().then((o) => setOpts((cur) => (cur ? { ...cur, portMapping: o.portMapping } : o))).catch(() => undefined), 10000)

  if (!opts) return null

  async function save(patch: Parameters<typeof api.setTorrentOptions>[0], done: string) {
    setBusy(true)
    try {
      adopt(await api.setTorrentOptions(patch))
      toast.success(done)
    } catch (e) {
      toast.error(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(false)
    }
  }

  const uploadNumber = Number(upload.trim().replace(',', '.'))
  const uploadValue = upload.trim() === '' ? 0 : Math.round(uploadNumber)
  const uploadBad = upload.trim() !== '' && (!Number.isFinite(uploadNumber) || uploadNumber < 0)
  const catsChanged = JSON.stringify(cats) !== JSON.stringify(opts.categories) || JSON.stringify(byKind) !== JSON.stringify(opts.categoryByKind)

  return (
    <div className="torrent-options" style={variant === 'settings' ? { borderTop: 0, marginTop: 0, paddingTop: 0 } : undefined}>
      <div className="to-map">
        <b><TipLabel text={t('Whether your router passed the torrent port on to this machine (UPnP or NAT-PMP), so peers can reach you. Not needed to download, but it helps seeding.')} learnMore="downloads#opening-the-port-on-your-router">{t('Router port')}</TipLabel></b>
        <span className={`badge ${opts.portMapping.state === 'mapped' ? 'downloaded' : opts.portMapping.state === 'none' ? 'missing' : ''}`}>
          {opts.portMapping.state === 'mapped'
            ? t('open')
            : opts.portMapping.state === 'none'
              ? t('not mapped')
              : opts.portMapping.state === 'off' || opts.portMapping.state === 'tunnel'
                ? t('not used')
                : opts.portMapping.state === 'working'
                  ? t('checking')
                  : t('idle')}
        </span>
        <span className="hint">{mapText(opts, listenPort)}</span>
      </div>
      {variant === 'activity' && (
        <Link className="hint" to="/settings/torrents">
          {t('More torrent options in Settings')}
        </Link>
      )}
      {variant === 'settings' && (
        <div className="torrent-options" style={{ borderTop: 0, marginTop: 0, paddingTop: 0 }}>
          <Switch
            checked={opts.portMapping.enabled}
            disabled={busy}
            onChange={(on) => void save({ portMapping: on }, on ? t('Mediarium will ask the router to open the port.') : t('Mediarium will not ask the router.'))}
            label={t('Open the port on my router')}
            tip={t("You can skip it: torrents download without it. An open port finds more peers and lets you seed properly. In Docker's default network no router is usually found.")}
            learnMore="downloads#opening-the-port-on-your-router"
            description={t('Asks the router with UPnP, then NAT-PMP, so peers can connect to you. It is never used with a VPN or proxy.')}
          />
          <Switch
            checked={opts.allowLocalAddresses}
            disabled={busy}
            onChange={(on) => void save({ allowLocalAddresses: on }, t('Saved. It applies when Mediarium restarts.'))}
            label={t('Let torrents use trackers on my own network')}
            description={t('Off by default: a torrent can name any tracker or web seed, and one on your home network or this computer could be used to reach things there. Turn it on only if you run a tracker or web seed at home. Applies when Mediarium restarts.')}
          />
          <Switch
            checked={opts.sequentialDefault}
            disabled={busy}
            onChange={(on) => void save({ sequentialDefault: on }, t('Saved.'))}
            label={t('Download new torrents in order')}
            tip={t('A little slower than the usual order and harder on the peers that share it, so leave it off unless you want to watch while it downloads.')}
            learnMore="downloads#download-in-order"
            description={t('First piece to last, so a video can be played while it downloads. Each torrent can choose for itself under Details.')}
          />
          <Switch
            checked={opts.skipJunk}
            disabled={busy}
            onChange={(on) => void save({ skipJunk: on }, t('Saved.'))}
            label={t('Leave out samples and notes')}
            tip={t('You can still tick any left-out file under Details while the torrent downloads. A torrent that is only junk is downloaded as it is.')}
            learnMore="downloads#choosing-files"
            description={t('Sample clips and .nfo, .txt, .exe and .url files are not downloaded.')}
          />
          <Switch
            checked={opts.packOnlyWanted}
            disabled={busy}
            onChange={(on) => void save({ packOnlyWanted: on }, t('Saved.'))}
            label={t('Only the wanted season of a pack')}
            description={t('A pack with several seasons downloads only the season Mediarium was looking for. Off downloads the whole pack.')}
          />
          <form
            className="to-limit"
            onSubmit={(e) => {
              e.preventDefault()
              if (!uploadBad) void save({ uploadLimitKB: uploadValue }, uploadValue > 0 ? t('Upload limit saved.') : t('Upload is no longer limited.'))
            }}
          >
            <label>
              <TipLabel text={t('Caps how fast the built-in client uploads, for all torrents together. Empty means no limit.')} learnMore="downloads#speed-limits">{t('Upload limit (KB/s)')}</TipLabel>
              <input inputMode="numeric" value={upload} placeholder={t('No limit')} onChange={(e) => setUpload(e.target.value)} aria-invalid={uploadBad} />
            </label>
            <button className="btn-sm" type="submit" disabled={busy || uploadBad || uploadValue === opts.uploadLimitKB}>
              {t('Save')}
            </button>
            <small className="hint">{t('Applies in the same hours as the download limit in Settings. Each torrent can have its own limits under Details.')}</small>
          </form>
          <div className="to-cats">
            <b>{t('Categories')}</b>
            <p className="hint">{t('A category files torrents in a folder of their own inside the downloads working folder. Pick which category each kind of download gets. With none picked, nothing changes.')}</p>
            {cats.map((c, i) => (
              <div className="to-cat-row" key={i}>
                <label>
                  <small>{t('Name')}</small>
                  <input value={c.name} maxLength={40} onChange={(e) => setCats(cats.map((x, j) => (j === i ? { ...x, name: e.target.value } : x)))} />
                </label>
                <label>
                  <small className="has-tip"><TipLabel text={t('A plain folder name inside the downloads working folder. Leave it empty to use the working folder itself.')} learnMore="downloads#categories-and-save-folders">{t('Folder (optional)')}</TipLabel></small>
                  <input value={c.subfolder} maxLength={60} spellCheck={false} onChange={(e) => setCats(cats.map((x, j) => (j === i ? { ...x, subfolder: e.target.value } : x)))} />
                </label>
                <button
                  className="btn-sm"
                  onClick={() => {
                    const gone = cats[i].name
                    setCats(cats.filter((_, j) => j !== i))
                    setByKind(Object.fromEntries(Object.entries(byKind).filter(([, v]) => v !== gone)))
                  }}
                >
                  {t('Remove')}
                </button>
              </div>
            ))}
            {cats.length < MAX_CATEGORIES && (
              <div className="to-actions">
                <button className="btn-sm" onClick={() => setCats([...cats, { name: '', subfolder: '' }])}>
                  {t('Add a category')}
                </button>
              </div>
            )}
            {cats.length > 0 && (
              <div className="to-kinds">
                {KINDS.map(([kind, label]) => (
                  <label key={kind}>
                    <small>{label}</small>
                    <select value={byKind[kind] ?? ''} onChange={(e) => setByKind({ ...byKind, [kind]: e.target.value })}>
                      <option value="">{t('No category')}</option>
                      {cats
                        .filter((c) => c.name.trim() !== '')
                        .map((c) => (
                          <option key={c.name} value={c.name}>
                            {c.name}
                          </option>
                        ))}
                    </select>
                  </label>
                ))}
              </div>
            )}
            <div className="to-actions">
              <button className="btn-sm" disabled={busy || !catsChanged} onClick={() => void save({ categories: cats, categoryByKind: byKind }, t('Categories saved.'))}>
                {t('Save categories')}
              </button>
            </div>
          </div>
        </div>
      )}
    </div>
  )
}
