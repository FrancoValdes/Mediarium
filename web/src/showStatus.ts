import { t } from './i18n.ts'

/** showStatusLabel is where a show stands, in the screen language. */
export function showStatusLabel(status?: string): string {
  switch (status) {
    case 'ended':
      return t('Ended')
    case 'continuing':
      return t('Continuing')
    case 'upcoming':
      return t('Upcoming')
    default:
      return ''
  }
}
