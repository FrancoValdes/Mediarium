import type { IssueCategory } from '../api'
import { t } from '../i18n'

// What a problem report can be about, in the words shown for each.
export const ISSUE_CATEGORY: Record<IssueCategory, string> = {
  'wrong-file': t('Wrong file'),
  'no-sound': t('No sound or wrong audio'),
  'bad-subtitles': t('Bad subtitles'),
  'bad-picture': t('Bad picture'),
  other: t('Something else'),
}
