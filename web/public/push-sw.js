// Mediarium's push notification worker. It only shows the notifications the
// server sends and opens the right page when one is tapped. It has no fetch
// handler on purpose: nothing is cached, so the app is never served stale.

// When Mediarium is served under a sub-path (URL_BASE, like /mediarium) this
// file is too, and the worker's scope is that path. Addresses of the app's own
// files and pages start with it.
const base = new URL(self.registration.scope).pathname.replace(/\/+$/, '')

self.addEventListener('install', () => self.skipWaiting())
self.addEventListener('activate', (event) => event.waitUntil(self.clients.claim()))

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
