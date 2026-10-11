import { t } from '../i18n'
import Icon from './Icon'

// On a phone a long toolbar of filters would fill the screen, so the page
// folds it behind this button (a dot says some filter is on). On wider screens
// the button is hidden and the filters are simply there.
export default function FilterToggle({ open, onToggle, active }: { open: boolean; onToggle: () => void; active?: boolean }) {
  return (
    <button type="button" className={`filters-toggle${open ? ' open' : ''}`} aria-expanded={open} onClick={onToggle}>
      <Icon name="sliders" size={16} /> {t('Filters')}
      {active && <span className="filters-dot" aria-label={t('Some filters are on')} />}
      <Icon name="chevron-down" size={14} />
    </button>
  )
}
