import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { Link, useLocation, useNavigate, useParams, useSearchParams } from 'react-router-dom'
import Icon from '../components/Icon'
import PageLoading from '../components/PageLoading'
import { useDocumentTitle } from '../documentTitle'
import { DOCS_URL } from '../docs'
import { currentLanguage, t } from '../i18n'
import type { GuideData } from '../guide/build.ts'
import { buildSearchIndex, highlight, indexGuide, resolveDocLink, searchGuide, searchTokens, snippet, tocFor, type GuideIndex, type TopicIndex } from '../guide/guide.ts'
import { loadGuide } from '../guide/load'
import { parseMarkdown } from '../guide/markdown.ts'
import { Blocks, type Linker } from '../guide/GuideText'
import { GROUPS } from '../guide/topicDefs.ts'
import '../guide/guide.css'

function scrollToId(id: string) {
  const el = id ? document.getElementById(id) : null
  if (!el) return
  el.scrollIntoView({ block: 'start' })
  el.focus({ preventScroll: true })
}

// The user guide: the project's documentation, built into this version of the
// app (web/tools/build-guide.mjs), so it works without a connection and always
// matches what is installed. /help lists the topics, /help/<topic> shows one,
// and ?q= is a search.
export default function Guide() {
  const { topic: topicId = '' } = useParams()
  const location = useLocation()
  const navigate = useNavigate()
  const [params, setParams] = useSearchParams()
  const q = params.get('q') ?? ''
  const searching = q.trim() !== ''
  const [data, setData] = useState<GuideData | null>(null)
  const [failed, setFailed] = useState(false)
  const searchBox = useRef<HTMLInputElement>(null)

  const [attempt, setAttempt] = useState(0)
  useEffect(() => {
    let live = true
    loadGuide().then(
      (d) => live && setData(d),
      () => live && setFailed(true),
    )
    return () => {
      live = false
    }
  }, [attempt])
  const load = () => {
    setFailed(false)
    setAttempt((n) => n + 1)
  }

  const guide = useMemo(() => (data ? indexGuide(data) : null), [data])
  const current = guide?.byId.get(topicId) ?? null
  const sections = useMemo(() => (guide && searching ? buildSearchIndex(guide) : null), [guide, searching])

  useDocumentTitle(current ? `${current.topic.title} · ${t('User guide')}` : t('User guide'))

  const jump = useCallback(
    (id: string) => {
      navigate({ hash: id ? `#${id}` : '' }, { replace: true })
      scrollToId(id)
    },
    [navigate],
  )

  // Opening a topic at an anchor (from a link, a search result or a HelpLink) scrolls there once the text is drawn.
  const hash = location.hash
  useEffect(() => {
    if (!current || searching || !hash) return
    const id = decodeURIComponent(hash.slice(1))
    // The app shell puts a newly opened page at the top and asks again a moment later, so the jump
    // to the section is repeated until that has settled.
    const timers = [0, 170, 420].map((ms) => window.setTimeout(() => scrollToId(id), ms))
    return () => timers.forEach((t) => window.clearTimeout(t))
  }, [current, searching, hash])

  const setQuery = (value: string) => {
    const next = new URLSearchParams(params)
    if (value) next.set('q', value)
    else next.delete('q')
    setParams(next, { replace: true })
  }

  const english = currentLanguage() === 'en'
  let body
  if (failed) {
    body = (
      <div className="g-note" role="alert">
        <p>{t('The guide could not be loaded.')}</p>
        <button type="button" onClick={load}>
          {t('Try again')}
        </button>
      </div>
    )
  } else if (!guide) {
    body = <PageLoading />
  } else if (guide.topics.length === 0) {
    body = (
      <div className="g-note">
        <p>{t('This copy of Mediarium was built without the guide.')}</p>
        <a href={DOCS_URL} target="_blank" rel="noreferrer noopener">
          {t('Read the documentation online')}
        </a>
      </div>
    )
  } else {
    body = (
      <div className={`g-layout${current || searching ? '' : ' g-home'}`}>
        <aside className="g-rail">
          <form className="g-search" role="search" onSubmit={(e) => e.preventDefault()}>
            <Icon name="search" size={16} />
            <input
              ref={searchBox}
              type="search"
              value={q}
              placeholder={t('Search the guide')}
              aria-label={t('Search the guide')}
              onChange={(e) => setQuery(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === 'Escape' && q) {
                  e.preventDefault()
                  setQuery('')
                } else if (e.key === 'Enter' && sections) {
                  const first = searchGuide(sections, q, 1)[0]
                  if (first) navigate(`/help/${first.section.topicId}${first.section.id ? '#' + first.section.id : ''}`)
                }
              }}
            />
            {q && (
              <button type="button" className="g-clear" aria-label={t('Clear the search')} onClick={() => { setQuery(''); searchBox.current?.focus() }}>
                <Icon name="x" size={14} />
              </button>
            )}
          </form>
          <TopicNav key={current?.topic.id ?? ''} guide={guide} current={current} jump={jump} />
        </aside>
        <div className="g-main">
          {!english && (
            <p className="g-lang" role="note">
              {t('The guide is in English for now. The rest of Mediarium stays in your language.')}
            </p>
          )}
          {searching && sections ? (
            <Results sections={sections} q={q} />
          ) : current ? (
            <Article ti={current} guide={guide} jump={jump} />
          ) : (
            <Landing guide={guide} unknown={topicId !== ''} />
          )}
        </div>
      </div>
    )
  }

  return (
    <>
      {body}
    </>
  )
}

// The topics grouped, with the sections of the open one under it.
function TopicNav({ guide, current, jump }: { guide: GuideIndex; current: TopicIndex | null; jump: (id: string) => void }) {
  const [active, setActive] = useState('')
  // On a phone the list is folded away until asked for; on a wide screen it is always shown.
  const [fold, setFold] = useState(false)
  const toc = useMemo(() => (current ? tocFor(current) : []), [current])

  // The section being read is marked in the list.
  useEffect(() => {
    if (!current) return
    const heads = Array.from(document.querySelectorAll<HTMLElement>('.g-article .g-h'))
    if (heads.length === 0 || typeof IntersectionObserver === 'undefined') return
    const seen = new Map<string, boolean>()
    const pick = () => {
      const first = heads.find((h) => seen.get(h.id))
      if (first) setActive(first.id)
    }
    const watch = new IntersectionObserver(
      (entries) => {
        for (const e of entries) seen.set(e.target.id, e.isIntersecting)
        pick()
      },
      { rootMargin: '0px 0px -75% 0px' },
    )
    heads.forEach((h) => watch.observe(h))
    return () => watch.disconnect()
  }, [current])

  const groups = GROUPS.filter((g) => guide.topics.some((ti) => ti.topic.group === g))
  return (
    <nav className="g-topics" aria-label={t('Guide topics')}>
      <button type="button" className="g-fold-btn" aria-expanded={fold} onClick={() => setFold((o) => !o)}>
        <span>{current ? current.topic.title : t('All topics')}</span>
        <Icon name="chevron-down" size={16} />
      </button>
      <div className={`g-groups${fold ? ' open' : ''}`}>
        {groups.map((g) => (
          <div key={g} className="g-group">
            <p className="g-group-name">{g}</p>
            <ul>
              {guide.topics
                .filter((ti) => ti.topic.group === g)
                .map((ti) => {
                  const here = ti === current
                  return (
                    <li key={ti.topic.id}>
                      <Link to={`/help/${ti.topic.id}`} aria-current={here ? 'page' : undefined} className={here ? 'on' : ''}>
                        {ti.topic.title}
                      </Link>
                      {here && toc.length > 0 && (
                        <ul className="g-toc" aria-label={t('On this page')}>
                          {toc.map((e) => (
                            <li key={e.id} className={e.depth ? 'sub' : ''}>
                              <a
                                href={`#${e.id}`}
                                aria-current={active === e.id ? 'location' : undefined}
                                onClick={(ev) => {
                                  ev.preventDefault()
                                  jump(e.id)
                                }}
                              >
                                {e.text}
                              </a>
                            </li>
                          ))}
                        </ul>
                      )}
                    </li>
                  )
                })}
            </ul>
          </div>
        ))}
      </div>
    </nav>
  )
}

function OnlineLink() {
  return (
    <a className="g-online" href={DOCS_URL} target="_blank" rel="noreferrer noopener">
      {t('Read the full documentation')} <Icon name="external" size={13} />
    </a>
  )
}

function Landing({ guide, unknown }: { guide: GuideIndex; unknown: boolean }) {
  return (
    <div className="g-landing">
      <header className="g-head">
        <div>
          <h1>{t('User guide')}</h1>
          <p className="g-lede">{t('How to do the everyday things. It is the same documentation as on the website, built into this version of Mediarium, so it works without an internet connection.')}</p>
        </div>
        <OnlineLink />
      </header>
      {unknown && (
        <p className="g-note" role="status">
          {t('There is no guide topic with that name. Pick one below.')}
        </p>
      )}
      {GROUPS.map((g) => {
        const list = guide.topics.filter((ti) => ti.topic.group === g)
        if (list.length === 0) return null
        return (
          <section key={g} className="g-lgroup" aria-label={g}>
            <h2>{g}</h2>
            <ul>
              {list.map((ti) => (
                <li key={ti.topic.id}>
                  <Link to={`/help/${ti.topic.id}`}>
                    <strong>{ti.topic.title}</strong>
                    <span>{ti.topic.summary}</span>
                  </Link>
                </li>
              ))}
            </ul>
          </section>
        )
      })}
    </div>
  )
}

export function Article({ ti, guide, jump, popup = false }: { ti: TopicIndex; guide: GuideIndex; jump: (id: string) => void; popup?: boolean }) {
  const location = useLocation()
  const parsed = useMemo(() => ti.topic.sources.map((s) => parseMarkdown(s.md)), [ti])
  const linker: Linker = useMemo(
    () => ({
      resolve: (href, file) => resolveDocLink(href, file, guide),
      jump,
      current: location.pathname + location.hash,
    }),
    [guide, jump, location.pathname, location.hash],
  )
  const toc = useMemo(() => tocFor(ti), [ti])
  const at = guide.topics.indexOf(ti)
  const prev = at > 0 ? guide.topics[at - 1] : null
  const next = at < guide.topics.length - 1 ? guide.topics[at + 1] : null

  return (
    <article className="g-article">
      <header className="g-head">
        <div>
          <h1>{ti.topic.title}</h1>
          <p className="g-lede">{ti.topic.summary}</p>
        </div>
        <OnlineLink />
      </header>
      {toc.length > 1 && (
        <details className="g-here">
          <summary>{t('On this page')}</summary>
          <ul>
            {toc.map((e) => (
              <li key={e.id} className={e.depth ? 'sub' : ''}>
                <a
                  href={`#${e.id}`}
                  onClick={(ev) => {
                    ev.preventDefault()
                    jump(e.id)
                  }}
                >
                  {e.text}
                </a>
              </li>
            ))}
          </ul>
        </details>
      )}
      <div className="g-body">
        {ti.topic.sources.map((s, i) => (
          <section key={s.file + i} className="g-source">
            {s.title && (
              <h2 id={ti.sourceIds[i].titleId} className="g-h g-source-title" tabIndex={-1}>
                {s.title}
                <a href={`#${ti.sourceIds[i].titleId}`} className="g-anchor" aria-label={t('Link to this section')} onClick={(e) => { e.preventDefault(); jump(ti.sourceIds[i].titleId) }}>
                  #
                </a>
              </h2>
            )}
            <Blocks blocks={parsed[i]} file={s.file} linker={linker} ids={ti.sourceIds[i].ids} />
          </section>
        ))}
      </div>
      {!popup && (
      <footer className="g-foot">
        <nav aria-label={t('Other topics')}>
          {prev ? (
            <Link to={`/help/${prev.topic.id}`} rel="prev">
              <Icon name="chevron-left" size={15} />
              <span>
                <small>{t('Before this')}</small>
                {prev.topic.title}
              </span>
            </Link>
          ) : (
            <span />
          )}
          {next && (
            <Link to={`/help/${next.topic.id}`} rel="next" className="next">
              <span>
                <small>{t('Next')}</small>
                {next.topic.title}
              </span>
              <Icon name="chevron-right" size={15} />
            </Link>
          )}
        </nav>
        <p>
          {t('This page is part of the Mediarium documentation.')} <OnlineLink />
        </p>
      </footer>
      )}
    </article>
  )
}

function Results({ sections, q }: { sections: ReturnType<typeof buildSearchIndex>; q: string }) {
  const hits = useMemo(() => searchGuide(sections, q, 40), [sections, q])
  const tokens = useMemo(() => searchTokens(q), [q])
  return (
    <div className="g-results" aria-live="polite">
      <header className="g-head">
        <div>
          <h1>{t('Search results')}</h1>
          <p className="g-lede">{hits.length === 0 ? t('Nothing in the guide matches “{query}”.', { query: q.trim() }) : hits.length === 1 ? t('1 section matches “{query}”.', { query: q.trim() }) : t('{n} sections match “{query}”.', { n: hits.length, query: q.trim() })}</p>
        </div>
        <OnlineLink />
      </header>
      {hits.length === 0 && <p className="g-note">{t('Try fewer or different words, or pick a topic from the list.')}</p>}
      <ol>
        {hits.map(({ section: s }) => (
          <li key={s.topicId + '#' + s.id}>
            <Link to={`/help/${s.topicId}${s.id ? '#' + s.id : ''}`}>
              <strong>{highlight(s.heading, tokens).map((p, i) => (p.hit ? <mark key={i}>{p.text}</mark> : p.text))}</strong>
              <small>{s.parent && s.parent !== s.topicTitle ? `${s.topicTitle} › ${s.parent}` : s.topicTitle}</small>
              <span>{highlight(snippet(s, tokens), tokens).map((p, i) => (p.hit ? <mark key={i}>{p.text}</mark> : p.text))}</span>
            </Link>
          </li>
        ))}
      </ol>
    </div>
  )
}
