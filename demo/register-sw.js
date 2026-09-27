/* Registers the demo service worker.
 *
 * Kept deliberately tiny and dependency-free. The base path is derived
 * from this script's own URL rather than hardcoded, so the same file works
 * at /music-tag-web/ (project Pages) or at / (a custom domain / user
 * Pages site) without a rebuild.
 */
(function () {
  if (!('serviceWorker' in navigator)) return;
  // document.currentScript is captured synchronously at parse time; the
  // 'load' handler runs later, by which point currentScript is null.
  var here = document.currentScript;
  if (!here || !here.src) return;
  var base = new URL('.', here.src).pathname;

  window.addEventListener('load', function () {
    navigator.serviceWorker.register(base + 'sw.js', { scope: base }).catch(function (err) {
      // Non-fatal: without a worker the app just shows its login screen
      // and every API call 404s. Say so rather than failing silently.
      console.warn('[demo] service worker registration failed:', err);
    });
  });
})();
