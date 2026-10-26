/* Pulse service worker: lets Android (and desktop browsers) install the dashboard as an
   app. It caches nothing: customer messages and contacts never stay on the phone, and
   every page and API call goes to Pulse itself. When Pulse cannot be reached, opening the
   app shows a short explanation instead of the browser's error page. */
'use strict';

self.addEventListener('install', () => self.skipWaiting());
self.addEventListener('activate', (event) => event.waitUntil(self.clients.claim()));

const OFFLINE = `<!DOCTYPE html><html lang="en"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1"><title>Pulse is not reachable</title></head>
<body>
<h1>Pulse is not reachable</h1>
<p>The computer or server running Pulse is off, offline, or its address changed.</p>
<p>Check that Pulse is running and that remote access is switched on in Settings &gt; Account, then try again.</p>
</body></html>`;

self.addEventListener('fetch', (event) => {
  if (event.request.mode !== 'navigate') return; // everything else goes to the network as usual
  event.respondWith(fetch(event.request).catch(() =>
    new Response(OFFLINE, { status: 503, headers: { 'Content-Type': 'text/html; charset=utf-8', 'Cache-Control': 'no-store' } })));
});
