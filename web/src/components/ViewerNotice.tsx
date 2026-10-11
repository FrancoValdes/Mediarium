import { useRef } from 'react'
import { createPortal } from 'react-dom'
import { t } from '../i18n'
import { useFocusTrap } from '../useFocusTrap'

// What a viewer (an account that can only look) sees in place of an "add to
// library" form. The server refuses the add anyway; this says why up front.
export default function ViewerNotice({ onClose }: { onClose: () => void }) {
  const box = useRef<HTMLDivElement>(null)
  useFocusTrap(box)
  return createPortal(
    <div className="modal-backdrop" onMouseDown={(e) => e.target === e.currentTarget && onClose()}>
      <div ref={box} tabIndex={-1} className="modal" role="dialog" aria-modal="true" aria-label={t('This account can only look')}>
        <div className="modal-head">
          <h2 style={{ margin: 0 }}>{t('This account can only look')}</h2>
        </div>
        <div className="modal-body">
          <p style={{ marginTop: 0 }}>{t("You can browse, discover and watch, but not add titles or change anything. Ask an administrator if you'd like to add or ask for something.")}</p>
        </div>
        <div className="modal-foot">
          <button className="primary" onClick={onClose} autoFocus>
            {t('OK')}
          </button>
        </div>
      </div>
    </div>,
    document.body,
  )
}
