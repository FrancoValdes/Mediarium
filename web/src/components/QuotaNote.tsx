import type { SubtitleQuota } from '../api'
import Icon from './Icon'
import { locale, t } from '../i18n'

// Says how many subtitle downloads are left today and warns, in plain words,
// when the subtitles waiting to be fetched will not fit in one day's allowance.
export default function QuotaNote({ quota }: { quota: SubtitleQuota | null }) {
  if (!quota || !quota.hasKey) return null
  const pct = quota.limit > 0 ? Math.min(100, Math.round((quota.used / quota.limit) * 100)) : 0
  const warn = quota.exceeded || (quota.missingFiles > quota.remaining && quota.missingFiles > 0)
  return (
    <div className={`quota-note${warn ? ' warn' : ''}`}>
      <div className="quota-head">
        <Icon name={warn ? 'warning' : 'info'} size={16} />
        <strong>
          {t('{left} of {limit} subtitle downloads left today', { left: quota.remaining, limit: quota.limit })}
        </strong>
        {quota.resetsAt && quota.used > 0 && <small>{t('refills {time}', { time: new Date(quota.resetsAt).toLocaleTimeString(locale(), { hour: '2-digit', minute: '2-digit' }) })}</small>}
      </div>
      <div className="bar" style={{ ['--tc' as string]: warn ? 'var(--warning)' : 'var(--c-rose)' }}>
        <span style={{ width: `${Math.max(3, pct)}%` }} />
      </div>
      {quota.message && <p>{quota.message}</p>}
    </div>
  )
}
