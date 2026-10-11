import { useCallback, useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { api, isAdmin, type TitleEvent, type WaitingRelease } from '../api'
import { useAuth } from '../AuthContext'
import { timeAgo } from '../format'
import { untilText } from '../delayText'
import { useLive } from '../useLive'
import { t } from '../i18n'
import Icon from './Icon'

// For a title still waiting for a release: what the last search found and
// why nothing was taken, in plain words, with what can be done about it.
export default function SearchStatus({ kind, id }: { kind: 'movie' | 'series' | 'album'; id: number }) {
  const { user } = useAuth()
  const [last, setLast] = useState<TitleEvent | null>(null)
  // Releases held back by the delay profile for this title.
  const [held, setHeld] = useState<WaitingRelease[]>([])

  const load = useCallback(() => {
    ;(kind === 'movie' ? api.movieEvents(id) : kind === 'album' ? api.albumEvents(id) : api.seriesEvents(id))
      .then((ev) => setLast(ev.find((e) => e.kind === 'searched') ?? null))
      .catch(() => setLast(null))
    if (kind !== 'album')
      api
        .searchWaiting()
        .then((all) => setHeld(all.filter((w) => (kind === 'movie' ? w.movieId === id : w.seriesId === id))))
        .catch(() => setHeld([]))
  }, [kind, id])
  useEffect(load, [load])
  useLive(load, 10000)

  const soonest = held.length > 0 ? held.reduce((a, b) => (a.until <= b.until ? a : b)) : null
  if (!last && !soonest) return null
  if (!last && soonest) {
    return (
      <div className="search-status waiting">
        <Icon name="clock" size={16} />
        <div>
          <strong>{t('Waiting for a better protocol until {time}', { time: untilText(soonest.until) })}</strong>
          <p>{t('A release was found and is held back so a copy from the better protocol has time to turn up. It is taken when the wait is over.')}</p>
        </div>
      </div>
    )
  }
  if (!last) return null
  const nothing = last.level !== 'info'
  const cinema = /CAM\/TeleSync/.test(last.message)
  return (
    <div className={`search-status${nothing ? ' waiting' : ''}`}>
      <Icon name={nothing ? 'clock' : 'check'} size={16} />
      <div>
        <strong>{nothing ? t('Waiting for a release') : t('Last search')}</strong> <small>· {timeAgo(last.at)}</small>
        <p>{last.message.replace(/^Searched: /, '')}</p>
        {soonest && (
          <p className="search-status-hint">
            <strong>{t('Waiting for a better protocol until {time}', { time: untilText(soonest.until) })}</strong> {t('A release was found and is held back. It is taken when the wait is over.')}
          </p>
        )}
        {nothing && (
          <p className="search-status-hint">
            {t('Mediarium keeps looking and grabs it when a release you accept turns up.')}
            {cinema && ' ' + t("Only cinema recordings exist so far, which your quality profile doesn't accept.")}
            {isAdmin(user) && (
              <>
                {' '}
                <Link to="/settings/quality">{t('Choose other qualities to fall back to')}</Link>
              </>
            )}
          </p>
        )}
      </div>
    </div>
  )
}
