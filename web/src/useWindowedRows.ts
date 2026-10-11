import { useCallback, useEffect, useLayoutEffect, useRef, useState, type RefObject } from 'react'
import { spaces, visibleRows } from './windowRange'

// A long list (a library of thousands of posters) is not put on the page all at
// once: only the rows near the screen exist, and empty space of the right
// height stands in for the rest, so the scroll bar, Back and "jump to where I
// was" behave as if every row were there. Short lists are left alone.

// Below this many rows nothing is held back.
const MIN_ROWS = 24

// The nearest ancestor that scrolls (the page content), or null for the window.
function scrollParent(el: HTMLElement | null): HTMLElement | null {
  for (let p = el?.parentElement; p; p = p.parentElement) {
    const oy = getComputedStyle(p).overflowY
    if (oy === 'auto' || oy === 'scroll' || oy === 'overlay') return p
  }
  return null
}

// The row heights measured so far, by list shape and width.
const measured = new Map<string, number>()

export interface RowWindow {
  // The rows to put on the page: [first, last).
  first: number
  last: number
  // Empty space that stands in for the rows above and below, in pixels.
  before: number
  after: number
  // False while every row is shown (a short list).
  windowed: boolean
}

// useWindowedRows decides which rows of a list of `rows` rows are on the page.
// ref is the element that holds the rows; perRow is how many items sit in a
// row (the columns of a grid, 1 for a table); lead and tail are how many
// children in front of and behind the items are not items (spacer rows), so the
// row height can be measured from the real ones. estimate is the row height
// (with its gap) to use until a real one has been measured.
export function useWindowedRows(ref: RefObject<HTMLElement | null>, rows: number, perRow: number, estimate: number, lead = 0, tail = 0): RowWindow {
  const windowed = rows >= MIN_ROWS
  // The height of one row with the gap below it, and that gap.
  const [metrics, setMetrics] = useState({ stride: estimate, gap: 0 })
  const [win, setWin] = useState({ first: 0, last: Math.min(rows, 10) })

  const compute = useCallback(
    (stride: number) => {
      const el = ref.current
      if (!el || !windowed) return
      const parent = scrollParent(el)
      const top = el.getBoundingClientRect().top
      const parentTop = parent ? parent.getBoundingClientRect().top : 0
      const height = parent ? parent.clientHeight : window.innerHeight
      const above = parentTop - top // how far the list's top edge is above the top of the screen
      const { first, last } = visibleRows(above, height, stride, rows)
      setWin((cur) => (cur.first === first && cur.last === last ? cur : { first, last }))
    },
    [ref, rows, windowed],
  )

  // Measure real rows (the distance between the first and last row on the page)
  // and use that height from then on. It is measured once for each width and
  // number of columns: measuring again while scrolling would move the space
  // above and make the list jump.
  const measuredFor = useRef('')
  useLayoutEffect(() => {
    const el = ref.current
    if (!el || !windowed) return
    const key = `${estimate}|${perRow}|${Math.round(el.clientWidth)}`
    if (measuredFor.current === key) return
    const adopt = (stride: number) => {
      measuredFor.current = key
      measured.set(key, stride)
      setMetrics({ stride, gap: Number.parseFloat(getComputedStyle(el).rowGap) || 0 })
      compute(stride) // the rows and the spaces are worked out again from the new height
    }
    // The same list laid out the same way before (going to a title and Back): use that height at once, so the page
    // is exactly as tall as it was and the scroll position still points at the same place.
    const known = measured.get(key)
    if (known) return adopt(known)
    const kids = el.children
    const items = kids.length - lead - tail
    const shown = Math.ceil(items / perRow)
    if (shown < 3) return
    const a = kids[lead] as HTMLElement | undefined
    const b = kids[lead + (shown - 1) * perRow] as HTMLElement | undefined
    if (!a || !b) return
    // Only trust it when the rows really have perRow items each (the column count may still be a guess).
    const endOfRow = kids[lead + perRow - 1] as HTMLElement | undefined
    const nextRow = kids[lead + perRow] as HTMLElement | undefined
    if (perRow > 1 && (!endOfRow || !nextRow || endOfRow.offsetTop !== a.offsetTop || nextRow.offsetTop <= a.offsetTop)) return
    const s = (b.offsetTop - a.offsetTop) / (shown - 1)
    if (s > 20) adopt(s)
  }, [ref, windowed, estimate, perRow, lead, tail, compute, win])

  useEffect(() => {
    if (!windowed) return
    const el = ref.current
    const parent = scrollParent(el)
    const target: HTMLElement | Window = parent ?? window
    let frame = 0
    const onChange = () => {
      if (frame) return
      frame = requestAnimationFrame(() => {
        frame = 0
        compute(metrics.stride)
      })
    }
    compute(metrics.stride)
    target.addEventListener('scroll', onChange, { passive: true })
    window.addEventListener('resize', onChange)
    const watch = typeof ResizeObserver === 'undefined' ? null : new ResizeObserver(onChange)
    if (watch && parent) watch.observe(parent)
    return () => {
      if (frame) cancelAnimationFrame(frame)
      target.removeEventListener('scroll', onChange)
      window.removeEventListener('resize', onChange)
      watch?.disconnect()
    }
  }, [ref, compute, windowed, metrics.stride])

  if (!windowed) return { first: 0, last: rows, before: 0, after: 0, windowed: false }
  const first = Math.min(win.first, rows)
  const last = Math.min(Math.max(win.last, first), rows)
  const { before, after } = spaces(first, last, rows, metrics.stride, metrics.gap)
  return { first, last, before, after, windowed: true }
}
