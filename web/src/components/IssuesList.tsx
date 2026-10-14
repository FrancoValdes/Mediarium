import { useCallback, useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { api, isAdmin, type Issue } from '../api'
import { useAuth } from '../AuthContext'
import { timeAgo } from '../format'
import { t, st } from '../i18n'
import { useLive } from '../useLive'
import { useConfirm } from './ConfirmProvider'
import Icon from './Icon'
import { PosterFallback } from './PosterCard'
import { ISSUE_CATEGORY } from './issueCategories'
import { useToast } from './Toast'
import './RequestPortal.css'

// Activity > Reports: problems people reported with a title. Administrators
// see everyone's and mark them resolved, with a comment the person is told;
// anyone else sees their own.

export default function IssuesList({ onCount }: { onCount?: (open: number) => void }) {
  const toast = useToast()
  const confirm = useConfirm()
  const admin = isAdmin(useAuth().user)
  const [list, setList] = useState<Issue[] | null>(null)
  const [answering, setAnswering] = useState(0)
  const [comment, setComment] = useState('')
  const [busy, setBusy] = useState(0)

  const load = useCallback(() => {
    api
      .listIssues()
      .then((r) => {
        setList(r.issues)
        onCount?.(r.open)
      })
      .catch(() => setList((cur) => cur ?? []))
  }, [onCount])
  useEffect(load, [load])
  useLive(load, 30000)

  async function act(i: Issue, fn: () => Promise<unknown>, done: string) {
    setBusy(i.id)
    try {
      await fn()
      toast.success(done)
      load()
    } catch (e) {
      toast.error(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(0)
    }
  }

  if (list === null) return <div className="skeleton" style={{ height: 120 }} />
  if (list.length === 0)
    return (
      <div className="empty-state">
        <Icon name="bug" size={30} />
        <p>
          {admin
            ? t('No reports. When someone reports a problem with a movie or show, it shows up here.')
            : t("You haven't reported anything. Use the Report a problem button on a movie or show page.")}
        </p>
      </div>
    )
  return (
    <div className="issue-list">
      {list.map((i) => (
        <div key={i.id} className={`issue-row st-${i.status}`}>
          <div className="qthumb">{i.poster ? <img src={i.poster} alt="" loading="lazy" /> : <PosterFallback />}</div>
          <div className="wmain">
            <strong>
              <Link to={i.kind === 'movie' ? `/title/${i.tmdbId}` : `/series/${i.itemId}`}>
                {i.title}
                {i.year ? ` (${i.year})` : ''}
              </Link>
            </strong>
            <small>
              {[ISSUE_CATEGORY[i.category], i.episode, admin && i.reporter ? t('reported by {name}', { name: i.reporter }) : '', timeAgo(i.createdAt)].filter(Boolean).join(' · ')}
            </small>
            {i.message && <p className="issue-text">{st(i.message)}</p>}
            {i.comment && (
              <p className="issue-answer">
                <strong>{t('Answer')}:</strong> {i.comment}
              </p>
            )}
          </div>
          <div className="row-actions">
            <span className={`state-tag st-${i.status === 'open' ? 'pending' : 'added'}`}>{i.status === 'open' ? t('Open') : t('Resolved')}</span>
            {admin && i.status === 'open' && (
              <button className="primary btn-sm btn-with-icon" disabled={busy === i.id} onClick={() => setAnswering(answering === i.id ? 0 : i.id)}>
                <Icon name="check" size={14} /> {t('Resolve')}
              </button>
            )}
            {admin && i.status === 'resolved' && (
              <button className="btn-sm" disabled={busy === i.id} onClick={() => void act(i, () => api.reopenIssue(i.id), t('Report reopened.'))}>
                {t('Reopen')}
              </button>
            )}
            {admin && (
              <button
                className="icon-btn"
                title={t('Remove the report')}
                aria-label={t('Remove the report')}
                disabled={busy === i.id}
                onClick={async () => {
                  const ok = await confirm({ title: t('Remove this report?'), body: <p>{t('It is deleted from the list. The person is not told.')}</p>, confirmLabel: t('Remove'), danger: true })
                  if (ok) void act(i, () => api.deleteIssue(i.id), t('Report removed.'))
                }}
              >
                <Icon name="trash" size={15} />
              </button>
            )}
          </div>
          {answering === i.id && (
            <div className="issue-reply">
              <input value={comment} onChange={(e) => setComment(e.target.value)} placeholder={t('What did you do? (optional, they will see it)')} maxLength={500} />
              <button
                className="primary btn-sm"
                disabled={busy === i.id}
                onClick={() =>
                  void act(i, () => api.resolveIssue(i.id, comment), t('Report marked as resolved.')).then(() => {
                    setAnswering(0)
                    setComment('')
                  })
                }
              >
                {t('Mark as resolved')}
              </button>
              <button className="btn-sm" onClick={() => setAnswering(0)}>
                {t('Cancel')}
              </button>
            </div>
          )}
        </div>
      ))}
    </div>
  )
}
