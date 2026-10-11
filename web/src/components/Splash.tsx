import AmbientBackdrop from './AmbientBackdrop'
import LogoGlyph from './LogoGlyph'
import { t } from '../i18n'

// Full-screen loading screen: the mark, the name, and an animated "Loading".
// The same markup is inlined in index.html so it shows before the app's code
// has even arrived; keep the two in step.
export default function Splash({ label = t('Loading'), hint, onRetry }: { label?: string; hint?: string; onRetry?: () => void }) {
  return (
    <div className="splash" role="status" aria-live="polite">
      <AmbientBackdrop level="full" />
      <div className="splash-mark">
        <LogoGlyph size={96} />
      </div>
      <div className="splash-name">Mediarium</div>
      <div className="splash-label">
        {label}
        <span className="splash-dots" aria-hidden="true">
          <i>.</i>
          <i>.</i>
          <i>.</i>
        </span>
      </div>
      {hint && <p className="splash-hint">{hint}</p>}
      {onRetry && (
        <button type="button" className="primary" onClick={onRetry}>
          {t('Try again')}
        </button>
      )}
    </div>
  )
}
