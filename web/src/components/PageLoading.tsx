import BrandMark from './BrandMark'
import { t } from '../i18n'

// What a page shows while its first answer is on the way: the Mediarium mark
// breathing inside a ring, so a slow look-up (a few seconds when the music or
// book database is busy) reads as "working" and not as a broken, empty page.
export default function PageLoading({ label }: { label?: string }) {
  return (
    <div className="page-loading" role="status" aria-live="polite">
      <span className="page-loading-mark">
        <BrandMark />
      </span>
      <span className="page-loading-text">{label ?? t('Loading')}</span>
    </div>
  )
}
