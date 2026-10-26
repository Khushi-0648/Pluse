/* Registers the service worker so the dashboard can be installed as an app (Android:
   Chrome menu > Install app). Browsers only allow this over https or on this computer. */
(() => {
  'use strict';
  if (!('serviceWorker' in navigator)) return;
  const local = location.hostname === '127.0.0.1' || location.hostname === 'localhost' || location.hostname === '[::1]';
  if (location.protocol !== 'https:' && !local) return;
  window.addEventListener('load', () => {
    navigator.serviceWorker.register('/sw.js', { scope: '/' }).catch(() => { /* the dashboard works without it */ });
  });
})();
