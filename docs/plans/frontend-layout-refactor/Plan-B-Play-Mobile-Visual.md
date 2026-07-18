# Plan B — Play Mode + Mobile + Visual

> **Owner:** Me (Hermes Agent)
> **Parallel counterpart:** `Plan-A-Scrape-Workflow.md` (yours — zero file overlap)
> **Source of truth:** `../../.scratch/frontend-layout-refactor/DESIGN.md`

## What I ship

A fully working local play mode end-to-end + mobile (mini PlayerBar + full-screen NowPlaying) + the visual token cleanup. After my plan merges, the user can:

1. Click「添加音乐」on the play top bar → open `DirPickerDrawer` (built by Plan A — I consume it via its published props) → multi-select directories → confirm → files get enqueued into `useLibraryStore` and shown as a table.
2. Click「搜索音乐」→ filter the table by query (in-memory against `useLibraryStore`).
3. Refresh the browser → the library is still there (persisted via `localStorage`).
4. Click a table row → PlayerBar starts playing it.
5. On mobile bottom of viewport, mini PlayerBar (~14 high); tap it → full-screen NowPlaying (cover + progress + prev/play/next); swipe down to dismiss.
6. Visual: opacity `bg-card/40 /30 /20` stack gone; surface-1/2/3 tokens written into `globals.css`; dark theme base is deeper near-black; light theme base is warm off-white.

## Why this is parallel-safe

- **Files I touch** (verified zero overlap with Plan A):
  - `src/store/useLibraryStore.ts` (new)
  - `src/components/play/PlayView.tsx` (new — successor to LocalView)
  - `src/components/play/PlayTopBar.tsx` (new — 添加音乐 / 搜索音乐)
  - `src/components/player/PlayerBar.tsx` (rewrite — add mobile mini + NowPlaying)
  - `src/components/player/PlayButton.tsx` (swap `useToastStore` for `useNoticeStore` — Plan A leaves a re-export shim during migration so this stays non-breaking)
  - `src/components/player/NowPlaying.tsx` (new — full-screen overlay)
  - `src/components/common/ToastHost.tsx` (rename to `NoticeHost.tsx` once Plan A's `useNoticeStore` lands; otherwise imports the shim)
  - `src/components/layout/AppShell.tsx` (delete)
  - `src/components/layout/LocalView.tsx` (delete)
  - `src/components/layout/ResizeHandle.tsx` (delete — play mode dropped resizable panels)
  - `src/components/search/SearchPanel.tsx` (delete)
  - `src/pages/HomePage.tsx` (rewire mode router to `<PlayView>` + `<ScrapeMode from Plan A>`)
  - `src/styles/globals.css` (or wherever Tailwind v4 tokens live — write surface-1/2/3, dim/deeper tokens)

- **Files I depend on from Plan A** (consume, don't edit):
  - `src/components/scraper/DirPickerDrawer.tsx` — I import with `destination="library"`.
  - `src/utils/expandDirs.ts` — my `useLibraryStore.enqueueDirs` uses the same `expandDirsToAudioFiles` util. I do NOT re-author it.
  - `src/store/useNoticeStore.ts` — `PlayButton` does notice pushing on play failures.
  - `src/store/useBrowserStore.ts` — DirPickerDrawer internals drive it; I read it indirectly via the drawer.

## Handshake contract with Plan A

The shared interfaces are published in `Plan-A-Scrape-Workflow.md` (the DirPickerDrawer props and the `ScrapeStatus` union; for play I also need `WorklistRow` shape mirrored as a library row). I freeze the contract here:

### `useLibraryStore` API (in my file)

```ts
interface LibraryState {
  rows: LibraryRow[];          // collected songs (deduped by fullPath)
  query: string;               // current in-memory search filter
  dirs: string[];              // collated, deduped list of directories the library was built from
  enqueueDirs(dirs: string[]): Promise<void>;  // expandDirsToAudioFiles + dedupe + persist
  search(s: string): void;
  remove(id: string): void;
  clear(): void;
  getFiltered(): LibraryRow[]; // rows filtered by query — used by PlayView
}
export interface LibraryRow {
  id: string;        // FileNode.id cast to string
  fullPath: string;  // relative to MUSIC_DIR — used for /media stream
  fileName: string;
  musicInfo?: Partial<MusicTagInfo>;   // lazy tag preview
}
```

Persist `rows` and `dirs` to `localStorage` under key `library.v1` via existing `readJson/writeJson` helpers from `src/utils/persist.ts`. On `enqueueDirs`: dedupe against existing `rows` by `fullPath` (Plan A's spec). On `clear()`: remove the key.

### `DirPickerDrawer` API

I consume it per Plan A's published props:

```ts
interface DirPickerDrawerProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  destination: 'library' | 'worklist';
  existingDirs?: string[];   // I pass `useLibraryStore.getState().dirs` to show already-added badges on dirs (optional — Plan A may ignore)
}
```

If Plan A's `DirPickerDrawer` doesn't yet support `existingDirs` when I start, I don't pass it (it's marked optional in the contract). I don't break the contract — both `existingDirs` usage and non-usage are valid.

### `ScrapeStatus` type

Plan A publishes `ScrapeStatus` in `src/types/index.ts`. I import it for the type but my store has no rows of scrape status — b store doesn't care about scrape states.

## Step-by-step tasks

Each step is a checkpoint. Commit early and small. Suggested cadence: one commit per step on my feature branch `feat/play-mobile-visual`.

### Step B1 — Write the surface-1/2/3 token foundation (CSS-first, structural)

I do CSS work first because Plan A (Plan B's mirror work) may consume `--surface-1/2/3` if it replaces the `bg-card/*` epsilon stack.

1. Locate the Tailwind v4 token file. Check `src/styles/globals.css`, `src/index.css`, `tailwind.config.ts` — whichever declares `@theme` / `:root` shadcn variables.
2. Under `:root` (light theme) add `--surface-1`, `--surface-2`, `--surface-3` color values. Suggested:
   - light: surface-1 = `oklch(0.99 0 0)` near-white warm, surface-2 = `oklch(0.97 0.001 60)` subtle warm, surface-3 = `oklch(0.995 0 0)` near-pure-white for dialog
   - dark: surface-1 = `oklch(0.18 0.005 270)` near-black, surface-2 = `oklch(0.22 0.006 270)` card, surface-3 = `oklch(0.25 0.007 270)` dialog
3. Make `bg-background`, `bg-card`, `bg-popover` mappings optionally alias to surface-1/2/3 by inline—for-now but DON'T rewrite the existing class names. Just add the new tokens so downstream consumers (Plan APlayView etc) can `bg-[var(--surface-1)]` or a Tailwind utility class.
4. Run dev site — confirm it boots, visuals look unchanged (the existing `bg-card/40 /30 /20` opacity stack still works).

**Verify:** `npm run dev` boots; `npm run lint` passes; visual unchanged.

### Step B2 — Author `useLibraryStore`

1. Write `src/store/useLibraryStore.ts` per the API above.
2. `enqueueDirs` kisses `expandDirsToAudioFiles` (Plan A's util) — but if Plan A's util isn't landed yet, stub it locally with a thin replica and add a TODO `// PLAN-A-INTEGRATION: replace local stub with shared expandDirsToAudioFiles from src/utils/expandDirs.ts`. **Critical:** I don't author `expandDirs.ts` myself.
3. Persist via `readJson` / `writeJson` from `src/utils/persist.ts`; key `library.v1`. On boot, hydrate synchronously from localStorage (`readJson` is sync).
4. `getFiltered()` returns `rows` filtered by `query` against `fileName` (case-insensitive substring) — simple in-memory filter. If `rows.length > 5000`, the filter loop is still fine (sub-10ms substring on 5000 entries is trivial).

**Verify:** Unit-import test — render a small harness component that enqueues two dirs and logs `rows.length` via the store's getState(); visual inspection suffices since vitest may not be set up.

### Step B3 — Author `PlayTopBar` + `PlayView`

1. `src/components/play/PlayTopBar.tsx` — a single horizontal bar with two buttons: 「添加音乐」 (opens `DirPickerDrawer` destination="library"), 「搜索音乐」 (toggles an input row revealing an `<input>` controlled search-or-list filter); the input writes to `useLibraryStore.search`.
2. `src/components/play/PlayView.tsx` — vertical layout:
   - `<PlayTopBar/>`
   - main: a list or table of `useLibraryStore.getFiltered()` rows.
3. Row rendering: show file name, duration if metadata exists, album cover thumbnail if `musicInfo.album_img` present (use `resolveCoverSrc` — skill pitfall #8). Row click → `usePlayerStore.playTrack(PlayerTrack{kind:'local', fileName, filePath: row.fullPath})` + the toast warn hook for missing source.
4. Empty state: when `rows.length === 0` render a centered "还没有音乐，点上方添加音乐按钮" message with a large pulse + 「添加」快捷 button.
5. Use `var(--surface-1)` / `--surface-2` from B1 for area backgrounds, NOT `bg-card/40`.

### Step B4 — Mobile mini PlayerBar + NowPlaying overlay

1. Rework `src/components/player/PlayerBar.tsx`:
   - Above 640px: existing horizontal PlayerBar stays as-is functionally, but uses `var(--surface-1)` / `--surface-2` for styling, not `bg-card/*`.
   - Below 640px: render `<div className="sm:hidden">` mini bar (~14 high, h-14) showing artist - title and a thin progress bar; tapping opens NowPlaying.
2. Author `src/components/player/NowPlaying.tsx` — a full-screen overlay (fixed inset-0 with `bg-[var(--surface-1)]` / `bg-foreground/95`):
   - Big cover at top center, square aspect, max 60vw or 90vw whichever smaller
   - Title + artist below
   - Seekbar (reuses the same range input pattern PlayerBar uses)
   - Prev / Play-Pause / Next buttons (pure icons)
   - **No queue area** (user-picked B2 = no queue)
   - Pull-down handle at top: a small horizontal bar with `cursor-grab`; the user can drag past 80px threshold to dismiss (or click handle button to dismiss). Downward swipe uses Pointer events.
3. Trigger: state local to PlayerBar `useState<boolean>(nowPlayingOpen)`; min render when no current track.
4. Hook teardown — if track changes while NowPlaying open, NowPlaying updates accordingly (it reads `usePlayerStore`). No special lifecycle.

**Verify:** Resize browser viewport below 640px; mini bar appears, tap expands; drag handle down; cover scales correctly; switching track reflects in NowPlaying without leaving the overlay.

### Step B5 — Rewire `HomePage.tsx` mode router

1. After Plan A has shipped `<ScrapeMode/>`: in `src/pages/HomePage.tsx`, the mode switch renders `<PlayView/>` for `mode='play'` and `<ScrapeMode/>` for `mode='scrape'` (imported from Plan A's `src/components/scraper/ScrapeMode.tsx`).
2. If Plan A hasn't shipped yet, the `'scrape'` branch keeps rendering `<AppShell/>` (old behavior) — branching `import {ScrapeMode} from '@/components/scraper/ScrapeMode'` lazy, OR wrapping in a try/catch dynamic import — best is: just wait for Plan A to land first on the dev branch before merging this into your branch. **Concrete approach:** rebase Plan B off the merged Plan A feature branch step by step.
3. Keep the `mode` persistence (`localStorage['appShell.mode']`) verbatim; the header at the top stays.

### Step B6 — Rename `useToastStore` → `useNoticeStore` on my side

Plan A's step 2 leaves a re-export shim from `useToastStore.ts` so my files keep importing `useToastStore` cleanly during migration. Now MY plan:

1. In `src/components/player/PlayButton.tsx` and `src/components/player/PlayerBar.tsx` and `src/components/common/ToastHost.tsx`: swap import + usage from `useToastStore` → `useNoticeStore`.
2. After all consumers (mine and Plan A's) migrated, BOTH plans coordinate to delete the `useToastStore.ts` shim. Plan A creates it; whoever migrates last (probably me, since Plan A migrated theirs) deletes the shim. **Plan A owns the deletion if it merges last; I own deletion if I merge last.** Coordinator convention: I leave a TODO PR comment when ready.

### Step B7 — Delete dead code from my side

After my views shipped and HomePage updated:

1. `git rm src/components/layout/AppShell.tsx src/components/layout/LocalView.tsx src/components/layout/ResizeHandle.tsx src/components/search/SearchPanel.tsx`
2. `rg "AppShell|LocalView|ResizeHandle|SearchPanel" src --type ts` — clean lingering imports (any left in Plan-A's files? Coordinate with Plan A if found — Plan A side expects to have migrated away from these by then).
3. After Step B6 fully done, if I am the last to migrate, delete `src/store/useToastStore.ts` shim.

**Verify:** `npx tsc --noEmit`, `npm run lint`, `git grep "bg-card/40\|bg-card/30\|bg-card/20" src/components/play src/components/player src/components/common` = 0 matches.

## What does NOT block me (don't wait)

- Plan A's `useWorklistStore`, `Worklist`, `ScrapeTopBar` — fully independent of `useLibraryStore`. I don't read or import them.
- Plan A's editor rewiring (`TagEditor`, `ScrapeResults`) — these sit in the mode-agnostic Dialog hosted by HomePage; I read the store but Plan A rewrites the import wiring. If Plan A hasn't migrated their `useEditorStore` consumers when I start PlayView, the Dialog still works because Plan A's `useAppStore` shim re-exports the runtime values.

I do wait (if necessary, but I shouldn't have to):

- Plan A to author `expandDirsToAudioFiles` BEFORE I land `useLibraryStore.enqueueDirs` integration. **Mitigation:** Step B2 stubs locally with a literal copy, marked by a TODO to swap. This way my PlayView starts working in parallel; I wire Plan A's real util once it lands on the shared base branch. The TODO has explicit content so my final merge swaps the stub out cleanly.

## Pitfalls

- **`DirPickerDrawer` SHARED components must follow Plan A's published props contract. If I add new props (like `existingDirs`), I MUST not change the contract unilaterally; I add optional fields only.**
- **PlayerBar rewrite is delicate.** Reading `usePlayerStore` state correctly (track, isLoading, currentTime, duration, volume, isPlaying) is a big surface; preserve all existing behaviors (volume slider, scrubbing, source-error toast). Don't lose functionality on desktop to ship mobile.
- **Mock mobile testing:** Use browser devtools device emulation (375x667 iPhone, 768x1024 iPad). Real touch via Chrome devtools is fine for gestures testing.
- **Skill pitfall #12:** `PlayerTrack.source` discriminated union — when the library row "play" action constructs a `PlayerTrack{kind:'local', fileName, filePath}`, don't add new fields or invent `{kind:'library'}` — reuse the local shape. If I new shape is needed, extend the union.
- **Skill pitfall #8:** Album cover images in library rows go through `resolveCoverSrc()` ONLY. Even cached thumbnails from localStorage (base64 strings) hit the same code path.
- **localStorage size.** If `useLibraryStore.rows` ever exceeds ~3MB serialized (≈ several thousand file rows × small tag cached), localStorage will hit the 5MB limit; surface a toast error on quota overflow (NOTE: quota error from setItem throws `QuotaExceededError` — catch in `writeJson`). Don't inadvertently store the full `musicInfo` in localStorage — only the leer preview (fileName, fullPath, duration-like, small album cover base64 if any).
- **Visual token powered (the oklch values in B1).** They're suggested; real visual review happens against a late-night Eval where I drop a did-reference-Linear screenshot and tweak from there. Values are NOT final ; "tune at implementation" (grilling default G).
- **Skill pitfall #6:** Now Playing is a separate screen from PlayerBar (a sibling DOM). The mini-bar mini-bar is the PlayerBar itself; NowPlaying is an overlay built directly. No Dialog-DomDialog nesting issues here.
- **Skill pitfall #15:** JWT bootstrap — irrelevant: my plan doesn't touch auth, so any first-boot secret rotating story is inert.

## Done criteria (checklist)

- [ ] `npx tsc --noEmit` passes
- [ ] `npm run lint` passes
- [ ] `git grep "useToastStore\b" src/components/player src/components/common` = 0 matches (Plan A's shim life)
- [ ] `git grep "useLibraryStore\b" src/components/play` > 0 matches (PlayView wired)
- [ ] Player e2e (add → list persist across reload → search → play row → PlayerBar → mobile: tap → NowPlaying → swipe-down to mini works) verified on 375px viewport
- [ ] `git grep "bg-card/40\|bg-card/30\|bg-card/20" src/components/play src/components/player src/components/common` = 0 matches
- [ ] `git grep "surface-1\|surface-2\|surface-3" src/styles` > 0 matches (tokens written)
- [ ] Dead files from my side (AppShell, LocalView, ResizeHandle, SearchPanel) all deleted; `rg` for them in src returns only Plan-A-territory migration leftovers (coordinate)
- [ ] Final visual review: dark theme looks cohesive with deeper base; light theme has warm off-white base; layer hierarchy readable; no opacity-stack residue in my component territory

## When both plans are done — final cleanup (cross-cutting)

After both A and B merged:

- Delete `src/store/useAppStore.ts` if any alias shim remained.
- Delete `src/store/useToastStore.ts` shim if any remained.
- Run `git grep "bg-card/40\|bg-card/30\|bg-card/20" src` — should be zero everywhere.
- Run `git grep "useAppStore\|useToastStore\|ScrapeView\|LocalView\|FileBrowser\b" src` — should be zero, all dead refs gone.
- Final visual review across both modes; look for token consistency issues between the two sides (Plan A might have used raw `bg-card` somewhere; sweep and align).

Coordinated by: in-code comments marked `// PLAN-A-INTEGRATION:` or `// PLAN-B-INTEGRATION:` for in-flight sync points. Each side updates the comment when done.
