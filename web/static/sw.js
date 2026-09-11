// Network-only passthrough. This app is session/auth-heavy and largely
// real-time (WebSocket chat, live task/timesheet/notification data), so
// caching responses here would risk serving stale data or another user's
// cached page after a login switch. This service worker exists solely to
// satisfy PWA installability criteria (a registered SW with a fetch
// handler) — it deliberately does no offline caching.
self.addEventListener("install", () => {
  self.skipWaiting();
});

self.addEventListener("activate", (event) => {
  event.waitUntil(self.clients.claim());
});

self.addEventListener("fetch", (event) => {
  event.respondWith(fetch(event.request));
});
