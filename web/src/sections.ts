import type { IconName } from './components/Icon'
import { t } from './i18n.ts'

export type SectionKey = 'dashboard' | 'discover' | 'library' | 'wanted' | 'calendar' | 'activity' | 'settings' | 'profile' | 'about'

export const SECTIONS: Record<SectionKey, { label: string; icon: IconName }> = {
  dashboard: { label: t('Dashboard'), icon: 'grid' },
  discover: { label: t('Discover'), icon: 'compass' },
  library: { label: t('Library'), icon: 'film' },
  wanted: { label: t('Wanted'), icon: 'bookmark' },
  calendar: { label: t('Calendar'), icon: 'calendar' },
  activity: { label: t('Activity'), icon: 'activity' },
  settings: { label: t('Settings'), icon: 'sliders' },
  profile: { label: t('Profile'), icon: 'user' },
  about: { label: t('About and credits'), icon: 'info' },
}

// Which colour family a URL belongs to.
export function sectionFor(pathname: string): SectionKey {
  if (pathname === '/') return 'dashboard'
  if (pathname.startsWith('/discover') || pathname.startsWith('/search') || pathname.startsWith('/title') || pathname.startsWith('/books/work')) return 'discover'
  if (pathname.startsWith('/library') || pathname.startsWith('/series') || pathname.startsWith('/music') || pathname.startsWith('/book/') || pathname.startsWith('/import')) return 'library'
  if (pathname.startsWith('/wanted')) return 'wanted'
  if (pathname.startsWith('/calendar')) return 'calendar'
  if (pathname.startsWith('/queue')) return 'activity'
  if (pathname.startsWith('/settings')) return 'settings'
  if (pathname.startsWith('/profile')) return 'profile'
  if (pathname.startsWith('/about') || pathname.startsWith('/help')) return 'about'
  return 'dashboard'
}
