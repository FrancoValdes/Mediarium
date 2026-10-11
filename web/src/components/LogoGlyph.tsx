import { useId } from 'react'

// The Mediarium arch as a solid object: lit from the top, a darker edge, and a
// slab of depth behind it, so it sits with the media shapes (KindGlyph's
// "solid" variant) instead of looking like a flat sticker. Takes its colour
// from the current text colour; the .g-logo class sets it to the accent.
export default function LogoGlyph({ size = 64, className = '' }: { size?: number; className?: string }) {
  const id = useId().replace(/:/g, '')
  const arch = 'M5 28V15a11 11 0 0 1 22 0v13h-6l-5-11-5 11z'
  return (
    <svg className={`kind-glyph solid-glyph g-logo ${className}`.trim()} width={size} height={size} viewBox="0 0 32 32" fill="none" strokeWidth={1.2} strokeLinejoin="round" aria-hidden="true">
      <defs>
        <linearGradient id={`l${id}`} x1="0" y1="0" x2="0.35" y2="1">
          <stop offset="0" className="g-top" />
          <stop offset="0.55" className="g-mid" />
          <stop offset="1" className="g-low" />
        </linearGradient>
      </defs>
      <path className="logo-depth" d={arch} transform="translate(1.1 1.5)" />
      <path className="logo-face" d={arch} fill={`url(#l${id})`} />
      <path className="shine" d="M7.4 15.5a8.6 8.6 0 0 1 7.2-8.1" />
    </svg>
  )
}
