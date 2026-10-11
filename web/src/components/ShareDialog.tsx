import { createPortal } from 'react-dom'
import { useEffect, useRef, useState } from 'react'
import { COMMUNITIES, SHARE_IMAGES, sharePlaces, type SharePlace } from '../share'
import { useFocusTrap } from '../useFocusTrap'
import { t } from '../i18n'
import Icon from './Icon'
import { useToast } from './Toast'

// "Tell others about Mediarium": pick where, copy the ready-made text,
// download a picture, and open the site's own post page.
export default function ShareDialog({ onClose }: { onClose: () => void }) {
  const places = sharePlaces()
  const [placeId, setPlaceId] = useState<SharePlace['id']>('reddit')
  const place = places.find((p) => p.id === placeId) ?? places[0]
  const toast = useToast()
  const box = useRef<HTMLDivElement>(null)
  useFocusTrap(box)
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => e.key === 'Escape' && onClose()
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [onClose])

  async function copy(text: string) {
    try {
      await navigator.clipboard.writeText(text)
      toast.success(t('Copied.'))
    } catch {
      toast.error(t("Couldn't copy. Select the text and copy it by hand."))
    }
  }

  const image = SHARE_IMAGES.find((i) => i.id === place.image) ?? SHARE_IMAGES[0]

  return createPortal(
    <div className="modal-backdrop" onMouseDown={(e) => e.target === e.currentTarget && onClose()}>
      <div ref={box} tabIndex={-1} className="modal share-dialog" role="dialog" aria-modal="true" aria-label={t('Share Mediarium')}>
        <div className="share-head">
          <span className="share-ico">
            <Icon name="share" size={20} />
          </span>
          <div>
            <h2>{t('Share Mediarium')}</h2>
            <p>{t('A post from someone who uses it is the best way for others to find it. Thank you!')}</p>
          </div>
          <button className="icon-btn" onClick={onClose} aria-label={t('Close')}>
            <Icon name="x" size={18} />
          </button>
        </div>

        <div className="share-places" role="tablist" aria-label={t('Where to share')}>
          {places.map((p) => (
            <button key={p.id} role="tab" aria-selected={p.id === placeId} className={p.id === placeId ? 'active' : ''} onClick={() => setPlaceId(p.id)}>
              {p.name}
            </button>
          ))}
        </div>

        <div className="share-body">
          <div className="share-text">
            {place.title && (
              <label>
                {t('Title')}
                <div className="share-copy-row">
                  <input readOnly value={place.title} onFocus={(e) => e.currentTarget.select()} />
                  <button onClick={() => void copy(place.title ?? '')} aria-label={t('Copy the title')}>
                    <Icon name="copy" size={15} />
                  </button>
                </div>
              </label>
            )}
            <label>
              {t('Text')}
              <textarea readOnly rows={place.text.length > 200 ? 7 : 4} value={place.text} onFocus={(e) => e.currentTarget.select()} />
            </label>
            <div className="share-actions">
              <button className="primary btn-with-icon" onClick={() => void copy(place.text)}>
                <Icon name="copy" size={15} /> {t('Copy the text')}
              </button>
              {place.open && (
                <a className="btn-link" href={place.open} target="_blank" rel="noreferrer">
                  <Icon name="external" size={15} /> {t('Open {place}', { place: place.name })}
                </a>
              )}
            </div>
            {place.id === 'reddit' || place.id === 'forum' ? (
              <p className="share-note">
                {t('Places where it fits:')} {COMMUNITIES.join(', ')}. {t('Check each community’s rules on self-promotion first.')}
              </p>
            ) : place.id === 'instagram' ? (
              <p className="share-note">{t('Instagram has no share page on the web: download the picture, then post it from your phone with the text.')}</p>
            ) : null}
          </div>

          <figure className="share-image">
            <img src={image.file} alt={t('A picture of Mediarium to post with the text')} />
            <figcaption>
              <span>{image.size}</span>
              <span className="share-image-links">
                {SHARE_IMAGES.map((i) => (
                  <a key={i.id} href={i.file} download className={i.id === image.id ? 'active' : ''}>
                    <Icon name="download" size={14} /> {i.size}
                  </a>
                ))}
              </span>
            </figcaption>
          </figure>
        </div>
      </div>
    </div>,
    document.body,
  )
}
