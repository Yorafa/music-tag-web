#!/usr/bin/env bash
# Build the Pages demo locally and serve it.
#
# The bundle is patched to /music-tag-web/... and the service worker can
# only intercept requests inside its own scope, so the site has to be
# served UNDER that prefix. Serving dist-pages/ directly gives you a blank
# page with a bundle asking for /music-tag-web/assets/... that 404s. That
# is what the symlink below is for.
#
#   ./demo/serve.sh [port]     # default 8000
#
# Then open http://127.0.0.1:<port>/music-tag-web/ — log in with any
# username and password, /api/token/ is mocked.
#
# Use 127.0.0.1, not your LAN IP: Service Workers require a secure
# context, and plain HTTP over the LAN does not qualify. The page tells
# the visitor so instead of just failing the login.
set -euo pipefail

PORT="${1:-8000}"
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BASE="/music-tag-web"
cd "$ROOT"

echo "==> building frontend (vite, base=$BASE)"
(cd frontend && npx vite build --base="$BASE" --outDir=../dist-pages --emptyOutDir)

echo "==> rewriting /api/ and /media/ literals into the bundle"
node demo/patch-bundle.mjs dist-pages "$BASE"

echo "==> injecting the service worker registration"
node demo/inject-sw.mjs dist-pages "$BASE"

echo "==> preparing docroot .pages-serve/$BASE"
mkdir -p .pages-serve
ln -sfn "$ROOT/dist-pages" ".pages-serve$BASE"

echo
echo "    http://127.0.0.1:$PORT$BASE/     (any username/password)"
echo "    Ctrl-C to stop"
echo
exec python3 -m http.server "$PORT" --bind 0.0.0.0 --directory .pages-serve
