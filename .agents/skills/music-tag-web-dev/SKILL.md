---
name: music-tag-web-dev
description: Architecture, conventions, and patterns for the Go Music Tag Web project — Go 1.23+gRPC backend, React 19+Vite+shadcn-ui frontend, music metadata editor with multi-source scraping.
version: 2.0.0
author: Hermes Agent
license: MIT
metadata:
  hermes:
    tags: [music-tag-web, go, react, shadcn-ui, gRPC, metadata-scraper]
    related_skills: []
---

# Go Music Tag Web — Development Guide

## Overview

Self-hosted Docker 化音乐元数据批量编辑工具。支持 FLAC/APE/WAV/AIFF/WV/TTA/MP3/M4A/OGG/MPC/OPUS/WMA/DSF/MP4
全部主流有损/无损格式。所有音乐文件本地处理，不上传第三方。作为 Navidrome/Jellyfin/Funkwhale 的 sidecar 服务。

**fork 自** `xhongc/music-tag-web`，从原 Python/Django 单体完整重构为 Go 1.23 + gRPC 微服务 + React 19 SPA 架构。

## When to Use

- Working on any part of the music-tag-web codebase (frontend or backend)
- Adding a new music source plugin (gRPC server)
- Modifying tag I/O, search fan-out, or batch operations
- Debugging the frontend UI layout, stores, or API integration
- Setting up the project locally for development
- Understanding the deployment architecture for docker compose

## Architecture

### Backend (root — Go 1.23)

| Layer | Implementation |
|---|---|
| HTTP service | Go 1.23 + gin (gateway: API + React SPA static + `/media/*` Range streaming) |
| Async tasks | asynq + Redis (worker) |
| Music sources | **gRPC microservices**: netease / kugou / kuwo / migu / qmusic / musicbrainz / acoustid — independent processes |
| Tag I/O | `bogem/id3v2` + `dhowden/tag` (pure Go, no FFI) |
| Auth | JWT in-memory + bcrypt; fail-closed defaults |
| Encryption | gRPC TLS optional (`GRPC_USE_TLS=1` + `GRPC_TLS_CA_FILE`) |
| Deploy | `docker compose up -d --build` starts gateway + worker + 8 gRPC plugins + redis, plus `fpcalc-base` (a build-only base image; its container does nothing) |
| Image size | gateway 131 MB, worker 126 MB, acoustid 115 MB, youtube 199 MB, six name-search plugins 21.5 MB each. gateway/worker/acoustid all `FROM` the shared `fpcalc-base`, so chromaprint's 88 MB of ffmpeg libraries is stored once |

### Frontend (`frontend/`)

- **Stack**: React 19, Vite 7, TypeScript, TailwindCSS v4, shadcn/ui (base-ui), Zustand, axios
- **UI primitives**: Components in `src/components/ui/` use `@base-ui/react` (Switch, Dialog, Tabs, Select)
- **API client**: `src/api/client.ts` — axios instance with JWT interceptors
- **Types**: `src/types/index.ts` — all shared TypeScript interfaces
- **Utils**: `src/utils/` — `persist.ts` (localStorage helpers), `cover.ts` (cover image handling), `string.ts` (string helpers), `path.ts` (path helpers)

### Zustand Stores

Each concern has its own store (NO single global store):

| Store | File | Purpose |
|---|---|---|
| `useAppStore` | `src/store/useAppStore.ts` | File browser, tag editor, search results, sort, id3 cache |
| `useAuthStore` | `src/store/useAuthStore.ts` | JWT token, login/logout, persisted to `auth.accessToken` |
| `usePlayerStore` | `src/store/usePlayerStore.ts` | Audio playback (track, queue, volume, seek), volume persisted |
| `useThemeStore` | `src/store/useThemeStore.ts` | Dark/light theme toggle |
| `useSourceStore` | `src/store/useSourceStore.ts` | Dynamic source list from `GET /api/sources/`, enabled sources |
| `useToastStore` | `src/store/useToastStore.ts` | Toast notification queue |

### Backend Directory Structure

```
cmd/
├── gateway/            # HTTP server entry point (gin)
├── worker/             # Asynq worker entry point
└── plugins/            # gRPC plugin servers
    ├── netease/ ├── kugou/ ├── kuwo/ ├── migu/
    ├── qmusic/ ├── musicbrainz/ └── acoustid/
internal/
├── config/             # Env-based config, fail-closed defaults
├── gateway/
│   ├── handler/        # Gin HTTP handlers (auth, file, update, tag, source...)
│   ├── middleware/      # CORS, JWT auth, webhook auth, body limit
│   └── router/         # Route setup (router.go)
├── plugin/
│   ├── interface.go    # TagSource + DownloadSource interfaces, Song type
│   ├── grpc_adapter.go # gRPC client dial + keepalive
│   ├── registry.go     # In-process plugin registry
│   └── <name>/         # Per-source server.go (gRPC implementations)
├── tasks/              # Asynq tasks (tidy, scanner, prune, yt_dl...)
├── tag/                # Tag I/O (reader.go, writer.go)
├── db/                 # GORM models + DB init
├── netguard/           # SSRF protection (ssrf.go)
└── utils/              # SafeJoin, path helpers
```

### Plugin Interface (`internal/plugin/interface.go`)

Two interfaces:

**TagSource** (music metadata):
- `Name()`, `DisplayName()` — identity
- `SupportsSearch()`, `SupportsLyric()`, `SupportsId3()`, `SupportsAudioURL()` — capability flags
- `Search(ctx, query, page, limit) (*SearchResult, error)` — paginated search
- `FetchID3ByTitle(ctx, title) ([]Song, error)` — top-N matches
- `FetchLyric(ctx, songID) (string, error)` — lyric text
- `GetAudioURL(ctx, songID) (string, error)` — stream URL

**DownloadSource** (audio download — YouTube etc.):
- `Name()`, `DisplayName()`
- `Search(ctx, query, maxResults) ([]DownloadItem, error)`
- `Download(ctx, videoID, downloadDir) (*DownloadResult, error)`

### API Routes (`internal/gateway/router/router.go`)

Route groups:

**Public:**
- `POST /api/token/` — login
- `POST /api/token/refresh/` — refresh JWT
- `POST /api/token/verify/` — verify JWT

**Webhook:**
- `POST /api/webhooks/file_moved/` — shared-secret auth

**Authenticated (JWT + body limit):**
- `POST /api/file_list/` — list files
- `POST /api/music_id3/` — read tag
- `POST /api/update_id3/` — write tag
- `POST /api/batch_update_id3/` — batch write
- `POST /api/batch_update_id3/` — auto-scrape + batch write（前端按目录分组提交）
- `POST /api/fetch_id3_by_title/` — scrape single song
- `POST /api/fetch_lyric/` — fetch lyrics
- `POST /api/tidy_folder/` — folder organize
- `POST /api/upload_image/` — upload cover
- `POST /api/search_music/` — multi-source search (fan-out)
- `POST /api/download/` — generic download enqueue (source-routed; class A 加入库)
- `GET /api/stream/` — audio proxy streaming (+ `?as_attachment=1` for browser download)
- `GET /api/sources/` — dynamic source list
- `GET /api/clear_celery/` — clear task queue
- `GET /api/active_queue/` — queue status
- `GET /api/full_scan_folder/` — full recursive scan

**Static:**
- `/media/*filepath` — Range streaming for audio
- `/static/*filepath` — Vite build assets
- `/` — React SPA entry (index.html)
- NoRoute — SPA fallback for client-side routing

### Response Format (`internal/gateway/handler/response.go`)

All API responses use a uniform JSON envelope:

```go
type APIResponse struct {
    Result  bool        `json:"result"`
    Code    string      `json:"code"`
    Data    interface{} `json:"data"`
    Message string      `json:"message"`
}
```

- `Success(c, msg, data)` → `{"result": true, "code": "200", "data": ..., "message": msg}`
- `SuccessData(c, data)` → msg defaults to `"success"`
- `Failure(c, msg)` → `{"result": false, "code": "400", "data": [], "message": msg}`

### Multi-Source Search API

```
POST /api/search_music/
Body: { query, sources[], pages{}, limit }
Response: { result, data: { songs[], pages{}, has_more{} } }
```

**Round-robin pagination**: Frontend sends `pages` (per-source page counts). Backend increments each source's page by 1, fetches, merges results. Returns updated `pages` + `has_more`. When `has_more` all false, stops.

**Source persistence**: Selected sources saved to `localStorage('search.sources')` via useSourceStore.

**Dynamic source list** (`GET /api/sources/`): Stage A of `docs/plugable-plugins.md`. Returns each registered source with capability flags. Frontend hydrates useSourceStore once on mount.

## UI Layout Architecture

### AppShell (`src/components/layout/AppShell.tsx`)

Section shell. The current section and sidebar state persist to
localStorage (`appShell.section`, `appShell.sidebarCollapsed`).

- **Sidebar** (desktop) / **nav drawer** (mobile): the 5 sections
- **Sections**: `PlayView` (library) · `WorkstationView` (scraper) ·
  `CloudSearchView` (search) · `AuditLogView` (audit) · `SettingsView`
- **TopHeader** + **PlayerBar**: persistent across every section
- **TrackDetailDialog**: the song-detail surface, mounted at root so a
  section switch does not unmount a half-edited dialog

### 智能刮削 (`src/components/workstation/`)

Two-column: `FileTreeBrowser` (left, draggable, persisted to
`workstation.leftWidth`) + `WorkstationToolbar` / `WorkstationTable`
(center).

**There is no song-detail column.** Detail is a dialog at every viewport
width — a row tap in 智能刮削 and in 音乐库 both call `openDetail()` and
get the identical surface. `selectWorklistRow` must not grow a viewport
branch: an earlier version did, which is how the phone and the desktop
ended up with two different detail UIs, each correct against its own
spec.

### Key Components

| Component | File | Purpose |
|---|---|---|
| `AppShell` | `layout/AppShell.tsx` | Section routing, sidebar, dialog host, PlayerBar |
| `Sidebar` / `TopHeader` | `layout/Sidebar.tsx`, `layout/TopHeader.tsx` | Nav + mobile drawer |
| `ResizeHandle` | `layout/ResizeHandle.tsx` | Draggable split bar |
| `PlayView` | `play/PlayView.tsx` | 音乐库: library table |
| `WorkstationView` | `workstation/WorkstationView.tsx` | 智能刮削: file tree + worklist |
| `selectWorklistRow` | `workstation/rowSelection.ts` | Row tap → select + `openDetail` |
| `TrackDetailDialog` | `detail/TrackDetailDialog.tsx` | The song-detail dialog, all widths |
| `TrackInspector` | `detail/TrackInspector.tsx` | 标签 / 候选 / 歌词 / 封面 / 指纹 + 智能刮削 |
| `CloudSearchView` | `search/CloudSearchView.tsx` | Cloud multi-source search |
| `AuditLogView` | `audit/AuditLogView.tsx` | Operation history |
| `SettingsView` | `settings/SettingsView.tsx` | Paths, sources, hot reload |
| `PlayerBar` | `player/PlayerBar.tsx` | Bottom playback bar |
| `DirPickerDrawer` | `scraper/DirPickerDrawer.tsx` | Directory picker (library or worklist) |

## Local Development

```bash
# Go backend
go vet ./...
go build ./cmd/gateway/ ./cmd/worker/
go run ./cmd/gateway/    # with JWT_SECRET, ADMIN_USERS etc. set

# Frontend
cd frontend
npm install
npm run dev              # HMR at http://localhost:5173
npm run build            # production build → ../static/dist/
npm run typecheck        # typecheck — MUST be `tsc -b`
npm run lint             # lint

# Docker full stack
docker compose up -d --build
```

> **Do not "simplify" `npm run typecheck` to `npx tsc --noEmit`.**
> `frontend/tsconfig.json` is a solution config — `{"files": [],
> "references": [app, node, test]}` — so invoking `tsc` against it directly
> type-checks **zero files** and exits 0. It looks like a passing
> typecheck and is not one. Only `tsc -b` follows the references and
> covers `app` / `node` / `test` (including `*.test.ts`). This is not
> hypothetical: a commit landed two missing imports through it, and the
> failure only surfaced when the Docker build ran `tsc -b`.
> To confirm the command still works, inject
> `const bad: number = someString;` and check it reports TS2322.

## UI Component Creation Pattern

When a shadcn component is missing:
1. Check `@base-ui/react` for a matching primitive (Switch, Dialog, Tabs, Select)
2. If none → native HTML element + Tailwind styling (e.g. Checkbox: `<input type="checkbox">` + `sr-only` + styled div with `peer` pattern)
3. Always `export { ComponentName }` and use `cn()` from `@/lib/utils`

## Common Pitfalls

1. **SMB/NFS symlink**: Repo root `./music` can be a symlink to external mount (e.g. `/volume1/music`); `docker compose` follows symlinks.

2. **JWT header prefix**: Frontend axios interceptor sends `Authorization: JWT <token>` (NOT `Bearer`). Go gateway's `middleware/auth.go::JWTAuth` recognizes both `JWT ` and `Bearer ` prefixes.

3. **gRPC keepalive**: `clientKeepaliveParams` has `PermitWithoutStream: true` so idle connections don't lose dead-peer detection. Plugin container restart → TCP RST → auto-redial.

4. **Plugin silent-fail**: kuwo and qmusic return 0 results + nil error on dirty upstream. Fan-out does NOT block other sources. First diagnostic: `grep '[SearchMusic] plugin'`.

5. **Frontend stores**: Each concern gets its own Zustand store. Do NOT merge into useAppStore. Add new stores for new features.

6. **Dialog nesting**: Never nest `<Dialog>` inside another `<Dialog>` — base-ui's DialogPrimitive.Root provider chain will silently unmount the inner React tree.

7. **Mobile responsive**: ScrapeView auto-collapses Toolbar below 640px via `window.matchMedia`. FileBrowser table columns wrap on small screens.

8. **Cover image handling**: Always use `resolveCoverSrc()` / `inspectCoverSrc()` from `src/utils/cover.ts` — they handle embedded base64, remote URLs, empty payloads, and oversized images.

9. **Path safety**: All user-supplied paths go through `internal/utils.SafeJoin(MUSIC_DIR, path)` for containment.

10. **SSRF guard**: `internal/netguard.Guard` rejects private IPs / 169.254 / loopback / multicast. All remote cover fetches pass through this.

11. **yt-dlp sanitize**: `internal/tasks/yt_dlp_validate.go` double-validates format/output_format/quality — once in gateway, once in worker (defense in depth).

12. **Player source types**: `PlayerTrack.source` uses a discriminated union — `{kind: 'local', fileName, filePath} | {kind: 'plugin', source, songId}`.

13. **cover data consistency**: `useAppStore.musicInfo.album_img` can be embedded art, remote URL, or base64. Always call `resolveCoverSrc()` before rendering.

14. **Pre-built frontend required**: First deploy MUST build frontend first (`cd frontend && npm install && npm run build`). Without `static/dist/`, gateway's `/` returns 404.

15. **First-boot bootstrap**: Lazymode generates secrets via `crypto/rand` on first start. Admin password appears ONCE in gateway startup log. Backup `.env` with explicit secrets to skip bootstrap.

## Verification Checklist

- [ ] `go vet ./...` passes
- [ ] `go build ./cmd/gateway/ ./cmd/worker/` succeeds
- [ ] `npm run typecheck` passes (frontend typecheck; `= tsc -b`)
- [ ] `npm run lint` passes
- [ ] `docker compose config --quiet` validates compose file
- [ ] `GET /api/sources/` returns 7+ entries
- [ ] JWT login works at `POST /api/token/`
- [ ] File listing works at `POST /api/file_list/`
- [ ] Search fan-out works at `POST /api/search_music/`
