// Download-source "ensure downloaded" utility.
//
// The user said:
//   "前端加flag，如果第一次点击播放就去下载覆盖，如果不是就复用"
// ("first click → download & overwrite, second click onward → reuse").
//
// Implementation:
//   - `downloaded: Set<string>` is *session-local* — it's not persisted to
//     localStorage on purpose so a fresh page reload triggers a re-fetch
//     (matches the in-memory "first click of a new session" semantic).
//   - `inFlight: Map<string, Promise<void>>` deduplicates concurrent
//     ensureDownload calls: a rapid double-click on PlayButton no longer
//     fires two POSTs /api/download/ — both callers await the
//     same promise. (Round-3 review caught this; the Set-based
//     downloading flag had a microtask race that allowed two onClick
//     handlers to enter the POST branch concurrently.)
//   - `ensureDownload(id)` POSTs /api/download/ only the FIRST
//     time per session; on subsequent calls it returns immediately.
//     The 202 returned by /api/download/ (task enqueued, may not
//     yet have written the file) is treated as "download started" — the
//     gateway /api/stream will glob for the file and return Failure
//     until yt-dlp finishes. The <audio>.error in PlayerBar will then
//     surface a per-source toast so the user knows what to retry.
//   - `isDownloaded(id)` is a pure predicate — caller is free to invoke
//     from getState() without subscription.
//
// Failure semantics: if the POST itself fails (4xx / network error),
// we DON'T add the id to `downloaded` — subsequent calls will retry
// the POST. The pre-existing <audio>.error path remains the user-facing
// error report.
//
// NOTE: The store is named `useYoutubeStore` because YouTube is the only
// registered DownloadSource today, but it posts to the generic
// /api/download/ endpoint with `source: 'youtube'` in the body — a
// future per-source sibling store (e.g. useSoundcloudStore) would post
// to the same /api/download/ with its own `source` value. The endpoint
// is unified; only the source identifier differs.

import { create } from 'zustand';
import axios from 'axios';

interface YoutubeState {
  /** video_ids that have had /api/download/ POST accept this session */
  downloaded: Set<string>;
  /** In-flight POST promises, keyed by video_id. Rapid double-clicks share
   *  one POST instead of firing concurrent duplicates. */
  inFlight: Map<string, Promise<void>>;
  /** First-call per session. POSTs /api/download/ (source=youtube), marks the id as "started" on accept. */
  ensureDownload: (videoId: string) => Promise<void>;
  /** Pure predicate — caller is free to invoke from getState() without subscription. */
  isDownloaded: (videoId: string) => boolean;
}

async function postDownload(videoId: string): Promise<void> {
  // The gateway Download handler returns a Success envelope
  // once the asynq task is enqueued. We accept any 200 envelope as
  // "started"; downstream /api/stream will retry the actual file
  // presence via its glob, and PlayerBar's <audio>.error covers the
  // "still not ready" toast path.
  await axios.post('/api/download/', { source: 'youtube', video_id: videoId });
}

export const useYoutubeStore = create<YoutubeState>((set, get) => ({
  downloaded: new Set<string>(),
  inFlight: new Map<string, Promise<void>>(),

  ensureDownload: async (videoId: string) => {
    if (!videoId) return;
    if (get().downloaded.has(videoId)) return;

    // Dedupe: if a POST is already in flight for this id, share its
    // promise rather than firing a duplicate POST.
    const existing = get().inFlight.get(videoId);
    if (existing) return existing;

    const promise = (async () => {
      try {
        await postDownload(videoId);
      } catch (cause) { // intentionally naming `cause` for clarity in the rethrow below
        // Drop the in-flight flag so a later click retries. Keep the id
        // OUT of `downloaded` so the next ensureDownload actually fires
        // another POST. The user-facing toast pipeline (PlayerBar.onMediaError)
        // surfaces the underlying reason on the audio element itself.
        set((s) => {
          const inflight = new Map(s.inFlight);
          inflight.delete(videoId);
          return { inFlight: inflight };
        });
        // Preserve the original error as `cause` so console / downstream
        // toasts can introspect the axios status / network reason.
        throw new Error(`download failed: ${videoId}`, { cause });
      }
      set((s) => {
        const inflight = new Map(s.inFlight);
        inflight.delete(videoId);
        const downloaded = new Set(s.downloaded);
        downloaded.add(videoId);
        return { inFlight: inflight, downloaded };
      });
    })();

    set((s) => {
      const inflight = new Map(s.inFlight);
      inflight.set(videoId, promise);
      return { inFlight: inflight };
    });
    return promise;
  },

  isDownloaded: (videoId: string) => get().downloaded.has(videoId),
}));
