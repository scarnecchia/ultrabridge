const CACHE_NAME = 'ultrabridge-v3';
const PRECACHE_ASSETS = [
  '/manifest.json',
  '/erb.png',
  '/htmx.min.js'
];
const PRECACHE_PATHS = new Set(PRECACHE_ASSETS);

self.addEventListener('install', (event) => {
  event.waitUntil(
    caches.open(CACHE_NAME)
      .then((cache) => cache.addAll(PRECACHE_ASSETS))
      .then(() => self.skipWaiting())
  );
});

self.addEventListener('activate', (event) => {
  event.waitUntil(
    caches.keys()
      .then((keys) => Promise.all(
        keys.filter((k) => k !== CACHE_NAME).map((k) => caches.delete(k))
      ))
      .then(() => self.clients.claim())
  );
});

// Fetch strategy: cache-first ONLY for the static assets we explicitly
// precached at install time. Every other request (HTMX XHRs, tab navigation,
// JSON APIs, CalDAV, etc.) goes straight to the network — the SW does not
// call respondWith, so the browser handles the request normally with no
// SW-mediated caching path. This avoids the previous bug where HTMX
// requests like /files?path=Personal went through caches.match and could
// return undefined or a stale response, making HTMX swap nothing into
// #main-content and the page appear frozen.
self.addEventListener('fetch', (event) => {
  const req = event.request;
  if (req.method !== 'GET') return;

  // Let navigation requests (full-page loads) always hit the network directly.
  // The browser handles the request natively with no SW mediation.
  if (req.mode === 'navigate') return;

  // Everything else: only intercept if the path is one of the three static
  // assets we precached at install. All other GETs (HTMX XHRs, JSON APIs,
  // CalDAV, poller status pings) fall through to the network untouched.
  const url = new URL(req.url);
  if (url.origin !== self.location.origin) return;
  if (!PRECACHE_PATHS.has(url.pathname)) return;

  event.respondWith(
    caches.match(req).then((cached) => cached || fetch(req))
  );
});
