/* The service worker that receives a pushed alert.
 *
 * Deliberately tiny. It runs outside the app, it cannot be reasoned about from
 * the rest of the code, and anything clever here is clever in a context nobody
 * is looking at: it wakes for one message and shows one notification.
 *
 * Registered only in a secure context. Browsers refuse a service worker over
 * plain HTTP, so on an install reached by IP and port this never runs — which
 * is a property of the deployment, not of this file.
 */

self.addEventListener('push', (event) => {
  let alert = { title: 'Agentifi', body: '', url: '/' }
  try {
    alert = { ...alert, ...(event.data ? event.data.json() : {}) }
  } catch {
    // A payload that is not ours is still worth surfacing as "something
    // happened", rather than swallowed.
  }
  event.waitUntil(
    self.registration.showNotification(alert.title, {
      body: alert.body,
      icon: '/icon-192.png',
      // Android draws the badge in the status bar from its alpha alone, so it
      // is the robot's silhouette, not the coloured icon.
      badge: '/badge-96.png',
      // The title is the tag: one tag per alert type would collapse two
      // different warnings about different accounts into one.
      tag: alert.title,
      data: { url: alert.url || '/' },
    }),
  )
})

self.addEventListener('notificationclick', (event) => {
  event.notification.close()
  const target = (event.notification.data && event.notification.data.url) || '/'
  event.waitUntil(
    self.clients.matchAll({ type: 'window', includeUncontrolled: true }).then((windows) => {
      // Focus a tab that is already open rather than opening a second one:
      // somebody with the app open does not want a duplicate of it.
      for (const one of windows) {
        if (one.url.includes(target) && 'focus' in one) return one.focus()
      }
      return self.clients.openWindow(target)
    }),
  )
})
