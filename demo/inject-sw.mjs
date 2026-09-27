/* Inject the demo service-worker registration into the built index.html
 * and copy the demo assets next to it.
 *
 * This is a deploy-time step on the build artifact. frontend/src is not
 * modified — index.html as emitted by vite is left exactly as vite wrote
 * it apart from one added <script> tag.
 *
 * Usage: node demo/inject-sw.mjs <distDir> <base>
 */

import { copyFile, readFile, writeFile } from 'node:fs/promises';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

const HERE = dirname(fileURLToPath(import.meta.url));
const [, , distDir, base] = process.argv;

if (!distDir || !base) {
  console.error('usage: node inject-sw.mjs <distDir> <base>');
  process.exit(2);
}

/* sw.js and register-sw.js are copied verbatim; both derive the base from
 * their own URL, so neither needs a per-site build. */
for (const name of ['sw.js', 'register-sw.js']) {
  await copyFile(join(HERE, name), join(distDir, name));
}

const indexPath = join(distDir, 'index.html');
const original = await readFile(indexPath, 'utf8');

const MARKER = 'register-sw.js';
if (original.includes(MARKER)) {
  console.error('::error::index.html already references register-sw.js — refusing to inject twice');
  process.exit(1);
}

const bodies = original.match(/<\/body>/g) || [];
if (bodies.length !== 1) {
  console.error(`::error::expected exactly one </body> in index.html, found ${bodies.length}`);
  process.exit(1);
}

const tag = `<script src="${base}/register-sw.js"></script>`;
const patched = original.replace('</body>', `  ${tag}\n  </body>`);

await writeFile(indexPath, patched);

if (!patched.includes(tag)) {
  console.error('::error::injection did not land — refusing to publish a site with no worker');
  process.exit(1);
}

console.log(`Injected ${tag} into index.html; copied sw.js + register-sw.js into ${distDir}`);
