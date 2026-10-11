import { useCallback, useEffect, useState } from 'react'
import { api, type Settings } from '../../api'
import ImportLists from '../../components/ImportLists'
import ServiceKeyCard from '../../components/ServiceKeyCard'
import { TRAKT_COPY } from '../../components/serviceCopy'

// Settings > Info, lists and subtitles > Import lists: the lists, and the
// Trakt Client ID that reading Trakt lists needs.
export default function ListsSettings() {
  const [s, setS] = useState<Settings | null>(null)
  const load = useCallback(() => {
    api.getSettings().then(setS).catch(() => undefined)
  }, [])
  useEffect(load, [load])
  return (
    <div className="settings-stack">
      <ImportLists />
      {s && <ServiceKeyCard service="trakt" {...TRAKT_COPY} builtIn={!!s.traktClientIdBuiltIn} configured={s.hasTraktClientId} onSaved={load} allowOverride usingOwnKey={!!s.traktUsingOwnKey} />}
    </div>
  )
}
