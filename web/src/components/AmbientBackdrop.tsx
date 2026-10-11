import KindGlyph, { type GlyphKind } from './KindGlyph'
import LogoGlyph from './LogoGlyph'

// The shapes that drift slowly behind the app: the five kinds of media and the
// Mediarium logo, all as solid objects. "quiet" sits behind pages (faint, so
// it never competes with content); "full" is for the screens that have
// nothing else going on: sign-in, loading, setting up. Decoration only.
type Item = { k: GlyphKind | 'logo'; x: string; y: string; s: number; dur: number; delay: number; far?: boolean; extra?: boolean }

const ITEMS: Item[] = [
  { k: 'logo', x: '7%', y: '14%', s: 96, dur: 26, delay: -4 },
  { k: 'movie', x: '78%', y: '9%', s: 70, dur: 22, delay: -9 },
  { k: 'music', x: '91%', y: '52%', s: 84, dur: 30, delay: -14 },
  { k: 'tv', x: '60%', y: '84%', s: 66, dur: 28, delay: -2, far: true },
  { k: 'book', x: '16%', y: '78%', s: 62, dur: 25, delay: -11, far: true },
  { k: 'audiobook', x: '40%', y: '6%', s: 52, dur: 32, delay: -7, far: true },
  { k: 'movie', x: '30%', y: '56%', s: 46, dur: 34, delay: -16, far: true, extra: true },
  { k: 'music', x: '66%', y: '38%', s: 44, dur: 29, delay: -5, far: true, extra: true },
  { k: 'tv', x: '4%', y: '46%', s: 50, dur: 27, delay: -19, far: true, extra: true },
  { k: 'book', x: '84%', y: '88%', s: 48, dur: 31, delay: -8, far: true, extra: true },
]

export default function AmbientBackdrop({ level = 'quiet' }: { level?: 'quiet' | 'full' }) {
  return (
    <div className={`ambient ambient-${level}`} aria-hidden="true">
      {ITEMS.map((it, i) => (
        <span
          key={i}
          className={`ambient-item${it.far ? ' far' : ''}${it.extra ? ' extra' : ''}`}
          style={{ left: it.x, top: it.y, animationDuration: `${it.dur}s`, animationDelay: `${it.delay}s`, color: it.k === 'logo' ? 'var(--accent)' : `var(--c-${it.k})` }}
        >
          {it.k === 'logo' ? <LogoGlyph size={it.s} /> : <KindGlyph kind={it.k} size={it.s} variant="solid" />}
        </span>
      ))}
    </div>
  )
}
