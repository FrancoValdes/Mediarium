import { Link } from 'react-router-dom'
import { releaseHint, type Unanswered } from '../releaseHint'
import { t } from '../i18n'

// The line about the indexers next to a release list: says why there is
// nothing to show, or that some sources did not answer.
export default function ReleaseNotice({ sources, unanswered, results, className }: { sources: number; unanswered: Unanswered[]; results: number; className?: string }) {
  const h = releaseHint(sources, unanswered, results)
  if (!h) return null
  return (
    <p className={className} style={{ color: 'var(--text-dim)' }} role={h.empty ? undefined : 'status'}>
      {h.text}
      {h.link && (
        <>
          {' '}
          <Link to="/settings/indexers">{t('Settings > Indexers & Search')}</Link>.
        </>
      )}
    </p>
  )
}
