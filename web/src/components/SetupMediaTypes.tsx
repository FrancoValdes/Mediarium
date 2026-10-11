import Icon from './Icon'
import KindGlyph, { type GlyphKind } from './KindGlyph'
import type { Chosen } from '../setupHelpers'
import { t } from '../i18n'

type Key = keyof Chosen

const CARDS: { key: Key; title: string; glyph: GlyphKind; text: string; soon?: boolean }[] = [
  { key: 'movies', title: t('Movies'), glyph: 'movie', text: t('Find and download movies, and file them neatly in your library.') },
  { key: 'tv', title: t('TV shows'), glyph: 'tv', text: t('Follow shows and get new episodes when they air.') },
  { key: 'music', title: t('Music'), glyph: 'music', text: t('Keep your artists and albums in order.') },
  { key: 'ebooks', title: t('Ebooks'), glyph: 'book', text: t('Books for your e-reader, EPUB first.') },
  { key: 'audiobooks', title: t('Audiobooks'), glyph: 'audiobook', text: t('Audiobooks, filed by author, ready for your player.') },
]

// "What do you want to manage?": one card per kind of media, each one
// switched on or off.
export default function SetupMediaTypes({ chosen, onToggle, lastOneHint }: { chosen: Chosen; onToggle: (key: Key) => void; lastOneHint: boolean }) {
  return (
    <div className="media-cards">
      {CARDS.map((c) => {
        const on = !c.soon && !!chosen[c.key]
        return (
          <button
            key={c.key}
            type="button"
            role="checkbox"
            aria-checked={on}
            aria-disabled={c.soon || undefined}
            disabled={c.soon}
            className={`choice-card media-card k-${c.glyph}${on ? ' active' : ''}${c.soon ? ' soon' : ''}`}
            onClick={() => !c.soon && onToggle(c.key)}
          >
            <span className="choice-box" aria-hidden="true">
              {on && <Icon name="check" size={13} />}
            </span>
            <span className="media-card-ico" aria-hidden="true">
              <KindGlyph kind={c.glyph} size={34} variant="solid" />
            </span>
            <strong>
              {c.title}
              {c.soon && <span className="fbox-tag">{t('Coming soon')}</span>}
            </strong>
            <small>{c.text}</small>
          </button>
        )
      })}
      {lastOneHint && <p className="media-cards-note">{t('At least one has to stay on.')}</p>}
    </div>
  )
}
