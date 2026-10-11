import { Fragment, useRef, useState, type ReactNode } from 'react'
import { Link } from 'react-router-dom'
import { t } from '../i18n'
import type { Block, Inline } from './markdown.ts'
import { safeHref } from './markdown.ts'
import type { DocTarget } from './guide.ts'

// Draws the blocks the Markdown reader produced as React elements. No HTML is
// ever built from text: everything is a React node, links go through safeHref
// and the guide's own link resolver, and pictures are left out.

export interface Linker {
  // Where a link written in the page `file` goes.
  resolve: (href: string, file: string) => DocTarget
  // Called when a link to a place on the page already open is pressed.
  jump: (id: string) => void
  current: string // path and hash of the page now showing
}

function CopyButton({ text }: { text: string }) {
  const [done, setDone] = useState(false)
  const timer = useRef(0)
  async function copy() {
    try {
      await navigator.clipboard.writeText(text)
    } catch {
      // Not allowed on an address without https: fall back to a hidden field.
      const area = document.createElement('textarea')
      area.value = text
      area.style.position = 'fixed'
      area.style.opacity = '0'
      document.body.appendChild(area)
      area.select()
      try {
        document.execCommand('copy')
      } finally {
        area.remove()
      }
    }
    setDone(true)
    window.clearTimeout(timer.current)
    timer.current = window.setTimeout(() => setDone(false), 1600)
  }
  return (
    <button type="button" className="g-copy" onClick={() => void copy()}>
      {done ? t('Copied') : t('Copy')}
    </button>
  )
}

function Inlines({ nodes, file, linker, inLink = false }: { nodes: Inline[]; file: string; linker: Linker; inLink?: boolean }) {
  return (
    <>
      {nodes.map((n, i) => {
        switch (n.t) {
          case 'text':
            return <Fragment key={i}>{n.v}</Fragment>
          case 'code':
            return <code key={i}>{n.v}</code>
          case 'strong':
            return (
              <strong key={i}>
                <Inlines nodes={n.c} file={file} linker={linker} inLink={inLink} />
              </strong>
            )
          case 'em':
            return (
              <em key={i}>
                <Inlines nodes={n.c} file={file} linker={linker} inLink={inLink} />
              </em>
            )
          case 'del':
            return (
              <del key={i}>
                <Inlines nodes={n.c} file={file} linker={linker} inLink={inLink} />
              </del>
            )
          case 'br':
            return <br key={i} />
          case 'img':
            // The guide carries no pictures; the words of the caption are kept out of the flow too.
            return null
          case 'link': {
            const inner = <Inlines nodes={n.c} file={file} linker={linker} inLink />
            if (inLink) return <Fragment key={i}>{inner}</Fragment>
            const safe = safeHref(n.href)
            if (!safe) return <Fragment key={i}>{inner}</Fragment>
            const target = linker.resolve(safe, file)
            if (target.kind === 'none') return <Fragment key={i}>{inner}</Fragment>
            if (target.kind === 'guide') {
              return (
                <Link
                  key={i}
                  to={target.path}
                  onClick={(e) => {
                    // Pressing a link to the spot you are already at does nothing in the router, so scroll there.
                    if (target.path === linker.current) {
                      e.preventDefault()
                      linker.jump(target.path.split('#')[1] ?? '')
                    }
                  }}
                >
                  {inner}
                </Link>
              )
            }
            return (
              <a key={i} href={target.url} target="_blank" rel="noreferrer noopener" className="g-ext">
                {inner}
              </a>
            )
          }
        }
      })}
    </>
  )
}

export function Blocks({ blocks, file, linker, ids }: { blocks: Block[]; file: string; linker: Linker; ids: string[] }) {
  // Headings take their anchors in order; nested blocks never hold headings.
  let h = 0
  return <>{blocks.map((b, i) => renderBlock(b, i, file, linker, () => ids[h++]))}</>
}

function renderBlock(b: Block, key: number, file: string, linker: Linker, nextId: () => string): ReactNode {
  switch (b.t) {
    case 'heading': {
      const id = nextId()
      // The page's own title is the one h1, so the document's headings start at h2.
      const Tag = (`h${Math.min(6, Math.max(2, b.level))}`) as 'h2'
      return (
        <Tag key={key} id={id} className="g-h" tabIndex={-1}>
          <Inlines nodes={b.c} file={file} linker={linker} inLink />
          <a href={`#${id}`} className="g-anchor" aria-label={t('Link to this section')} onClick={(e) => { e.preventDefault(); linker.jump(id) }}>
            #
          </a>
        </Tag>
      )
    }
    case 'p':
      return (
        <p key={key}>
          <Inlines nodes={b.c} file={file} linker={linker} />
        </p>
      )
    case 'code':
      return (
        <div key={key} className="g-code">
          <CopyButton text={b.v} />
          <pre tabIndex={0}>
            <code>{b.v}</code>
          </pre>
        </div>
      )
    case 'quote':
      return (
        <blockquote key={key}>
          <Blocks blocks={b.c} file={file} linker={linker} ids={[]} />
        </blockquote>
      )
    case 'hr':
      return <hr key={key} />
    case 'list': {
      const items = b.items.map((it, j) => (
        <li key={j}>
          <Blocks blocks={it} file={file} linker={linker} ids={[]} />
        </li>
      ))
      return b.ordered ? (
        <ol key={key} start={b.start !== 1 ? b.start : undefined}>
          {items}
        </ol>
      ) : (
        <ul key={key}>{items}</ul>
      )
    }
    case 'table':
      return (
        <div key={key} className="g-table" tabIndex={0} role="region" aria-label={t('Table')}>
          <table>
            <thead>
              <tr>
                {b.head.map((c, j) => (
                  <th key={j} style={b.align[j] ? { textAlign: b.align[j] as 'left' } : undefined}>
                    <Inlines nodes={c} file={file} linker={linker} />
                  </th>
                ))}
              </tr>
            </thead>
            <tbody>
              {b.rows.map((r, j) => (
                <tr key={j}>
                  {r.map((c, k) => (
                    <td key={k} style={b.align[k] ? { textAlign: b.align[k] as 'left' } : undefined}>
                      <Inlines nodes={c} file={file} linker={linker} />
                    </td>
                  ))}
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )
  }
}
