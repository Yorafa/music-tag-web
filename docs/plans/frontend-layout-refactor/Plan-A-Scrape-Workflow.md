# Plan A — Scrape Workflow Rewrite

> **Owner:** You
> **Parallel counterpart:** `Plan-B-Play-Mobile-Visual.md` (mine — zero file overlap)
> **Source of truth:** `../../.scratch/frontend-layout-refactor/DESIGN.md`

## What you ship

A fully working scrape mode end-to-end. After your plan merges, the user can:

1. Click「添加音乐」on the scrape top bar → DirPickerDrawer opens → multi-select directories → confirm → files appear in the Worklist.
2. Filter by 全部/待刮/已刮/失败; multi-select / 全选 rows.
3. Trigger the existing batch tools (全盘扫描 / 增量扫描 / 整理文件夹) against the selected rows.
4. Open a row → mode-agnostic detail Dialog (TagEditor + ScrapeResults, lightly adapted) shows candidates → apply → status flips to `scraped`.
5. Failures show `failed` badge and are filterable.

## Why this is parallel-safe

- **Files you touch** (verified zero overlap with Plan B):
  - `src/store/useAppStore.ts` (decompose — see step 1)
  - `src/store/useBrowserStore.ts` (new)
  - `src/store/useEditorStore.ts` (new)
  - `src/store/useWorklistStore.ts` (new)
  - `src/store/useNoticeStore.ts` (renamed from useToastStore + remember)
  - `src/utils/expandDirs.ts` (new)
  - `src/utils/persist.ts` (add helpers if needed)
  - `src/components/editor/TagEditor.tsx` (adapt import source)
  - `src/components/editor/ScrapeResults.tsx` (adapt import source)
  - `src/components/files/FileBrowser.tsx` (delete at end)
  - `src/components/files/FileBrowser.test.tsx` (delete with it)
  - `src/components/layout/Toolbar.tsx` (decompose into the new ScrapeTopBar)
  - `src/components/layout/ScrapeView.tsx` (delete at end)
  - `src/components/search/SearchResults.tsx` (delete at end — current-dir audio list is gone)
  - `src/components/scraper/DirPickerDrawer.tsx` (new — SHARED with Plan B; you own it, B imports)
  - `src/components/scraper/ScrapeTopBar.tsx` (new — the tools row)
  - `src/components/scraper/Worklist.tsx` (new)
  - `src/components/scraper/WorklistHeaderBar.tsx` (new — add/filter/select)
  - `src/types/index.ts` (add `ScrapeStatus`, `WorklistRow`)

- **Files Plan B touches that you don't** (don't edit):
  - `useLibraryStore.ts`, `PlayView`, `PlayTopBar`, `DirPickerDrawer` **consumer only** (Beta)
  - `AppShell.tsx`, `LocalView.tsx`, `SearchPanel.tsx`, `ResizeHandle.tsx` (delete, owned by B)
  - `player/PlayerBar.tsx`, `PlayButton.tsx`, `pages/HomePage.tsx`, `common/NoticeHost.tsx`
  - `globals.css` (surface-1/2/3 tokens — owned by B; you just consume `--surface-1/2/3` variables once defined)

## Handshake contract with Plan B (β-side depends on α-side)

Plan B's `useLibraryStore.enqueueDirs` needs the same `expandDirsToAudioFiles` util — that lives in your territory and is the ONE shared util. **To enable true parallelism, commit this util on a feature branch FIRST and base both plans' branches off it.** That way B never blocks on you in tree, only in merge-order policy.

Similarly, Plan B's `PlayView` will open `DirPickerDrawer` — you build it with the published prop interface (below); B consumes it. Don't sneak mode-aware logic into the drawer.

### Shared `DirPickerDrawer` props (frozen contract — don't deviate without pinging B)

```ts
interface DirPickerDrawerProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  destination: 'library' | 'worklist';   // decides which enqueueDirs to call internally via useLibraryStore/useWorklistStore from inside the drawer
  existingDirs?: string[];                   // optional — for showing "already added" badges; you can ignore initially, B may pass
}
```

### Shared `ScrapeStatus` type (frozen — defined in `src/types/index.ts`)

```ts
export type ScrapeStatus = 'pending' | 'scraped' | 'failed';
export interface WorklistRow {
  id: string;        // FileNode.id cast to string; stable across re-add
  fullPath: string;  // relative to MUSIC_DIR
  fileName: string;
  status: ScrapeStatus;
  // cached tag preview (optional, lazy)
  musicInfo?: Partial<MusicTagInfo>;
}
```

## Step-by-step tasks

Each step is a checkpoint — verify before moving on. **Commit early and small.** Suggested cadence: one commit per step on your feature branch `feat/scrape-workflow`.

### Step 1 — Decompose `useAppStore` (foundation, PR1 of your line)

Rewrite `src/store/useAppStore.ts` into a thin "alias shim" file that imports-and-re-exports from the new stores, so Plan B-side in-flight readers don't break mid-refactor. THEN remove the alias from your consumers step by step.

1. Create `src/store/useBrowserStore.ts` with: `filePath`, `treeData`, `setFilePath`, `setTreeData`, `setIsLoading`. Persist `filePath` to localStorage via existing `readString/writeString` if currently persisted there (check current behavior first — likely not, in-memory only).
2. Create `src/store/useEditorStore.ts` with: `selectedFile`, `fullPath`, `musicInfo`, `musicInfoManual`, `showFields`, `setSelectedFile`, `setFullPath`, `setMusicInfo`, `updateMusicInfo`, `setShowFields`, `editorOpen`, `setEditorOpen`, `fadeShowDetail`, `setFadeShowDetail`, `songList`, `setSongList`. Mode-agnostic. Mode switch does NOT clear this state.
3. Set `useAppStore.ts` to re-export the union (so existing consumers keep working during transition). Then audit each consumer file (you can find them with `rg "useAppStore" src --type ts|sort`) and change the import source — touch ONLY the importer you're migrating in that commit, never break the tree state.
4. Run `npx tsc --noEmit` after each importer migration; the file tree must still typecheck.

**Verify:** `npx tsc --noEmit && npm run lint && npm run dev` — old UI still renders, scrape and play both still launch end-to-end.

### Step 2 — Rename `useToastStore` → `useNoticeStore`

1. `git mv src/store/useToastStore.ts src/store/useNoticeStore.ts`
2. Update the symbol name inside (interface `ToastState` → `NoticeState`, exported name `useToastStore` → `useNoticeStore`, file `Toast` type keeps name).
3. `rg "useToastStore\b" src/components/player/PlayButton.tsx src/components/player/PlayerBar.tsx src/components/common/ToastHost.tsx` — change import + call sites accordingly. (Note: PlayerBar / PlayButton are owned by Plan B; ping me before editing them, OR — preferred — keep `useToastStore` as a re-export shim from `useNoticeStore.ts` for the B-side that reads it, and let me clean it up from my side. **Pick: re-export shim approach** — you don't touch B's files at all.)

**Verify:** `npx tsc --noEmit && npm run lint`.

### Step 3 — Author `src/utils/expandDirs.ts` (shared util, commit early on a base branch)

1. Define `export const AUDIO_EXTS=['flac','ape','wav','aiff','wv','tta','mp3','m4a','ogg','mpc','opus','wma','dsf','mp4']`. Source of truth: `.agents/skills/music-tag-web-dev/SKILL.md`.
2. Implement `export async function expandDirsToAudioFiles(dirs: string[]): Promise<FileNode[]>` — for each dir, call `getFileList(p)` (existing `src/api/client.ts`), recursively flatten, filter by `AUDIO_EXTS` (case-insensitive, matched against file name extension), dedupe by `node.id`. Return ordered list.
3. Unit-test it — a 5-line jest/vitest sample if you have a test runner, otherwise write a small smoke test under `src/utils/expandDirs.test.ts`. (Check if vitest is set up before writing tests — if not, skip and rely on integration verification.)

**Verify:** Call site is wired in Step 4.

### Step 4 — Author `DirPickerDrawer` (SHARED component — your biggest shared contribution)

Path: `src/components/scraper/DirPickerDrawer.tsx`.

1. Renders as right-side drawer on desktop, bottom-sheet on mobile — use `position: fixed` + Tailwind transition + a body scroll-lock. Use the existing `cn()` from `@/lib/utils`.
2. Inside: directory tree (read from `useBrowserStore.treeData` — fetch via `getFileList(filePath)` on initial open with `filePath=''`), multi-select directories via checkbox per directory row, chips at the bottom showing selected paths with `✕` to remove, and a Confirm/Cancel button bar.
3. Confirm: call `useLibraryStore.enqueueDirs(selected)` if `destination='library'`, `useWorklistStore.enqueueDirs(selected)` if `destination='worklist'`. (Both store hooks' API must match the table below.) When the destination store doesn't exist yet (Plan B hasn't landed `useLibraryStore`), the drawer can guard via `useLibraryStore` optional import — but since you do own useWorklistStore, scrape-side e2e works from Step 5 onwards.
4. Close via Esc, mask click, mobile pull-down handle (swipe gesture or pull-handle button — your call).
5. **Subscribe only to the store fields you need** — selectors, not whole state, to keep Plan B's re-renders off you.

### Step 5 — Author `useWorklistStore`

1. File: `src/store/useWorklistStore.ts`. Fields: `rows: WorklistRow[]`, `selectedIds: string[]`, `filter: 'all'|'pending'|'scraped'|'failed'`, `enqueueDirs(dirs)` (async, uses `expandDirsToAudioFiles` + dedupe by `fullPath`), `toggleSelected(id)`, `selectAll()`, `clear()`, `setStatus(id, status)`, `setFilter(f)`. `remove(ids)`.
2. Wire to `useBrowserStore`'s tree only when needed (drawer uses it; Wstore just owns the Worklist rows). No localStorage persistence (Worklist is session-scoped — cleared on reload is fine for trial-scrape work).
3. Status update helpers wrap `POST /api/batch_auto_update_id3/` from `src/api/client.ts`; on success → `scraped`, on failure → `failed`. Both via the existing client.

### Step 6 — Build `Worklist` component + `WorklistHeaderBar`

1. `src/components/scraper/Worklist.tsx`: rows bound to `useWorklistStore`, each row shows cover thumbnail (from cached `musicInfo.album_img` passed through `resolveCoverSrc` — see skill pitfall #8), filename, status badge, kebab menu with three actions: play / detail / remove. Row click opens `setEditorOpen(true)` via `useEditorStore`.
2. `src/components/scraper/WorklistHeaderBar.tsx`: `[+ 添加音乐] [filter: all/pending/scraped/failed] [✓ 全选]`. Filter buttons toggle `useWorklistStore.filter`. 全选 toggles all `selectedIds`.
3. Status badges: `pending` = gray `bg-zinc-500/10`, `scraped` = green `bg-emerald-500/10`, `failed` = red `bg-red-500/10`. Use the same color tokens that already appear in `FileBrowser.tsx` state badges (consistency).

### Step 7 — Build `ScrapeTopBar` (the tools row)

1. `src/components/scraper/ScrapeTopBar.tsx` — extracts the three tool buttons from current `Toolbar.tsx` (全盘扫描 / 增量扫描 / 整理文件夹), keeps the tidy folder Dialog logic verbatim.
2. The tools operate on `useWorklistStore.selectedIds` instead of `useAppStore.checkedIds`. If no rows selected, buttons are grayed out with a tooltip "请先选择至少一行".
3. The整理文件夹 (tidy) Dialog logic moves here wholesale from `Toolbar.tsx`.

### Step 8 — Compose `ScrapeView` replacement + adapt consumers

1. New entry: `src/components/scraper/ScrapeMode.tsx` — vertical layout: `<ScrapeTopBar/>` on top (tools row), `<WorklistHeaderBar/>` next, `<Worklist/>` fills the rest. No more三栏 layout.
2. In `src/pages/HomePage.tsx`'s mode→render switch, the `'scrape'` branch renders `<ScrapeMode/>`. (Plan B will update HomePage's mode routing with you — pick a coordination point: simplest is B's PR reuses your `<ScrapeMode/>` import.)
3. Update `TagEditor.tsx` / `ScrapeResults.tsx` — change every `useAppStore` call to the new store (`useEditorStore` mostly), if it reads `musicInfo`/`songList`/`setMusicInfo` etc. This is the lion's share of editor-side work but each change is mechanical.

### Step 9 — Delete dead code

1. `git rm src/components/files/FileBrowser.tsx src/components/files/FileBrowser.test.tsx src/components/layout/Toolbar.tsx src/components/layout/ScrapeView.tsx src/components/search/SearchResults.tsx`.
2. `rg "FileBrowser|ScrapeView|Toolbar|SearchResults" src --type ts` — clean any lingering imports. If any are in Plan B's files (LocalView/AppShell), ping B author — those will be deleted by Plan B anyway.
3. Once all call sites of the `useAppStore` alias shim are gone, remove `src/store/useAppStore.ts`.

**Verify:** Full scrape e2e against a real backend with a couple of sample FLACs:
- 添加音乐 → drawer → multi-select 2 dirs → confirm
- Worklist shows N rows, all `pending`
- 全选 → 整理文件夹 (smoke, can cancel)
- 选一行 → 点行 → detail Dialog opens → apply a candidate → row status → `scraped`
- 批量自动刮削 with a target known to fail → row → `failed`
- Filter 全部/待刮/已刮/失败 toggles correctly

## What does NOT block you (don't wait)

- Plan B's `useLibraryStore`, `PlayView`, `NowPlaying` — completely independent. The drawer's `destination='library'` branch will no-op cleanly when `useLibraryStore` is absent (guard with conditional internal hook access if needed, or simply land Plan A first on a feature branch so Beta imports it from your branch base).
- Plan B's surface-1/2/3 tokens — you can stop using `bg-card/40 /30 /20` ad-hoc; layout-wise the absence of those classes won't break rendering (Tailwind won't paint a var that's undefined yet — page just looks unstyled for the consumer surface until B lands; the structure survives).

## Pitfalls

- **`FileBrowser.test.tsx` exists.** Check what it tests — if it pins the old DirectoryBrowser behavior, either rewrite tests for the new `DirPickerDrawer` semantics or delete with a note in the PR description. **Don't leave it broken.**
- **`useSourceStore`.** You might need it for scrape-side source list (`GET /api/sources/`). Confirm its current shape covers your needs; if not, extend it — DO NOT push scrape-source state into `useWorklistStore`.
- **Editor wiring.** `TagEditor` / `ScrapeResults` likely touch many `useAppStore` slices (musicInfo, songList, editorOpen, showFields, fadeShowDetail, etc.). They ALL move to `useEditorStore`. Migrating them is the bulk of your mechanical work; do it methodically, file-by-file, leave the running app able to typecheck after each.
- **Skill pitfall #6:** Never nest a Dialog in a Dialog. Your tidy Dialog sits inside ScrapeTopBar but the mode-agnostic detail Dialog lives at HomePage root (or AppShell substitute). They are sequential, not nested.
- **Skill pitfall #8:** Wlist rows showing album cover MUST go through `resolveCoverSrc()` from `src/utils/cover.ts`. Don't `album_img` directly.
- **Skill pitfall #12:** `PlayerTrack.source` is a discriminated union; the row "play" action needs to construct one of `{kind:'local', fileName, filePath}` from Worklist row's `fullPath/fileName`. Don't invent new play API.

## Done criteria (checklist)

- [ ] `npx tsc --noEmit` passes
- [ ] `npm run lint` passes
- [ ] `git grep "useAppStore\b" src` returns zero matches (the alias is gone)
- [ ] `git grep "useToastStore\b" src` returns zero matches
- [ ] Scrape e2e (add → filter → multi-select → batch → status → detail → apply) works against a running dev backend
- [ ] `git grep "bg-card/40\|bg-card/30\|bg-card/20" src/components/scraper/` returns zero matches (you stopped stacking opacity)
- [ ] Dead files (AppShell/ScrapeView/Toolbar/FileBrowser/SearchResults from your side) are deleted; `rg` for them in `src/` returns only Plan-B-owned files
