import { useEffect, useState } from 'react'
import { api } from '../api'
import CopyBox from './CopyBox'
import Icon from './Icon'
import { t, tx } from '../i18n'

interface Library {
  label: string
  path: string
}

interface Result {
  label: string
  sameFilesystem: boolean
  supported: boolean
  error?: string
}

// Tells the person up front whether finished downloads can be moved into the
// library instantly (same filesystem) or have to be copied (different ones).
// Copying works, it just uses more disk space for a while, so this is a calm
// note and not an alarm. Used in the setup wizard and in Settings.
//
// Either pass one library folder (pathA) and the downloads folder (pathB), or
// several `libraries` to compare against the downloads folder at once.
export default function HardlinkWarning({
  pathA,
  pathB,
  libraries,
  downloads,
}: {
  pathA?: string
  pathB?: string
  libraries?: Library[]
  downloads?: string
}) {
  const list: Library[] = libraries ?? [{ label: t('library'), path: pathA ?? '' }]
  const dl = downloads ?? pathB ?? ''
  const key = JSON.stringify([list, dl])
  const [results, setResults] = useState<Result[] | null>(null)

  useEffect(() => {
    const wanted = list.filter((l) => l.path.trim())
    if (!dl.trim() || wanted.length === 0) {
      setResults(null)
      return
    }
    let stale = false
    const timeout = setTimeout(async () => {
      // Debounced while the person is still typing.
      const out = await Promise.all(
        wanted.map(async (l): Promise<Result> => {
          try {
            const r = await api.filesystemCheck(l.path.trim(), dl.trim())
            return { label: l.label, ...r }
          } catch {
            return { label: l.label, sameFilesystem: false, supported: true, error: 'no answer' }
          }
        }),
      )
      if (!stale) setResults(out)
    }, 400)
    return () => {
      stale = true
      clearTimeout(timeout)
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [key])

  if (!results) return null

  if (results.every((r) => !r.supported)) {
    return (
      <p className="fbox-calm">
        {t("Can't tell if these folders share a drive here. If they do, finished downloads move instantly. If not, they're copied.")}
      </p>
    )
  }
  const checked = results.filter((r) => r.supported && !r.error)
  if (checked.length === 0) {
    return <p className="fbox-calm">{t("Couldn't check the folders yet. That's normal if one doesn't exist inside the container yet.")}</p>
  }

  const copying = checked.filter((r) => !r.sameFilesystem)
  if (copying.length === 0) {
    return <p className="ok-line">✓ {t('Same filesystem. Hardlinking will work.')}</p>
  }

  const names = copying.map((r) => r.label)
  const what = names.length === 1 ? names[0] : t('{list} and {last}', { list: names.slice(0, -1).join(', '), last: names[names.length - 1] })
  return (
    <div className="callout info" role="note">
      <div className="callout-head">
        <Icon name="info" size={16} />
        <strong>{t('Finished downloads will be copied, not moved.')}</strong>
      </div>
      <p>
        {t('Your downloads folder is on a different drive or share than your {what}, so each file is copied into the library instead of moved. It works, just a bit slower.', { what })}
      </p>
      <ul>
        <li>
          {tx('{usenet} the download is deleted after the copy, so you only lose a little time.', { usenet: <strong>Usenet:</strong> })}
        </li>
        <li>
          {tx('{torrents} the file stays in Downloads while it seeds, so it takes twice the space until it is done seeding.', { torrents: <strong>{t('Torrents:')}</strong> })}
        </li>
      </ul>
      <details>
        <summary>{t('How to make it instant')}</summary>
        <p>
          {tx('Keep your downloads and your library inside one shared folder, and map only that folder. For example, on a Synology map {host} as {data}. Inside it you have Movies, tv and downloads, and Mediarium sees them as {movies}, {tv} and {downloads}. The line for your compose file:', {
            host: <code>/volume1/Media</code>,
            data: <code>/data</code>,
            movies: <code>/data/Movies</code>,
            tv: <code>/data/tv</code>,
            downloads: <code>/data/downloads</code>,
          })}
        </p>
        <CopyBox text="- /volume1/Media:/data" label={t('Compose line for one shared folder')} />
        <p>{t('Then type those three paths in the boxes above.')}</p>
      </details>
    </div>
  )
}
