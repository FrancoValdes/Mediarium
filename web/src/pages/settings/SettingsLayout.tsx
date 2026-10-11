import { Outlet, useLocation } from 'react-router-dom'
import { isAdmin } from '../../api'
import { useAuth } from '../../AuthContext'
import TipLabel from '../../components/TipLabel'
import { t } from '../../i18n'
import { pageBlurb, pageHelp, pageParts } from '../../settingsNav'
import '../../settings.css'

// Holds the open settings page. Pages that share a sidebar entry are listed
// under it in the sidebar, and the top bar names the group and the page. On
// phones the top bar has no room for the name, so it is repeated here.
export default function SettingsLayout() {
  const { pathname } = useLocation()
  const { user } = useAuth()
  const admin = isAdmin(user)
  const parts = pageParts(pathname, admin)
  const blurb = pageBlurb(pathname, admin)
  const help = pageHelp(pathname, admin)
  return (
    <div className="settings-content">
      <h2 className="settings-phone-title">
        {parts.group && <small>{parts.group}</small>}
        {parts.page}
      </h2>
      {blurb && (
        <p className="settings-blurb">
          {help ? <TipLabel help={help} label={t('Open the user guide for this page')}>{blurb}</TipLabel> : blurb}
        </p>
      )}
      <Outlet />
    </div>
  )
}
