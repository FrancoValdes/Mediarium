// Which rows of a long list belong on the page (see useWindowedRows). No React
// here, so it can be tested on its own.

// How far past the edge of the screen rows are kept, in pixels.
export const OVERSCAN = 1400
// Rows that are always on the page, so there are real rows to measure even
// when the height guess was far off.
export const MIN_SHOWN = 4

// visibleRows returns the rows [first, last) to put on the page. above is how
// far the top edge of the list is above the top of the screen (negative while
// the list starts further down), height is the height of the screen, stride is
// the height of one row together with the gap below it.
export function visibleRows(above: number, height: number, stride: number, rows: number): { first: number; last: number } {
  const first = Math.max(0, Math.min(rows, Math.floor((above - OVERSCAN) / stride)))
  const last = Math.max(Math.min(rows, first + MIN_SHOWN), Math.min(rows, Math.ceil((above + height + OVERSCAN) / stride)))
  return { first, last }
}

// spaces returns the empty space, in pixels, that stands in for the rows above
// first and below last. The last row has no gap below it.
export function spaces(first: number, last: number, rows: number, stride: number, gap: number): { before: number; after: number } {
  const before = first * stride
  const after = last < rows ? (rows - last) * stride - gap : 0
  return { before, after: Math.max(0, after) }
}
