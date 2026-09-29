// Background post-enqueue id3 fetcher.
//
// Why this exists: when a user batch-adds N songs via DirPickerDrawer's
// confirmation, useWorklistStore.enqueueDirs returns immediately with rows
// whose `musicInfo` is undefined. Without this hydrator the table renders
// raw filenames until each row is clicked — slow UX for any directory with
// > a handful of files.
//
// Wire shape:
//   1. POST /api/music_id3/ for each item (via readTagsFromPath → getMusicId3)
//   2. On SUCCESS WITH at least one non-null field → queue the write.
//   3. On EMPTY result or FAILURE envelope → skip the write, leaving
//      `musicInfo` undefined.
//   4. On NETWORK error → same as 3.
//
// Steps 3 and 4 exist for the boot hydration in useWorklistStore, which
// re-runs this helper for every persisted row where `needsMusicInfoRefetch`
// is true. A row left unwritten stays in that set, so a transient network
// failure retries on the next page load instead of pinning the row to a
// permanently empty cache. This is deliberate: empty-tag files are rare but
// real (raw compact discs, lossy files with stripped tags), and silently
// pretending we tried them would mask editing UX. (The old reason for this
// guard — the per-row click path's cacheHasAnyValue check — is gone; the
// detail dialog refetches on its own when the cover is missing.)
//
// WRITE AMORTIZATION — this is the reason `applyBatch` is not `setMusicInfo`.
// The store's per-row writer re-persists the WHOLE table on every call
// (`setMusicInfo` → `persistRows` → map + stripHeavy + JSON.stringify +
// localStorage.setItem over all N rows). Fanning N files through it is
// O(N²): measured at 4000 songs, that was 4001 setItem calls totalling
// 2.6 GB written to localStorage, blocking the main thread ~8.4 s — long
// enough that the whole page reads as frozen, which is what 「添加目录」
// looked like at scale. Writes are therefore collected and handed to the
// caller in chunks of `chunkSize` (plus a final partial chunk), so a batch
// costs ceil(N/chunk) store commits instead of N. The caller applies a
// chunk with one `set`, which is also one table render rather than N.
//
// The chunk is a behaviour change worth naming: tags now appear in waves
// rather than trickling in one row at a time. At the default chunk of 200 a
// 4000-song batch fills the screen in 20 passes rather than 4000, which is
// indistinguishable from "instantly" to a user and is what keeps the main
// thread free.
//
// Concurrency: bounded at 4 to avoid hammering the Go gateway when a user
// drops a 1000-file library. The bounded worker pool runs through a shared
// index (`next`) so each worker picks the next pending item; no per-worker
// task queue is needed, just N concurrent `while` loops. Writes are NOT
// gated on this bound — a chunk flushes as soon as it fills, whichever
// worker filled it, so a slow file can never strand a full chunk.
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

/** One row's worth of pending work, ready to hand to the caller. */
export interface HydratedTag {
  id: string;
  info: Partial<MusicTagInfo>;
}

/** How many rows to accumulate before flushing a chunk to the caller.
 *  200 was picked from the measurement above: a 4000-song batch becomes 20
 *  commits instead of 4000 (2.6 GB → ~20 MB), and no single chunk write is
 *  big enough to be felt as a stutter. Lowering it trades writes for a
 *  longer time-to-first-paint; raising it approaches the per-row cost this
 *  function exists to avoid. */
const DEFAULT_CHUNK_SIZE = 200;

/** Spawn a bounded-concurrency background reader. Writes back via the
 *  caller-supplied `applyBatch` (useWorklistStore.setMusicInfoBatch) so each
 *  store's persistence runs the normal Zustand update path — but once per
 *  chunk, not once per row.
 *
 *  A caller that only handles one row at a time can pass
 *  `(rows) => rows.forEach((r) => setMusicInfo(r.id, r.info))`; that is
 *  correct but reintroduces the O(N²) persist, which is why the store
 *  passes a real batch applier. */
export function hydrateTagsBatched(
  items: HydrateItem[],
  applyBatch: (rows: HydratedTag[]) => void,
  opts: { concurrency?: number; chunkSize?: number } = {},
): Promise<void> {
  if (items.length === 0) return Promise.resolve();

  const concurrency = Math.max(1, Math.min(opts.concurrency ?? 4, items.length));
  const chunkSize = Math.max(1, opts.chunkSize ?? DEFAULT_CHUNK_SIZE);
  let next = 0;

  // Single shared buffer. Workers are async but never interleaved
  // synchronously — no `await` sits between the read and the push — so a
  // plain array needs no lock.
  let pending: HydratedTag[] = [];

  function flush(): void {
    if (pending.length === 0) return;
    const chunk = pending;
    pending = [];
    applyBatch(chunk);
  }

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
          pending.push({ id, info });
          if (pending.length >= chunkSize) flush();
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
  )
    .then(() => {
      // The trailing partial chunk. Without this the last <chunkSize rows
      // of every batch would never reach the store.
      flush();
    })
    .then(() => undefined);
}
