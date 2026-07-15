# Pluggable Plugin Architecture — Design Doc

**Status**: Design (Stage A pending implementation)
**Audience**: self-hosted family media-center use, single trusted user
**Date**: 2025-06-29

> **Cross-ref**: This doc's Stage B (per-source YAML overrides), Stage C (sandboxed JS plugins via `dop251/goja`), and Stage D (runtime admin UI for plugin lifecycle) are **future-feature pitches** — they are NOT currently in any README claim set. Separately, [`docs/FEATURE-COVERAGE.md`](docs/FEATURE-COVERAGE.md) § Deferred/aspirational lists README claims that exist *today* but are not implemented in code. The two sets are *conceptually adjacent* (both aspirational, both off the current roadmap) but **distinct**:

---

## 1. Goal & non-goals

**Goal**: A pluggable plugin system for "music sources" (search + preview + lookup) and
"tag sources" (metadata + lyrics) that allows adding a new source **without touching frontend
code**, and ultimately without requiring a Go toolchain.

**Non-goals**:
- Public plugin marketplace or hosted registry
- Sandboxing arbitrary untrusted third-party code (user is trusted, single-tenant)
- Replacing backend — pure-frontend is infeasible for the major CN music platforms

## 2. Hard facts that shape this design

Web research (2025-06) confirms:

| Source | CORS open? | Stable browser-only lib? | Verdict |
|---|---|---|---|
| NetEase | ❌ | ❌ (WEAPI AES obfuscation) | Backend required |
| QQ Music | ❌ | ❌ (g_tk signed cookies) | Backend required |
| Kugou | ❌ | ❌ | Backend required |
| Kuwo | ❌ | ❌ | Backend required |
| Migu | ❌ | ❌ | Backend required |
| MusicBrainz | ✅ | ✅ | Could be browser-only |
| AcoustID | ✅ | ✅ | Could be browser-only |
| YouTube | n/a | n/a | iframe-only playback |

**Implication**: Pure-frontend plugin architecture is structurally blocked for 5 of 7 sources.
A backend component is non-negotiable; the design axis is **how pluggable the backend exposes
its plugins to the frontend**.

## 3. The customizability spectrum

We evaluated a 2-axis space:

- **L** = customizability level (what can user change)
- **P** = platform shape (where does plugin code live / run)

|             | P1 pure-frontend | P2 backend signing-proxy | P3 compile-time go plugin | P4 Go `.so` plugin                     | P5 WASM serverless          |
| ----------- | ---------------- | ------------------------ | ------------------------ | ------------------------------------- | --------------------------- |
| **L0** built-in only | 🚫 CORS | ✅ baseline | ✅ current | ✅ | ✅ |
| **L1** per-source API key / URL override | 🚫 | ✅ trivial | ✅ | ✅ | ✅ |
| **L2** JSON URL-template extractor | 🚫 | 🚫 5 encrypted sources fail | ✅ | ✅ | ✅ |
| **L3** user-authored JS plugin | 🚫 | ✅ sandbox via goja | ✅ | ✅ | ✅ |
| **L4** user-authored Go plugin | 🚫 | ✅ | ✅ | ⚠️ Linux-only dep hell | ✅ |
| **L5** user-authored WASM plugin | 🚫 | ✅ | ✅ | ✅ | ⚠️ asks WASM toolchain |

🚫 = blocked.  ✅ = viable.  ⚠️ = fragility / heavy lift.

**Recommended path**: L3 × P2 (sandboxed JS plugins executing inside the Go backend), reached
through:

- **Stage A** (today): L0 + P2 — expose existing built-in plugins to the frontend with a
  capability-aware dynamic source list.
- **Stage B** (later): L1 — per-source config override file (`data/sources/*.yaml`).
- **Stage C** (later): L3 — user-authored JS plugins executed in `dop251/goja` runtime.

This doc covers **Stage A** in full detail. Stages B/C sketched at the end.

---

## 4. Scope of Stage A

### 4.1 Why Stage A and not larger steps

Stage A intentionally stops at *"expose existing built-in plugins to the frontend"*:

1. Stage A severs the hardcoded coupling between frontend (hardcoded source strings in
   `SearchPanel.tsx`, `SourcePickerModal.tsx`) and backend (hardcoded `sourcesDefault` slice in
   `handler/tag.go`).
2. Once Stage A is in, *adding any new source* still requires Go code, but **requires zero
   frontend changes** — the new source will appear automatically because the frontend renders
   whatever list the backend returns.
3. Stage A is **infrastructure** for both Stage B (per-source config) and Stage C (user-defined
   JS plugins), so it earns its keep even if we stop here.

### 4.2 Storage policy (locked)

Layering of source-of-truth (single trusted user, self-host):

| Layer                   | Stores                                                              | Lifetime             |
| ----------------------- | ------------------------------------------------------------------- | -------------------- |
| Backend registry        | registered `TagSource` + `DownloadSource` instances                 | process, init-time   |
| Backend `GET /sources` | union of registry metadata                                          | per request          |
| Frontend localstorage  | `app.enabledSources: string[]` (set of currently-enabled source keys) | browser-local        |
| Frontend search request | `enabled_sources: string[]` in body; `null` = all-on                | per request          |
| Backend persistence     | **none**                                                            | —                    |

**Default behavior** when `localStorage["app.enabledSources"]` is missing or empty:
- Treat as **all-on** (no filter sent in search body).
- Backend `SearchMusic` fans out across **every** registered `TagSource`, overriding the hardcoded
  legacy `sourcesDefault`.
- This replaces the current hardcoded preference for `qmusic,netease,kugou,migu,musicbrainz`.

### 4.3 API contract

**Endpoint**: `GET /api/sources/`

**Auth**: behind the existing `authed` Gin router group (same as other endpoints).

**Response**: JSON array, ordered. Each element:

```jsonc
{
  "name":         "netease",                       // plugin key (used as identifier)
  "displayName":  "网易云音乐",                     // human label, may contain CJK
  "kind":         "tag",                          // "tag" | "download"
  "searchable":   true,                           // SupportsSearch()
  "lyric":        true,                           // SupportsLyric()
  "preview":      false,                          // future: SupportsPreview()
  "defaultOn":    true                            // server advisory; frontend honors user toggle
}
```

**Search request**: existing `POST /api/search_music/` body gains one optional field:

```
"sources": ["netease", "qmusic"]   // optional; absent = all-on
```

**Backward compatibility**: existing callers that omit `sources` keep working; backend interprets
omission as "fan out to all registered tag sources".

---

## 5. Concrete file-level change list

### 5.1 Backend

| File | Change | Lines (approx) |
|---|---|---|
| `internal/gateway/handler/source.go` | **new file**: define `SourceInfo` struct + `ListSources(c)` handler that iterates `plugin.ListTagSources()` + `plugin.ListDownloadSources()` | +60 |
| `internal/gateway/router/router.go` | mount `authed.GET("/sources/", handler.ListSources)` | +1 |
| `internal/gateway/handler/tag.go` | delete `sourcesDefault` constant; `SearchMusic` accepts optional `sources []string` field; empty/missing → fan out to all registry entries | −15 / +10 |

Total backend delta: ~75 lines, additive, no interface changes, no proto regen required.

### 5.2 Frontend

| File | Change | Lines (approx) |
|---|---|---|
| `frontend/src/types/index.ts` | add `SourceInfo` TypeScript type | +12 |
| `frontend/src/api/client.ts` | add `getSources(): Promise<SourceInfo[]>` API call | +8 |
| `frontend/src/store/useSourceStore.ts` | **new file**: Zustand store; `sources`, `enabled: Set<string>`, `loadSources()`, `toggle(name)`, `persist` writes to `localStorage["app.enabledSources"]` | +60 |
| `frontend/src/components/search/SearchPanel.tsx` | delete hardcoded `SEARCH_SOURCES` / `SOURCE_COLORS` / `VALID_SOURCES`; mount `useSourceStore.loadSources()`; thread `enabled` set into `searchMusic()` request body | −40 / +30 |
| `frontend/src/components/search/SourcePickerModal.tsx` | replace hardcoded chip list with sources read from store; respect `kind` field to render tag vs download rows | −20 / +20 |
| `frontend/src/components/settings/` (new folder) | new minimal page: per-source toggle row with `searchable` / `lyric` badges | +50 |

Total frontend delta: ~200 lines (mostly bookkeeping).

### 5.3 Verification commands

```sh
# Backend
go build ./...                             # should stay clean
go vet ./...                               # new endpoint type-checks
# Manual: curl http://localhost:8000/api/sources/  → list of 7 tag + 1 download plugin

# Frontend
cd frontend && npx tsc --noEmit -p tsconfig.app.json      # should stay clean
cd frontend && npx eslint src/                           # new code passes lint
# Manual: open browser, mount SourcePickerModal, see chip count == backend source count
```

---

## 6. Acceptance criteria

After Stage A ships:

1. ✅ GET /api/sources returns ≥7 entries (5 tag sources + acoustics + 1 download source).
2. ✅ Removing `netease` from localStorage `app.enabledSources` and refreshing the page
   hides the "网易云音乐" chip in `SourcePickerModal`.
3. ✅ With empty `app.enabledSources`, search fans out to **all** registered plugins (no
   `sources` field in request, backend default = full fan-out).
4. ✅ Hardcoded `sourcesDefault` constant is gone from `handler/tag.go`.
5. ⚠️ Adding a new built-in plugin (e.g., writing `internal/plugin/example/server.go` and
   registering it via `init()`) appears automatically in `SourcePickerModal` after a gateway
   restart — confirming "add-source = frontend-zero-edit" goal.

---

## 7. Failure modes & mitigations

| Failure | Mitigation |
|---|---|
| Plugin gRPC client cold-starts → first multi-source search is slow (~5s) | Document; warm pool deferred to Stage C |
| User disables all sources → search returns empty | UX: empty state with "all sources disabled" hint linking to Settings |
| localStorage cleared → all sources re-enabled (back to default) | Acceptable (self-host, single user); document in Settings UI |
| Future plugin adds a third `kind` ("album-art", "playlist") | Backend `kind` is `string`, not enum — wire-compatible |

---

## 8. Future stages

### Stage B — per-source config override (L1)

`data/sources/<name>.yaml` per source: API key overrides, region URL overrides, default-on flag.
Loaded by gateway at startup, **overrides** plugin constructor values. Self-host users edit
these by hand. ~½ day.

### Stage C — user-authored JS plugins (L3)

`data/source_plugins/<name>.js` per user source. JS exports `meta`, `search(ctx, q, page, limit)`,
`fetchId3ByTitle(ctx, title)`, `fetchLyric(ctx, id)`. Backend uses `github.com/dop251/goja`
runtime + injected `fetch` / `crypto` / `console` / `setTimeout` bridge → calls user code
through Go's http.Client (CORS handled at gateway boundary). 5s per-call timeout. ~1-2 weeks.

### Stage D — runtime admin UI for plugin lifecycle

Web UI to enable/disable/edit/test plugins without restart. ~½ day.

---

## 9. Open questions (decide later)

- **Stage C security budget**: trusted family user → no restrictive sandbox needed; but should
  we still timeout / cap memory per plugin? Lean: yes, 5s + 256MB cap.
- **Per-source logging**: should Stage A expose per-source health stats in `GET /api/sources`?
  Lean: defer to Stage D.
- **MusicBrainz / AcoustID browser-only path**: since CORS is open, could we run them in the
  frontend at the registry level and skip the backend for those two specifically? Lean: no
  — keeps the architecture uniform; revisit later.

---

## 10. Definition of Done for Stage A

- [x] This doc written + reviewed.
- [ ] `docs/plugable-plugins.md` committed.
- [ ] Backend `handler/source.go` + router + tag.go edits merged → `go build` clean.
- [ ] Frontend `useSourceStore.ts` + `SourcePickerModal` + `SearchPanel` + Settings page merged
      → `tsc` clean.
- [ ] Manual e2e: add `sources/netease.js` placeholder (Stage C scaffolding) appears as disabled
      chip in UI without frontend rebuild.
- [ ] `sourcesDefault` constant deleted.
