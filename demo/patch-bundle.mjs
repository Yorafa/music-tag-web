/* Rewrite the frontend's absolute API paths so they sit under the Pages base.
 *
 * Why
 * ---
 * The service worker in demo/sw.js can only intercept requests inside its
 * own scope, and the default scope of a worker served at
 * /music-tag-web/sw.js is /music-tag-web/. But the bundle hardcodes
 * root-absolute paths — `api/client.ts` sets `baseURL: '/api/'` with no
 * runtime override, and LoginPage/PlayerBar build `/api/stream/...`,
 * `/media/...` the same way. Those resolve to the site ORIGIN's /api/,
 * which on a project Pages site is outside the worker's scope and 404s.
 *
 * So the deploy rewrites the literals. This is a deploy-time patch of a
 * build artifact — frontend/src is NOT touched.
 *
 * The assertions below are the point of this file. A string replace that
 * silently matches nothing would produce a site that looks fine and 404s
 * every request, with no error anywhere. Every expected replacement is
 * counted, and a miss fails the build.
 *
 * Usage: node demo/patch-bundle.mjs <distDir> <base>
 *   distDir  vite output directory
 *   base     Pages base path, e.g. /music-tag-web
 */

import { readdir, readFile, writeFile } from 'node:fs/promises';
import { join } from 'node:path';

const [, , distDir, base] = process.argv;

if (!distDir || !base) {
  console.error('usage: node patch-bundle.mjs <distDir> <base>');
  process.exit(2);
}
if (!base.startsWith('/') || base.endsWith('/')) {
  console.error(`::error::base must look like /music-tag-web (got "${base}")`);
  process.exit(2);
}

const assetsDir = join(distDir, 'assets');
let files;
try {
  files = await readdir(assetsDir);
} catch (err) {
  console.error(`::error::cannot read ${assetsDir}: ${err.message}`);
  process.exit(1);
}

const jsFiles = files.filter((f) => f.endsWith('.js'));
if (jsFiles.length === 0) {
  console.error('::error::no .js files in assets/ — did the build run?');
  process.exit(1);
}

/* Each entry: the literal as it appears in the bundle, and the minimum
 * number of occurrences we insist on seeing. The minimums are the current
 * observed counts, floored at 1 — if a future frontend refactor drops one
 * of these call sites, the build fails and a human re-checks whether the
 * demo still makes sense, instead of shipping a half-broken page. */
const REWRITES = [
  { from: '/api/', to: `${base}/api/`, min: 1 },
  { from: '/media/', to: `${base}/media/`, min: 1 },
];

const report = [];
let patchedAny = false;

for (const name of jsFiles) {
  const path = join(assetsDir, name);
  const original = await readFile(path, 'utf8');

  // Idempotency: a second run (or a half-applied previous run) must not
  // produce /music-tag-web/music-tag-web/... .
  if (original.includes(`${base}/api/`)) {
    console.error(`::error::${name} already contains ${base}/api/ — refusing to double-prefix`);
    process.exit(1);
  }

  let text = original;
  const perFile = [];
  for (const { from, to, min } of REWRITES) {
    const count = text.split(from).length - 1;
    if (count === 0) continue;
    if (count < min) {
      console.error(
        `::error::${name}: found ${count}× "${from}", expected at least ${min} — ` +
          'the frontend changed; re-check the demo fixtures',
      );
      process.exit(1);
    }
    text = text.split(from).join(to);
    perFile.push(`${count}× "${from}" → "${to}"`);
    patchedAny = true;
  }

  if (text !== original) {
    await writeFile(path, text);
    report.push(`  ${name}\n    ${perFile.join('\n    ')}`);
  }
}

if (!patchedAny) {
  console.error(
    '::error::no /api/ or /media/ literals were rewritten — the bundle no longer ' +
      'matches what this demo assumes. Refusing to publish a site that will 404.',
  );
  process.exit(1);
}

console.log(`Patched bundle under base "${base}":`);
console.log(report.join('\n'));
