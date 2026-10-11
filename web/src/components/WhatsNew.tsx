import { useEffect, useRef, useState } from 'react'
import { createPortal } from 'react-dom'
import { REPO_URL } from '../docs'
import { t } from '../i18n'
import { useFocusTrap } from '../useFocusTrap'
import { notesToShow, SEEN_KEY, type ReleaseNotes } from '../whatsNew'
import Icon from './Icon'

function readSeen(): string | null {
  try {
    return localStorage.getItem(SEEN_KEY)
  } catch {
    return null
  }
}

function writeSeen(v: string) {
  try {
    localStorage.setItem(SEEN_KEY, v)
  } catch {
    // Private windows may refuse; the note then shows again next time.
  }
}

// After Mediarium updates, a short note with the new version's highlights,
// once in each browser. A browser that has never seen Mediarium before only
// remembers the version, so a fresh install doesn't greet anyone with it.
export default function WhatsNew({ version }: { version: string }) {
  const [notes, setNotes] = useState<ReleaseNotes[]>([])
  const box = useRef<HTMLDivElement>(null)

  useEffect(() => {
    if (!version) return
    const seen = readSeen()
    if (seen === null) {
      writeSeen(version)
      return
    }
    const due = notesToShow(seen, version)
    if (due.length > 0) setNotes(due)
    else if (seen !== version) writeSeen(version)
  }, [version])

  const close = () => {
    writeSeen(version)
    setNotes([])
  }

  useEffect(() => {
    if (notes.length === 0) return
    const onKey = (e: KeyboardEvent) => e.key === 'Escape' && close()
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  })

  if (notes.length === 0) return null
  return <WhatsNewDialog notes={notes} onClose={close} box={box} />
}

function WhatsNewDialog({ notes, onClose, box }: { notes: ReleaseNotes[]; onClose: () => void; box: React.RefObject<HTMLDivElement | null> }) {
  useFocusTrap(box)
  const newest = notes[0].version.replace(/\.0$/, '')
  return createPortal(
    <div className="modal-backdrop" onMouseDown={(e) => e.target === e.currentTarget && onClose()}>
      <div ref={box} tabIndex={-1} className="modal whats-new" role="dialog" aria-modal="true" aria-labelledby="whats-new-title">
        <div className="confirm-head">
          <span className="confirm-ico">
            <Icon name="sparkles" size={20} />
          </span>
          <h2 id="whats-new-title">{t("What's new in Mediarium {version}", { version: newest })}</h2>
        </div>
        <div className="confirm-body">
          {notes.map((n) => (
            <section key={n.version}>
              {notes.length > 1 && <h3>{n.version}</h3>}
              <ul>
                {n.items.map((item) => (
                  <li key={item}>{item}</li>
                ))}
              </ul>
            </section>
          ))}
          <p className="hint">
            <a href={`${REPO_URL}/blob/main/CHANGELOG.md`} target="_blank" rel="noreferrer">
              {t('See every change')}
            </a>
          </p>
        </div>
        <div className="modal-foot">
          <button className="primary" onClick={onClose} autoFocus>
            {t('Got it')}
          </button>
        </div>
      </div>
    </div>,
    document.body,
  )
}
