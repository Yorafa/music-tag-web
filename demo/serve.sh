#!/usr/bin/env bash
# Build the Pages showcase locally and serve it.
#
# The site is the real frontend build; every /api/* request fails, which
# is the point — there is no backend on a static host and the showcase
# exists to be looked at. The only thing injected is a small script that
# puts a placeholder token in localStorage, because App.tsx renders
# LoginPage or HomePage and nothing else, so without it the visitor never
# sees the app.
#
# It still has to be served UNDER the /music-tag-web prefix, because that
# is where vite's --base puts the asset URLs. Serving dist-pages/ at the
# root gives you a page whose assets 404.
#
#   ./demo/serve.sh [port]     # default 8000
#
# Then open http://<host>:<port>/music-tag-web/ — no login, no credentials.
# Unlike the previous worker-based version this works over plain HTTP, so
# the LAN address works too.
set -euo pipefail

PORT="${1:-8000}"
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BASE="/music-tag-web"
cd "$ROOT"

echo "==> building frontend (vite, base=$BASE)"
(cd frontend && npx vite build --base="$BASE" --outDir=../dist-pages --emptyOutDir)

echo "==> injecting showcase entry script"
node demo/inject-demo.mjs dist-pages

echo "==> preparing docroot .pages-serve/$BASE"
mkdir -p .pages-serve
ln -sfn "$ROOT/dist-pages" ".pages-serve$BASE"

echo
echo "    http://127.0.0.1:$PORT$BASE/     (no login needed)"
echo "    Ctrl-C to stop"
echo
exec python3 -m http.server "$PORT" --bind 0.0.0.0 --directory .pages-serve
