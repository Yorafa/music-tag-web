// Background post-enqueue id3 fetcher.
//
// Why this exists: when a user batch-adds N songs via DirPickerDrawer's
// confirmation, useLibraryStore.enqueueDirs / useWorklistStore.enqueueDirs
// return immediately with rows whose `musicInfo` is undefined. Without
// this hydrator the table renders raw filenames until each row is
// clicked — slow UX for any directory with > a handful of files.
//
// Wire shape:
//   1. POST /api/music_id3/ for each item (via readTagsFromPath → getMusicId3)
//   2. On SUCCESS WITH at least one non-null field → call the writer.
//   3. On EMPTY result or FAILURE envelope → skip the write, leaving
//      `musicInfo` undefined.
//   4. On NETWORK error → same as 3.
//
// Steps 3 and 4 exist for the boot hydration in useWorklistStore /
// useLibraryStore, which re-runs this helper for every persisted row
// where `needsMusicInfoRefetch` is true. A row left unwritten stays in
// that set, so a transient network failure retries on the next page
// load instead of pinning the row to a permanently empty cache. This
// is deliberate: empty-tag files are rare but real (raw compact discs,
// lossy files with stripped tags), and silently pretending we tried
// them would mask editing UX. (The old reason for this guard — the
// per-row click path's cacheHasAnyValue check — is gone; the detail
// dialog refetches on its own when the cover is missing.)
//
// Concurrency: bounded at 4 to avoid hammering the Go gateway when a
// user drops a 1000-file library. The bounded worker pool runs through
// a shared index (`next`) so each worker picks the next pending item;
// no per-worker task queue is needed, just N concurrent `while` loops.
//
// Ownership: this helper is a fire-and-forget starter. The caller does
// `void hydrateTagsBatched(...)` and the promise resolves whenever
// the queue drains. We do NOT return partial counts back to the user
// — the toast at the directory-picker confirm layer is the only UX
// surface, and adding per-batch hydration toasts would just spam.

import type { MusicTagInfo } from '@/types';
import { readTagsFromPath } from '@/lib/id3Reader';

interface HydrateItem {
  id: string;
  fullPath: string;
}

/** Spawn a bounded-concurrency background reader. Writes back via the
 *  caller-supplied `setMusicInfo` (use*Store.setMusicInfo) so each
 *  store's persistence runs the normal Zustand update path. */
export function hydrateTagsBatched(
  items: HydrateItem[],
  setMusicInfo: (id: string, info: Partial<MusicTagInfo>) => void,
  opts: { concurrency?: number } = {},
): Promise<void> {
  if (items.length === 0) return Promise.resolve();

  const concurrency = Math.max(1, Math.min(opts.concurrency ?? 4, items.length));
  let next = 0;

  async function worker(): Promise<void> {
    while (next < items.length) {
      const idx = next++;
      const { id, fullPath } = items[idx];
      try {
        const info = await readTagsFromPath(fullPath);
        if (Object.values(info).some((v) => v != null)) {
          // Only write when we actually have at least one non-null
          // field. Leaving `musicInfo` undefined on an empty result
          // preserves the click-path retry semantics — see file
          // header §3.
          setMusicInfo(id, info);
        } else if (import.meta.env?.DEV) {
          // Don't toast: the click path will do that on the user's
          // intentional action. Diagnostic-only in dev so a regression
          // doesn't silently kill hydration for a whole batch.
          console.debug('[hydrate] empty parse for', fullPath);
        }
      } catch (err) {
        // Transport / parse error: silent deferral. Click path will
        // surface a toast on the user's intentional action. One
        // breadcrumb in dev keeps the failure observable.
        if (import.meta.env?.DEV) {
          console.debug('[hydrate] error for', fullPath, err);
        }
      }
    }
  }

  return Promise.all(
    Array.from({ length: concurrency }, () => worker()),
  ).then(() => undefined);
}
