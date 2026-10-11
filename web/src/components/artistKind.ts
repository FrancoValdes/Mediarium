import { t } from '../i18n.ts'

// What kind of act MusicBrainz says an artist is, in plain words.
export function artistKind(type?: string): string {
  switch ((type ?? '').toLowerCase()) {
    case 'group':
      return t('Band')
    case 'person':
      return t('Solo artist')
    case 'orchestra':
      return t('Orchestra')
    case 'choir':
      return t('Choir')
    case 'character':
      return t('Character')
    default:
      return t('Artist')
  }
}
