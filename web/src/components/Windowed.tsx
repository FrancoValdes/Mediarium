import { useRef, type CSSProperties, type ReactNode } from 'react'
import { useGridColumns } from '../useGridColumns'
import { useWindowedRows } from '../useWindowedRows'

// A very long list of posters, or of table rows, with only the part near the
// screen on the page (see useWindowedRows). The look is exactly that of the
// plain list: a short list is simply rendered whole.

// A poster card and the gap below it, until a real one has been measured.
const POSTER_ROW = 380
// A table row, until a real one has been measured.
const TABLE_ROW = 66

export function WindowedGrid<T>({ items, render, className = 'poster-grid', style }: { items: T[]; render: (item: T) => ReactNode; className?: string; style?: CSSProperties }) {
  const ref = useRef<HTMLDivElement>(null)
  const cols = useGridColumns(ref)
  const rows = Math.ceil(items.length / cols)
  const win = useWindowedRows(ref, rows, cols, POSTER_ROW)
  const shown = win.windowed ? items.slice(win.first * cols, win.last * cols) : items
  return (
    <div ref={ref} className={className} style={{ ...style, paddingTop: win.before || undefined, paddingBottom: win.after || undefined }}>
      {shown.map(render)}
    </div>
  )
}

// The rows of a table body. columns is how many columns the table has, for the
// empty rows that stand in for what is out of view.
export function WindowedBody<T>({ items, columns, render }: { items: T[]; columns: number; render: (item: T) => ReactNode }) {
  const ref = useRef<HTMLTableSectionElement>(null)
  const win = useWindowedRows(ref, items.length, 1, TABLE_ROW, 1, 1)
  const shown = win.windowed ? items.slice(win.first, win.last) : items
  const space = (height: number) => (
    <tr aria-hidden="true" style={{ height }}>
      <td colSpan={columns} style={{ padding: 0, border: 0 }} />
    </tr>
  )
  return (
    <tbody ref={ref}>
      {space(win.before)}
      {shown.map(render)}
      {space(win.after)}
    </tbody>
  )
}
