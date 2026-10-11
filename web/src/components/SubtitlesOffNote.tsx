import { Link } from 'react-router-dom'
import { isAdmin } from '../api'
import { useAuth } from '../AuthContext'
import { t, tx } from '../i18n'

// The one quiet line title pages show instead of the subtitle tools while
// subtitles are switched off. Only administrators can change the switch, so
// only they get the link.
export default function SubtitlesOffNote() {
  const admin = isAdmin(useAuth().user)
  return (
    <p style={{ color: 'var(--text-dim)', fontSize: '0.9rem', margin: '0 0 24px' }}>
      {t('Subtitles are off.')}
      {admin && (
        <>
          {' '}
          {tx('Turn them on in {settings}.', { settings: <Link to="/settings/subtitles">{t('Settings')}</Link> })}
        </>
      )}
    </p>
  )
}
