import { useEffect, useState } from 'react'
import { Link, useParams } from 'react-router-dom'
import { api, type AppLink } from '../api'
import Icon from '../components/Icon'
import { useDocumentTitle } from '../documentTitle'
import { t } from '../i18n'

// An add-on shown inside Mediarium: the program's own page in a frame.
export default function AppFrame() {
  const { id } = useParams()
  const [app, setApp] = useState<AppLink | null | undefined>(undefined)
  useEffect(() => {
    api
      .listApps()
      .then((l) => setApp(l.find((a) => a.id === id && a.enabled) ?? null))
      .catch(() => setApp(null))
  }, [id])
  useDocumentTitle(app ? app.name : t('Add-ons'))
  if (app === undefined) return null
  if (!app) return <p>{t('That add-on is not here any more.')}</p>
  return (
    <div className="app-frame">
      <div className="page-header">
        <h1>{app.name}</h1>
        <a className="btn-with-icon" href={app.url} target="_blank" rel="noopener noreferrer">
          <Icon name="external" size={15} /> {t('Open in a new tab')}
        </a>
      </div>
      <iframe
        className="app-frame-view"
        src={app.url}
        title={app.name}
        referrerPolicy="no-referrer"
        sandbox="allow-scripts allow-forms allow-same-origin allow-popups allow-downloads"
      />
      <p className="field-hint">
        {t('If this stays empty, the program does not allow being shown in a frame.')} <Link to="/settings/addons">{t('Add-ons')}</Link>
      </p>
    </div>
  )
}
