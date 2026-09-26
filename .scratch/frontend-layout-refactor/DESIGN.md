# Frontend Layout Refactor — Design

Status: ready-for-agent
Created: 2026-07-17
Source: `/grilling` session — 16-question interview transcribed into decisions below.

## Motivation

Three pain points in the current frontend layout, in priority order:

1. **Scratch/local mode view reuse is awkward.** `ScrapeView` and `LocalView` each fork the same `ResizeObserver + rightWidth clamp + localStorage persist` boilerplate; the only real difference is which right-panel component they render and whether a Toolbar is present. Copy-paste was the de-facto pattern.
2. **Mobile (<640px) is bad.** The shared `Dialog` (base-ui) gets cramped on phones; the `PlayerBar` competes with the action bar + main list for vertical space; file rows wrap awkwardly (project pitfall #7).
3. **Visual quality is poor.** `bg-card/40 /30 /20` opacity stacking produces muddy layering; the shadcn default neutral grays read as flat; no consistent surface-token system.

The refactor does NOT introduce a new design system, a new component library, or any new backend API/DB schema. It is a pure frontend rewrite.

## Target Shape

Two modes fully decoupled. Each mode owns its own top action bar; both bars share the same `layout tokens` (height / padding / elevation / button styling) so the app still reads as one product. No shell component is shared between the modes — `AppShell`'s "switch on mode" indirection is retired.

### Local Play Mode (target: "Navidrome without sidebar")

- No persistent sidebar.
- Top floating action bar: `[+ 添加音乐] [搜索音乐]`.
- Main area: collected songs shown as a table / list.
- Bottom: persistent `PlayerBar`.
- 「添加音乐」 opens `DirPickerDrawer` (multi-select directories) → calls `useLibraryStore.enqueueDirs(dirs)`.
- The collected library is persisted to **`localStorage`** (z = "trial-listen library only, not full music management" — `localStorage` 5MB cap is sufficient; revisit `IndexedDB` only if it ever outgrows that).

### Scrape Mode

- No persistent `FileBrowser`.
- Main area = `Worklist`: every song added shows up here with a scrape status badge.
- Two bars stacked above the Worklist body:
  - **Upper (加工类 tools, single row):** 全盘扫描 / 增量扫描 / 整理文件夹 — these tools act on the **selected Worklist rows**.
  - **Lower (Worklist-header bar):** `[+ 添加音乐] [filter: 全部/待刮/已刮/失败] [全选]` — operates on the queue itself.
- 「添加音乐」 likewise opens `DirPickerDrawer` → calls `useWorklistStore.enqueueDirs(dirs)`.
- Scrape candidate review still flows through the **mode-agnostic detail Dialog** (existing `TagEditor` + `ScrapeResults` dual-panel layout is reused inside that Dialog).

## Store Decomposition

Current `useAppStore` is a 175-line catch-all mixing six concerns. It is retired; the 6 concerns are split into named stores. Existing global singletons (`useAuthStore`, `useThemeStore`, `usePlayerStore`) are untouched.

### Before → After

| Concern (current useAppStore slice) | Target store | Scope |
|---|---|---|
| `filePath`, `treeData`, `setFilePath`, `setTreeData` (directory browsing) | `useBrowserStore` (new) | mode-agnostic — used only by `DirPickerDrawer` now |
| `selectedFile`, `musicInfo`, `musicInfoManual`, `showFields`, `songList`, `fadeShowDetail`, `editorOpen` (detail Dialog state) | `useEditorStore` (new) | mode-agnostic — drives the detail Dialog |
| `checkedIds` (multi-select) | folded into `useWorklistStore.selectedIds` (scrape) and dropped from play mode | mode-private |
| `resource`, `sourceList` | stay on `useSourceStore` (existing) | scrape-private |
| `selectAutoMode`, `tidyFormData` | folded into `useWorklistStore` scrape-private state | scrape-private |
| `isLoading`, other UI flags | removed (call-site `useState`) | gone |
| `sortField`, `sortDir`, `id3Cache` | `useBrowserStore` (sort) + per-store id3 cache field (since play/scrape lists differ) | split |

### New mode-private stores

| Store | Purpose |
|---|---|
| `useLibraryStore` (new) | play mode — collected library directories + flattened files (persisted to `localStorage` via existing `readJson/writeJson` helpers). Actions: `enqueueDirs(dirs)`, `search(query)`, `remove(id)`, `clear()`. |
| `useWorklistStore` (new) | scrape mode — Worklist rows with per-row scrape status + selection. Actions: `enqueueDirs(dirs)`, `setStatus(id, status)`, `toggleSelected(id)`, `selectAll()`, `clear()`, batch-tool triggers (scan-full, scan, tidy). |

### Renames

- `useToastStore` → **`useNoticeStore`** — semantics point to "notification" (a queued info/warn/error broadcast), not to the specific toast-cap UI. All call-sites renamed; `ToastHost` continues to render.

### Editor (detail Dialog) state — keep mode-agnostic

The detail Dialog is rendered above both modes by `AppShell` today. After the refactor it is still rendered outside whichever mode body is active, so `useEditorStore` is a single shared store. **A1 pick: keep mode-agnostic** — `editorOpen` / `selectedFile` / `musicInfo` / `songList` live in one `useEditorStore`. The Dialog survives a mode switch without clearing its state, matching the current `AppShell` design intent.

(If cross-mode contamination becomes a real problem later, splitting into `usePlayEditor` / `useScrapeEditor` is a non-breaking follow-up.)

## Data Flow — Directory → Collected Songs

`DirPickerDrawer` returns the selected directory list `string[]` to its parent. The parent calls the destination store's `enqueueDirs(dirs)`, which fans out via a shared util:

```
src/utils/expandDirs.ts
  AUDIO_EXTS = ['flac','ape','wav','aiff','wv','tta','mp3','m4a','ogg','mpc','opus','wma','dsf','mp4']

  expandDirsToAudioFiles(dirs: string[]): Promise<FileNode[]>
    // For each dir, POST /api/file_list/ → flatten FileNode tree → filter by AUDIO_EXTS → dedupe by node.id

src/store/useLibraryStore.ts
  enqueueDirs(dirs)  => files = await expandDirsToAudioFiles(dirs)
                       push into library (dedupe; persisted to localStorage)

src/store/useWorklistStore.ts
  enqueueDirs(dirs)  => files = await expandDirsToAudioFiles(dirs)
                       push into worklist, each row stamped status: 'pending'
```

The **audio extension whitelist is unified across both modes** (C1 pick). Rationale: if play silently drops `.ape` while scrape collects it, users will be confused about "where are the files I added". Whether a file is *playable* is a PlayerBar concern (push a `'warn'` notice on unplayable stream), decoupled from the collection whitelist.

### `DirPickerDrawer` confirmation semantics (B pick — append + dedupe)

When the user opens the drawer a second time, selects `/music/foo` again and adds `/music/bar`:
- Already-added directories are silently skipped (no destructive clear, no prompt).
- Only genuinely-new directories are passed to `enqueueDirs`.
- A summary notice fires: "已收录 3 个新增目录、跳过 2 个重复".

### DirPickerDrawer UI defaults (subject to ratification during implementation)

- Selected directories visualize as a row of chips (`/music/foo ✕`) above the confirm button; `✕` deletes one row.
- Drawer shape: desktop right-side drawer / mobile bottom-sheet.
- Drawer close: Esc, mask click, and (mobile) pull-down handle past threshold.
- The parent passes `destination: 'library' | 'worklist'` — the drawer itself doesn't know which store.

## Mobile (< 640px)

- **Mini PlayerBar** pinned bottom (~14 high) — song title + thin progress bar.
- Tap the mini bar → slide up into a full-screen **`NowPlaying`** overlay: cover art + progress + prev / play / next.
- **No queue panel in `NowPlaying`** (B2 pick) — trial-listen library is small; queue concept has no value here.
- Downward swipe (or back button) dismisses `NowPlaying` back to mini.
- `DirPickerDrawer` on mobile renders as a bottom sheet — sufficient vertical room; solves the cramped-`Dialog` problem from pitfall #7.

## Visual

Approach **A** — keep `shadcn/ui (base-ui)` + `TailwindCSS v4`, do not switch stacks, do not import a Linear skin or any new design system. This is the user's explicit pick ("暂时保持这个风格也不差").

Fixes applied during the rewrite:

1. **Replace `bg-card/40 /30 /20` opacity stacking with explicit surface tokens** written into `globals.css`:
   - `--surface-1` — primary main-area background
   - `--surface-2` — cards / row hover
   - `--surface-3` — Dialog / popover / top layer
2. **Adjust dark theme**: deeper near-black base (e.g. `#0f0f11` for surface-1) instead of the shadcn default near-purple; brighter, higher-contrast text.
3. **Adjust light theme**: warm off-white base instead of clinical pure white.
4. Updated to also adjust `PlayerBar` height, list row font sizes, and spacing — these are tuned at implementation time against a reference (user can drop a screenshot or default to "shadcn dark variant"). Not pre-decided in this document.

## PR Plan (B pick — 4 staged PRs)

Order matters: each PR is independently mergeable and revertible.

### PR1 — Store foundation
- Decompose `useAppStore` → `useBrowserStore` + `useEditorStore` (+ fold `checkedIds` / `selectAutoMode` / `tidyFormData` into their owners).
- Create `useLibraryStore` and `useWorklistStore` skeletons (without UI consumers yet).
- Rename `useToastStore` → `useNoticeStore`; rename all call-sites.
- Add `src/utils/expandDirs.ts` with the audio whitelist.
- All existing UI components continue to work by reading from the split stores — no visual changes.
- **Verify:** `npx tsc --noEmit` passes; `npm run lint` passes; manual smoke of scrape + play flows identical to pre-refactor.

### PR2 — Scrape mode rewrite
- Delete `FileBrowser.tsx`, `ScrapeView.tsx`.
- Build `DirPickerDrawer`, `Worklist`, `ScrapeTopBar` (tools row + Worklist header bar).
- Wire to `useWorklistStore.enqueueDirs`, `setStatus`, status badge, filter UI.
- Reuse the detail Dialog (TagEditor + ScrapeResults) untouched.
- `LocalView` still runs on the split stores from PR1.
- **Verify:** scrape flow e2e (add dirs → Worklist populated → multi-select → batch scrape → status updates → open detail Dialog → apply candidate).

### PR3 — Local play mode rewrite
- Delete `LocalView.tsx`, `SearchPanel.tsx`, `AppShell.tsx`.
- Build `PlayView` + `PlayTopBar` («添加音乐» / «搜索音乐»).
- Wire to `useLibraryStore` (enqueue + localStorage persist + search).
- **Verify:** play flow e2e (add dirs → library persisted across reload → search → table click → PlayerBar plays).

### PR4 — Mobile + visual pass
- Mini PlayerBar + full-screen `NowPlaying` overlay + swipe-to-dismiss.
- Mobile bottom-sheet variant of `DirPickerDrawer`.
- `surface-1/2/3` CSS variables written into `globals.css`; remove the `bg-card/40 /30 /20` stack; adjust dark/light theme base color values.
- **Verify:** mobile viewport (<640px) mock in browser dev tools; surface tokens consistent; no `bg-card/*` opacity leftovers in `rg`.

## Open Details (ratify or override at implementation)

These are the grilling defaults; if you disagree, raise the concern when the relevant PR is being authored.

| # | Default |
|---|---|
| A | Selected dirs in `DirPickerDrawer` shown as chips above confirm button. |
| B | Append + dedupe on re-add; skipped dirs reported via notice summary. |
| C | Click a Worklist row → open mode-agnostic detail Dialog (no in-place expansion). Row-trailing kebab menu: play / detail / remove. |
| E | `useLibraryStore` persistence = `localStorage` (5MB cap fine for trial-listen scale); upgrade to `IndexedDB` only if it ever outgrows that. |
| F | Drawer close = Esc + mask click + (mobile) pull-down handle past threshold. |
| G | Specific `surface-1/2/3` color values tuned against a user-supplied reference screenshot or "deeper shadcn dark" default at implementation time. |

## Worklist Row Status (D pick — three-value enum)

```
type ScrapeStatus = 'pending' | 'scraped' | 'failed';
```

- `pending` — fresh into the Worklist; nothing done.
- `scraped` — at least one scrape action (batch auto, manual apply from candidate) wrote the tag successfully.
- `failed` — a batch auto-scrape attempt returned failure for this file.

Filter UI: «全部 / 待刮 / 已刮 / 失败» — `已刮` covers `scraped`; `失败` shown explicitly so failures can be reviewed and retried.

Status transition triggers:
- Batch `POST /api/batch_update_id3/` (grouped per directory) success → set `scraped`.
- Batch `POST /api/batch_update_id3/` failure → set `failed`.

The design originally named `POST /api/batch_auto_update_id3/` (an asynq
worker task) for these two transitions. That endpoint was removed: the worker
read its worklist from `task_taskrecord`, but nothing ever wrote rows there, so
it processed 0 files per batch, and the frontend never called it. The
synchronous per-directory `batch_update_id3/` is what actually runs.
- Manual candidate apply from detail Dialog's `ScrapeResults` → set `scraped` regardless of source (any successful write marks `scraped`, so the user's mental model "已刮 = 写成功了" always holds).

If the Worklist later moves to fully asynchronous scrape via the asynq worker, an intermediate `'scraping'` value can be appended (non-breaking, additive).

## Retired Files

After all four PRs merge:

- `frontend/src/components/layout/AppShell.tsx`
- `frontend/src/components/layout/ScrapeView.tsx`
- `frontend/src/components/layout/LocalView.tsx`
- `frontend/src/components/layout/Toolbar.tsx` (replaced by the scrape-mode top tools row; old tidy-dialog logic moves into the Worklist tool row)
- `frontend/src/components/files/FileBrowser.tsx`
- `frontend/src/components/search/SearchPanel.tsx`
- `frontend/src/components/search/SearchResults.tsx` (scrape side no longer uses a right-panel "current dir audio" list; detail Dialog's ScrapeResults is unaffected)
- `frontend/src/store/useAppStore.ts`
- `frontend/src/store/useToastStore.ts` (renamed to `useNoticeStore.ts`)

## Retained Files (unchanged or minimally touched)

- `frontend/src/components/editor/TagEditor.tsx` — adapted to read from `useEditorStore` instead of `useAppStore`.
- `frontend/src/components/editor/ScrapeResults.tsx` — same.
- `frontend/src/components/player/PlayerBar.tsx` — gains a mobile mini-bar + NowPlaying expansion in PR4.
- `frontend/src/components/player/PlayButton.tsx` — call-sites switch `useToastStore` → `useNoticeStore`.
- `frontend/src/components/common/ToastHost.tsx` — renamed `NoticeHost.tsx` (or keep filename; only the import path changes).
- `frontend/src/pages/HomePage.tsx` — header + mode switch stays; just switches which mode view it renders.

## Out of Scope for This Refactor

- Backend: no new API endpoints, no DB schema changes, no GORM models, no scanner task additions.
- Adding "album / artist / playlist" indexed library views (the full Navidrome feature set) — requires backend index, deferred. The local play mode stays file-directory-driven.
- YouTube download flow — preserved as-is, no rewrite.
- gRPC plugin architecture — untouched.
