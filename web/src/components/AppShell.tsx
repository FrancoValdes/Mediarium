import SidebarScrollMark from './SidebarScrollMark'
import { MiniPlayer, MusicPlayerProvider } from './MusicPlayer'
import { Fragment, useCallback, useEffect, useRef, useState, Suspense } from 'react'
import { NavLink, Outlet, useLocation, useNavigate, useNavigationType } from 'react-router-dom'
import { scrollToTop } from '../scrollTop'
import { api, isAdmin, type WatchLink } from '../api'
import { useAuth } from '../AuthContext'
import { demoEnabled } from '../demo'
import { titleFor, useSpecificTitle } from '../documentTitle'
import { sectionFor, type SectionKey } from '../sections'
import { inPage, pageParts, pageTitle, settingsGroupsFor } from '../settingsNav'
import { useLive } from '../useLive'
import { useUnreadErrors } from '../useProblems'
import { useModules } from '../ModulesContext'
import BrandMark from './BrandMark'
import PageLoading from './PageLoading'
import BusyNotice from './BusyNotice'
import FeedbackNudge from './FeedbackNudge'
import ImportBanner from './ImportBanner'
import Icon, { type IconName } from './Icon'
import ProfileMenu from './ProfileMenu'
import ReportHost from './ReportHost'
import SideTools from './SideTools'
import SearchBox from './SearchBox'
import ThemeToggle from './ThemeToggle'
import UsageStatsPrompt from './UsageStats'
import WhatsNew from './WhatsNew'
import { t, tc } from '../i18n'
import { useGuideShortcut } from '../guide/shortcut.ts'
import AmbientBackdrop from './AmbientBackdrop'

const NAV: { to: string; label: string; icon: IconName; sec: SectionKey; end?: boolean }[] = [
  { to: '/', label: t('Dashboard'), icon: 'grid', sec: 'dashboard', end: true },
  { to: '/discover', label: t('Discover'), icon: 'compass', sec: 'discover' },
  { to: '/library', label: t('Library'), icon: 'film', sec: 'library' },
  { to: '/calendar', label: t('Upcoming'), icon: 'calendar', sec: 'calendar' },
  { to: '/queue', label: t('Activity'), icon: 'activity', sec: 'activity' },
  { to: '/settings/media', label: t('Settings'), icon: 'sliders', sec: 'settings' },
]

// Where Back goes from a detail page that was opened directly; undefined
// for pages that aren't a single title.
function backFallback(path: string): string | undefined {
  if (path.startsWith('/title/') || path.startsWith('/show/')) return '/discover'
  if (path.startsWith('/series/')) return '/library?kind=tv'
  if (path.startsWith('/book/') || path.startsWith('/books/work/')) return '/library?kind=ebook'
  if (path.startsWith('/music/artist/') || path.startsWith('/music/album/')) return '/library?kind=music'
  if (path.startsWith('/music/release/')) return '/discover?kind=music'
  if (path.startsWith('/music/playlists/')) return '/music/playlists'
  if (path === '/music/playlists') return '/library?kind=music'
  return undefined
}

// Persistent left sidebar plus an always-visible search bar that looks up
// movies and TV shows live as you type. On phones the sidebar slides in from
// the left.
export default function AppShell() {
  return (
    <MusicPlayerProvider>
      <Shell />
    </MusicPlayerProvider>
  )
}

function Shell() {
  const [menuOpen, setMenuOpen] = useState(false)
  // A small, hideable link to support the project (administrators only). Hidden per browser.
  const [showSupport] = useState(() => {
    try {
      return localStorage.getItem('mediarium-hide-support') !== '1'
    } catch {
      return true
    }
  })
  const [active, setActive] = useState(0)
  const location = useLocation()
  const [homes, setHomes] = useState<WatchLink[]>([])
  useEffect(() => {
    api.mediaServerHomes().then(setHomes).catch(() => setHomes([]))
  }, [])
  // A new page opens at the top. Back puts the page where it was left (a long list you were scrolling), and a change that only
  // rewrites the address in place (picking a filter, typing in a search box) leaves the page where it is.
  const navType = useNavigationType()
  const scrolled = useRef(new Map<string, number>())
  const lastPath = useRef(location.pathname)
  useEffect(() => {
    const el = document.querySelector('.content')
    if (!el) return
    const key = location.key
    const onScroll = () => scrolled.current.set(key, el.scrollTop)
    el.addEventListener('scroll', onScroll, { passive: true })
    return () => el.removeEventListener('scroll', onScroll)
  }, [location.key])
  useEffect(() => {
    const samePath = lastPath.current === location.pathname
    lastPath.current = location.pathname
    if (navType === 'POP') {
      const y = scrolled.current.get(location.key) ?? 0
      if (y === 0) return scrollToTop()
      // The list may still be loading, so keep trying for a moment until the page is tall enough.
      const el = document.querySelector('.content')
      let tries = 0
      let timer = 0
      const tick = () => {
        el?.scrollTo({ top: y, behavior: 'auto' })
        if (el && Math.abs(el.scrollTop - y) > 2 && tries++ < 25) timer = window.setTimeout(tick, 120)
      }
      tick()
      return () => window.clearTimeout(timer)
    }
    if (navType === 'PUSH' || !samePath) scrollToTop()
  }, [location.key, location.pathname, navType])
  // On a phone the kind tabs scroll sideways: the open one is brought into view (sideways only, the page stays where it is).
  useEffect(() => {
    const timer = window.setTimeout(() => {
      for (const tabs of document.querySelectorAll<HTMLElement>('.kind-tabs')) {
        const on = tabs.querySelector<HTMLElement>(':scope > .active')
        if (!on || tabs.scrollWidth <= tabs.clientWidth + 2) continue
        const mine = on.getBoundingClientRect()
        const box = tabs.getBoundingClientRect()
        tabs.scrollLeft += mine.left - box.left - (tabs.clientWidth - mine.width) / 2
      }
    }, 150)
    return () => window.clearTimeout(timer)
  }, [location.key, location.search])
  const { user } = useAuth()
  const admin = isAdmin(user)
  // Members only have their own profile and About under Settings.
  const settingsGroups = settingsGroupsFor(admin, user?.role === 'viewer')
  const nav = NAV.map((n) => (n.sec === 'settings' ? { ...n, to: settingsGroups[0].pages[0].to } : n))
  const { on: moduleOn } = useModules()
  const ebooksOn = moduleOn('ebooks')
  const audiobooksOn = moduleOn('audiobooks')
  const musicOn = moduleOn('music')
  const inUpcoming = location.pathname.startsWith('/wanted') || location.pathname.startsWith('/calendar')
  // Errors from the last day that nobody has marked as read (administrators only).
  const unreadErrors = useUnreadErrors(admin)
  const errorDot = <span className="nav-dot" role="img" aria-label={t('New errors')} title={t('New errors. See Settings, System, Logs and errors.')} />

  // Close the mobile drawer whenever the page changes.
  useEffect(() => {
    setMenuOpen(false)
  }, [location.pathname])

  // On a phone the search is an icon that opens a bar over the header.
  const [searchOpen, setSearchOpen] = useState(false)
  useEffect(() => {
    if (!searchOpen) return
    document.querySelector<HTMLInputElement>('.top-search input')?.focus()
    const onKey = (e: KeyboardEvent) => e.key === 'Escape' && setSearchOpen(false)
    document.addEventListener('keydown', onKey)
    return () => document.removeEventListener('keydown', onKey)
  }, [searchOpen])
  useEffect(() => {
    setSearchOpen(false)
  }, [location.pathname])

  // The Settings entry always lists its groups, and one group at a time shows
  // its pages (an accordion). The group that holds the open settings page is
  // the open one; pressing another group opens it and closes the one before.
  // On any other page the open group folds up again; the list of groups stays.
  const currentGroup = location.pathname.startsWith('/settings')
    ? (settingsGroups.find((g) => g.pages.length > 1 && g.pages.some((p) => inPage(location.pathname, p.to)))?.key ?? null)
    : null
  const [openKey, setOpenKey] = useState<string | null>(() => currentGroup)
  // Moving to a page of another group closes the one that was open: also to a page
  // that has no pages under it (Indexers & Search), and to any page outside Settings.
  useEffect(() => {
    setOpenKey(currentGroup)
  }, [currentGroup, location.pathname])

  const inSettings = location.pathname.startsWith('/settings')

  // The page name in the top bar, in the browser tab and (when the page has no
  // heading of its own) as the page's one <h1>.
  const heading = location.pathname.startsWith('/help') ? t('User guide') : pageTitle(location.pathname, admin)
  const parts = pageParts(location.pathname, admin)
  const headingNode = parts.group ? (
    <>
      <span className="top-crumb">{parts.group}</span>
      <span className="top-sep" aria-hidden="true"> · </span>
      {parts.page}
    </>
  ) : (
    heading
  )
  useGuideShortcut()
  const specificTitle = useSpecificTitle()
  useEffect(() => {
    document.title = titleFor(specificTitle || heading)
  }, [specificTitle, heading])
  const mainRef = useRef<HTMLElement>(null)
  const navRef = useRef<HTMLElement>(null)
  const [pageHasHeading, setPageHasHeading] = useState(false)
  // Title, show, book and artist pages get a Back button: it returns to
  // wherever the page was opened from, or to its section when it was opened
  // directly (a link from outside, a fresh tab).
  const navigate = useNavigate()
  const backTo = backFallback(location.pathname)
  const goBack = () => {
    const idx = (window.history.state as { idx?: number } | null)?.idx ?? 0
    if (idx > 0) navigate(-1)
    else if (backTo) navigate(backTo)
  }
  useEffect(() => {
    const el = mainRef.current
    if (!el) return
    const check = () => setPageHasHeading(el.querySelector('h1') !== null)
    check()
    const watch = new MutationObserver(check)
    watch.observe(el, { childList: true, subtree: true })
    return () => watch.disconnect()
  }, [location.pathname])
  const [version, setVersion] = useState('')
  useEffect(() => {
    api.version().then((v) => setVersion(v.version)).catch(() => undefined)
  }, [])
  // A dot on the version when a newer release is waiting (administrators only).
  const [updateReady, setUpdateReady] = useState(false)
  const checkUpdate = useCallback(() => {
    if (!admin) return
    api
      .updateLatest()
      .then((n) => setUpdateReady(n.available))
      .catch(() => undefined)
  }, [admin])
  useEffect(checkUpdate, [checkUpdate])
  useLive(checkUpdate, 10 * 60 * 1000, admin)

  // A small count of downloads in flight next to "Activity".
  const loadActive = useCallback(() => {
    api
      .listActiveQueue()
      .then((q) => setActive(q.length))
      .catch(() => undefined)
  }, [])
  useEffect(loadActive, [loadActive])
  useLive(loadActive, 3000)

  return (
    <div className="app-shell" data-section={sectionFor(location.pathname)}>
      <a
        className="skip-link"
        href="#main"
        onClick={(e) => {
          e.preventDefault()
          mainRef.current?.focus()
        }}
      >
        {t('Skip to content')}
      </a>
      <div className={`sidebar-backdrop${menuOpen ? ' open' : ''}`} onClick={() => setMenuOpen(false)} />
      <nav ref={navRef} className={`sidebar${menuOpen ? ' open' : ''}`} aria-label={tc('menu', 'Main')}>
        <div className="brand">
          <BrandMark />
          <span className="brand-text">
            <span className="brand-name">Mediarium</span>
          </span>
        </div>
        {nav.map((n) => (
          <Fragment key={n.to}>
          <NavLink to={n.to} end={n.end} style={{ ['--nc' as string]: `var(--c-${n.sec})` }} className={({ isActive }) => (n.sec === 'settings' ? (inSettings ? 'active parent-open' : '') : isActive || (n.sec === 'calendar' && inUpcoming) ? 'active' : '')}>
            <span className="nav-ico">
              <Icon name={n.icon} size={17} />
            </span>
            <span className="nav-label">{n.label}</span>
            {n.to === '/queue' && active > 0 && <span className="nav-count">{active}</span>}
          </NavLink>
          </Fragment>
        ))}
        {(
        <div className="subnav" style={{ ['--nc' as string]: 'var(--c-settings)' }}>
          {settingsGroups.map((g) => {
            const current = g.pages.some((p) => inPage(location.pathname, p.to))
            if (g.pages.length === 1) {
              return (
                <NavLink key={g.key} to={g.pages[0].to} style={{ ['--sc' as string]: g.color }} className={() => (current ? 'active' : '')}>
                  <span className="nav-ico">
                    <Icon name={g.icon} size={15} />
                  </span>
                  <span className="nav-label">{g.label}</span>
                </NavLink>
              )
            }
            const open = openKey === g.key
            // Opening a group goes to its first page; pressing an open one folds it up.
            const toggleGroup = () => {
              if (open) {
                setOpenKey(null)
                return
              }
              setOpenKey(g.key)
              if (!g.pages.some((p) => inPage(location.pathname, p.to))) navigate(g.pages[0].to)
            }
            return (
              <div key={g.key} className={`sn-group${open ? ' open' : ''}`} style={{ ['--sc' as string]: g.color }}>
                <NavLink
                  to={g.pages[0].to}
                  role="button"
                  aria-expanded={open}
                  aria-controls={`sn-pages-${g.key}`}
                  className={() => (current ? 'sn-head current' : 'sn-head')}
                  onClick={(e) => {
                    e.preventDefault()
                    toggleGroup()
                  }}
                  onKeyDown={(e) => {
                    // A link answers Enter on its own; a button also answers Space.
                    if (e.key === ' ') {
                      e.preventDefault()
                      toggleGroup()
                    }
                  }}
                >
                  <span className="nav-ico">
                    <Icon name={g.icon} size={15} />
                  </span>
                  <span className="nav-label">{g.label}</span>
                  {g.key === 'system' && unreadErrors > 0 && errorDot}
                  <span className="nav-chev" style={{ transform: open ? 'rotate(90deg)' : 'none' }}>
                    <Icon name="open" size={12} />
                  </span>
                </NavLink>
                {open && (
                  <div className="sn-pages" id={`sn-pages-${g.key}`}>
                    {g.pages.map((p) => (
                      <NavLink key={p.to} to={p.to} title={p.label} style={{ ['--sc' as string]: g.color }} className={() => (inPage(location.pathname, p.to) ? 'sn-page active' : 'sn-page')}>
                        <span className="sn-ico">
                          <Icon name={p.icon} size={13} />
                        </span>
                        <span className="nav-label">{p.label}</span>
                        {p.to === '/settings/logs' && unreadErrors > 0 && <span className="sn-count">{unreadErrors}</span>}
                      </NavLink>
                    ))}
                  </div>
                )}
              </div>
            )
          })}
        </div>
        )}
        <SideTools ebooksOn={ebooksOn} audiobooksOn={audiobooksOn} musicOn={musicOn} homes={homes} showSupport={admin && showSupport} variant="side">
          <ThemeToggle />
        </SideTools>
        {version && (
          <div className="side-version">
            {admin ? (
              <NavLink to="/settings/system" aria-label={t('Version {version}', { version: `v${version}` })} title={updateReady ? t('Mediarium {version}. A newer version is available.', { version }) : `Mediarium ${version}`}>
                <BrandMark className="ver-mark" />
                v{version}
                {updateReady && <span className="version-dot" aria-label={t('A newer version is available')} />}
              </NavLink>
            ) : (
              <span aria-label={t('Version {version}', { version: `v${version}` })} title={`Mediarium ${version}`}>
                <BrandMark className="ver-mark" />
                v{version}
              </span>
            )}
          </div>
        )}
      </nav>
      <SidebarScrollMark target={navRef} />
      <div className="main-area">
        <AmbientBackdrop />
        <header className="topbar">
          <button className="menu-button" onClick={() => setMenuOpen((o) => !o)} aria-label={t('Open menu')} aria-expanded={menuOpen}>
            <Icon name="menu" size={20} />
          </button>
          {backTo && (
            <button type="button" className="back-btn" onClick={goBack} aria-label={t('Back')} title={t('Back')}>
              <Icon name="chevron-left" size={18} />
              <span>{t('Back')}</span>
            </button>
          )}
          {pageHasHeading ? <p className="top-title">{headingNode}</p> : <h1 className="top-title">{headingNode}</h1>}
          <div className={`top-search${searchOpen ? ' open' : ''}`}>
            <SearchBox />
            <button type="button" className="icon-btn top-search-close" onClick={() => setSearchOpen(false)} aria-label={t('Close search')}>
              <Icon name="x" size={18} />
            </button>
          </div>
          <button type="button" className="icon-btn search-toggle" onClick={() => setSearchOpen(true)} aria-label={t('Search')}>
            <Icon name="search" size={19} />
          </button>
          <div className="top-actions">
            {demoEnabled() && (
              <span className="demo-chip" title={t('Sample data is showing. Turn it off from your profile menu.')}>
                <Icon name="flask" size={14} /> <span className="demo-chip-label">{t('Demo data')}</span>
              </span>
            )}
            <SideTools ebooksOn={ebooksOn} audiobooksOn={audiobooksOn} musicOn={musicOn} homes={homes} showSupport={admin && showSupport} variant="top" />
            <span className="top-theme">
              <ThemeToggle />
            </span>
            <ProfileMenu />
          </div>
        </header>
        <BusyNotice />
        <ImportBanner />
        <main id="main" className="content" ref={mainRef} tabIndex={-1}>
          <Suspense fallback={<PageLoading />}>
            <Outlet />
          </Suspense>
        </main>
        <MiniPlayer />
        {!demoEnabled() && <WhatsNew version={version} />}
        {!demoEnabled() && admin && <UsageStatsPrompt />}
        <FeedbackNudge />
        <ReportHost />
      </div>
    </div>
  )
}
