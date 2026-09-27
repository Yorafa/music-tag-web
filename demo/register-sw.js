/* Registers the demo service worker.
 *
 * Kept deliberately tiny and dependency-free. The base path is derived
 * from this script's own URL rather than hardcoded, so the same file works
 * at /music-tag-web/ (project Pages) or at / (a custom domain / user
 * Pages site) without a rebuild.
 *
 * The early return at the top is the one path that cannot use a worker:
 * `navigator.serviceWorker` does not exist outside a secure context, and
 * plain-HTTP LAN access (http://192.168.x.x:8000/, a phone opening the
 * demo before it is on https) is not one. Returning silently there left
 * the visitor staring at a login form whose password cannot possibly
 * work — they type admin/admin, get a generic auth failure, and conclude
 * the demo is broken. So that case gets a banner.
 */
(function () {
  function banner(lines) {
    var el = document.createElement('div');
    el.setAttribute('role', 'alert');
    el.style.cssText = [
      'position:fixed', 'inset:auto 0 0 0', 'z-index:9999',
      'padding:12px 16px', 'font:14px/1.5 system-ui,sans-serif',
      'background:#7f1d1d', 'color:#fff', 'text-align:center',
    ].join(';');
    el.textContent = lines.join(' ');
    var mount = function () { document.body.appendChild(el); };
    if (document.body) mount();
    else document.addEventListener('DOMContentLoaded', mount);
  }

  if (!('serviceWorker' in navigator)) {
    if (location.protocol === 'https:' || location.hostname === 'localhost' ||
        location.hostname === '127.0.0.1' || location.hostname === '[::1]') {
      // https and still no worker: genuinely broken, not a context problem.
      banner(['演示需要 Service Worker，但此浏览器没有提供。', '请用普通（非隐私）窗口打开。']);
    } else {
      banner([
        '演示需要 Service Worker，而明文 HTTP 下浏览器不提供它，',
        '所以接口无法被拦截，登录会失败。请改用 https 访问，或在本机用',
        'http://127.0.0.1:8000/ 打开。',
      ]);
    }
    return;
  }

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
      banner(['Service Worker 注册失败：' + err.message, '演示将无法登录。']);
    });
  });
})();
