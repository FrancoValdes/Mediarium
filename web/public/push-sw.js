// Mediarium's service worker. It shows the notifications the server sends and
// opens the right page when one is tapped, and when the server can't be
// reached it answers a page that was asked for with a short "can't be reached"
// page (offline.html) instead of the browser's own error. It caches nothing
// else: every page and file comes from the server each time, so the app is
// never served stale.

// When Mediarium is served under a sub-path (URL_BASE, like /mediarium) this
// file is too, and the worker's scope is that path. Addresses of the app's own
// files and pages start with it.
const base = new URL(self.registration.scope).pathname.replace(/\/+$/, '')

const OFFLINE_CACHE = 'mediarium-offline-v1'
const OFFLINE_PAGE = base + '/offline.html'
const OFFLINE_FILES = [OFFLINE_PAGE, base + '/offline.js']

self.addEventListener('install', (event) => {
  event.waitUntil(
    caches
      .open(OFFLINE_CACHE)
      .then((cache) => cache.addAll(OFFLINE_FILES.map((f) => new Request(f, { cache: 'reload' }))))
      .catch(() => undefined) // without the page the browser's own error shows
      .then(() => self.skipWaiting()),
  )
})
self.addEventListener('activate', (event) =>
  event.waitUntil(
    caches
      .keys()
      .then((names) => Promise.all(names.filter((n) => n.startsWith('mediarium-offline-') && n !== OFFLINE_CACHE).map((n) => caches.delete(n))))
      .catch(() => undefined)
      .then(() => self.clients.claim()),
  ),
)

// Makes sure the page for a missing server is saved. It is saved when the
// worker installs; this tries again after a page loaded fine, in case that
// first try failed (a slow start, a flaky connection).
async function keepOfflineFiles() {
  try {
    const cache = await caches.open(OFFLINE_CACHE)
    for (const f of OFFLINE_FILES) {
      if (!(await cache.match(f))) await cache.add(new Request(f, { cache: 'reload' }))
    }
  } catch {
    // tried again after the next page
  }
}

// The saved "can't be reached" page, with its script's address made absolute:
// it is shown at whatever address was asked for, so a relative one would not do.
async function offlinePage() {
  const hit = await caches.match(OFFLINE_PAGE)
  if (!hit) return Response.error()
  const html = (await hit.text()).replace('src="offline.js"', 'src="' + base + '/offline.js"')
  return new Response(html, { status: 200, headers: { 'Content-Type': 'text/html; charset=utf-8' } })
}

// A page the person asked for (a navigation), and the offline page's own
// script, are answered from the network, and from the saved copy only when the
// network failed. Everything else goes straight to the network, as if there
// were no worker.
self.addEventListener('fetch', (event) => {
  const req = event.request
  if (req.mode === 'navigate') {
    event.respondWith(
      fetch(req).then((res) => {
        event.waitUntil(keepOfflineFiles())
        return res
      }, offlinePage),
    )
  } else if (new URL(req.url).pathname === base + '/offline.js') {
    event.respondWith(fetch(req).catch(async () => (await caches.match(base + '/offline.js')) || Response.error()))
  }
})

self.addEventListener('push', (event) => {
  let data = {}
  try {
    data = event.data ? event.data.json() : {}
  } catch {
    data = { body: event.data ? event.data.text() : '' }
  }
  const title = data.title || 'Mediarium'
  event.waitUntil(
    self.registration.showNotification(title, {
      body: data.body || '',
      icon: base + '/icon-192.png',
      badge: base + '/favicon-48.png',
      tag: data.tag || undefined,
      data: { url: data.url || base + '/' },
    }),
  )
})

self.addEventListener('notificationclick', (event) => {
  event.notification.close()
  const path = (event.notification.data && event.notification.data.url) || base + '/'
  const target = new URL(path, self.location.origin).href
  event.waitUntil(
    self.clients.matchAll({ type: 'window', includeUncontrolled: true }).then((list) => {
      for (const client of list) {
        if (client.url.startsWith(self.location.origin) && 'focus' in client) {
          if ('navigate' in client) client.navigate(target)
          return client.focus()
        }
      }
      return self.clients.openWindow(target)
    }),
  )
})
