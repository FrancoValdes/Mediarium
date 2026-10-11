import { Link } from 'react-router-dom'
import type { SubtitleQuota } from '../api'
import { isAdmin } from '../api'
import { useAuth } from '../AuthContext'
import { noSourceInQuota } from '../subtitleSources'
import { t } from '../i18n'

// Says why the subtitle buttons can't work, with a way to fix it.
export default function NoSourceNote({ quota }: { quota: SubtitleQuota | null }) {
  const admin = isAdmin(useAuth().user)
  if (!noSourceInQuota(quota)) return null
  return (
    <div className="notice notice-warn" role="status">
      <strong>{t('No subtitle source is switched on.')}</strong>{' '}
      {quota?.sourcesSetUp ? t('Subtitles cannot be searched until you switch one on.') : t('Add a key for OpenSubtitles or SubDL, or an add-on, to search for subtitles.')}{' '}
      {admin ? <Link to="/settings/subtitles">{t('Choose sources')}</Link> : t('An administrator can do this in Settings.')}
    </div>
  )
}
