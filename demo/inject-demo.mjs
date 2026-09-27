/* Inject the showcase entry script into the built index.html.
 *
 * What this is for
 * ----------------
 * The showcase publishes the REAL frontend (frontend/src is not modified)
 * on a host that cannot run the Go gateway. So every /api/* request fails.
 * That is fine and intended — the page is here to be looked at, not used.
 *
 * But there is exactly one thing failure does break, and it is not a
 * styling problem:
 *
 *     App.tsx:  if (!loggedIn) return <LoginPage />;
 *               return <HomePage />;
 *
 * A hard binary gate, no routes, no public page. Without a token the
 * showcase is one login form — the app the visitor came to see is never
 * mounted. And `useAuthStore` derives the flag from storage:
 *
 *     loggedIn: initialToken !== null
 *
 * so ANY non-empty string under `auth.accessToken` is enough. The store's
 * initializer runs once when the module is first imported, which is why
 * this has to be a plain inline <script> in the document rather than
 * something that runs after mount: the bundle's <script type="module"> is
 * deferred until parsing finishes, so an inline script anywhere in the
 * body is guaranteed to have run first.
 *
 * Nothing is mocked. The token is not a credential — it is never sent
 * anywhere, because every request it would authenticate is already
 * failing against a 404. It exists only to get past the render gate.
 *
 * Usage: node demo/inject-demo.mjs <distDir>
 */

import { readFile, writeFile } from 'node:fs/promises';
import { join } from 'node:path';

const [, , distDir] = process.argv;

if (!distDir) {
  console.error('usage: node inject-demo.mjs <distDir>');
  process.exit(2);
}

const indexPath = join(distDir, 'index.html');
const original = await readFile(indexPath, 'utf8');

/* Two independent ways this could go wrong, both silent: a page that
 * publishes without the token lands on the login form, and one that
 * publishes with it twice is merely noise. The first is the dangerous
 * one, so it is the one guarded. */
const MARKER = 'auth.accessToken';
if (original.includes(MARKER)) {
  console.error('::error::index.html already contains the showcase script — refusing to inject twice');
  process.exit(1);
}

const bodies = original.match(/<\/body>/g) || [];
if (bodies.length !== 1) {
  console.error(`::error::expected exactly one </body> in index.html, found ${bodies.length}`);
  process.exit(1);
}

/* Kept deliberately small and dependency-free. The notice exists because
 * an all-empty app with no explanation reads as a broken deployment, not
 * as a showcase — and the requests really are failing, in the console,
 * for anyone who opens devtools. It is a dismissible pill in a corner
 * rather than a full-width strip so it never covers the UI being shown. */
const script = `<script>
/* showcase entry — see demo/inject-demo.mjs */
(function () {
  try {
    localStorage.setItem('auth.accessToken', 'showcase-not-a-real-token');
  } catch (e) { /* private mode: the visitor gets the login form */ }

  function notice() {
    var el = document.createElement('div');
    el.setAttribute('data-showcase-notice', '');
    el.style.cssText = [
      'position:fixed', 'left:12px', 'bottom:12px', 'z-index:2147483647',
      'max-width:min(30rem,calc(100vw - 24px))', 'display:flex',
      'align-items:flex-start', 'gap:8px', 'padding:8px 10px',
      'font:12px/1.5 ui-sans-serif,system-ui,sans-serif',
      'color:#e7e5e4', 'background:rgba(28,25,23,.92)',
      'border:1px solid rgba(168,162,158,.35)', 'border-radius:8px',
      'box-shadow:0 4px 16px rgba(0,0,0,.25)', 'backdrop-filter:blur(6px)',
    ].join(';');
    var text = document.createElement('span');
    text.textContent = '静态展示：无后端，所有接口均不通，因此列表为空。';
    var close = document.createElement('button');
    close.type = 'button';
    close.textContent = '\\u00d7';
    close.setAttribute('aria-label', '关闭提示');
    close.style.cssText = [
      'flex:none', 'margin:0', 'padding:0 2px', 'border:0', 'cursor:pointer',
      'font:inherit', 'font-size:14px', 'line-height:1', 'color:inherit',
      'background:transparent', 'opacity:.6',
    ].join(';');
    close.addEventListener('click', function () { el.remove(); });
    el.appendChild(text);
    el.appendChild(close);
    document.body.appendChild(el);
  }
  if (document.body) notice();
  else document.addEventListener('DOMContentLoaded', notice);
})();
</script>`;

const patched = original.replace('</body>', `  ${script}\n  </body>`);
await writeFile(indexPath, patched);

/* Re-read rather than trusting the string: `replace` silently does
 * nothing if the anchor vanished, and a showcase published without the
 * entry script is a login form. */
const after = await readFile(indexPath, 'utf8');
if (!after.includes(MARKER)) {
  console.error('::error::injection did not land — refusing to publish a showcase that shows only the login page');
  process.exit(1);
}

console.log(`Injected showcase entry script into ${indexPath}`);
