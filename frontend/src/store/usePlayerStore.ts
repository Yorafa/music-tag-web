// Cross-mode player state. Lives in its own Zustand store so the audio
// element, transport bars, and inline row buttons all share one source of
// truth. Mounted in <PlayerBar> at the bottom of AppShell, but consumed
// from anywhere a row wants to start a preview (FileBrowser / SearchPanel /
// SearchResults).

import { create } from 'zustand';
import { readNumber, writeNumber } from '@/utils/persist';

/** Discriminated union for "where this track came from". Used by future
 *  download hooks to route local-file rows to write-through-tag plugins
 *  and external rows to the search-source download path. */
export type PlayerSource =
  | { kind: 'local'; fileName: string; filePath: string }
  | { kind: 'plugin'; source: string; songId: string };

export interface PlayerTrack {
  id: string;
  /** Absolute URL the <audio> element loads. Empty string disables play. */
  url: string;
  title: string;
  artist: string;
  cover?: string;
  durationSec?: number;
  source: PlayerSource;
}

interface PlayerState {
  currentTrack: PlayerTrack | null;
  isPlaying: boolean;
  currentTime: number;
  duration: number;
  queue: PlayerTrack[];
  /** 0..1. Persisted across page reloads so the user doesn't lose mix on
   *  tab-nap. NOT persisted to localStorage on every render — debounced by
   *  PlayerBar's setter wrapper. */
  volume: number;
  error: string | null;

  /** Start playing a new track. Replaces currentTrack; resets currentTime
   *  to 0 and (optionally) seeds the queue for next/prev navigation. */
  playTrack: (track: PlayerTrack, opts?: { queue?: PlayerTrack[] }) => void;
  togglePlay: () => void;
  pause: () => void;
  resume: () => void;
  /** Seek (seconds). Updates currentTime so the slider thumb follows; the
   *  <audio> element is brought in sync by the PlayerBar's slider
   *  onChange, not by a reactive effect (avoids a loop with onTimeUpdate). */
  seek: (sec: number) => void;
  setVolume: (v: number) => void;
  next: () => void;
  prev: () => void;

  // Mirrors from native <audio> events. Don't call from UI directly.
  setCurrentTime: (sec: number) => void;
  setDuration: (sec: number) => void;
  clearError: () => void;
}

const VOLUME_KEY = 'player.volume';

function loadInitialVolume(): number {
  const raw = readNumber(VOLUME_KEY);
  if (raw === null) return 1;
  return Math.max(0, Math.min(1, raw));
}

export const usePlayerStore = create<PlayerState>((set, get) => ({
  currentTrack: null,
  isPlaying: false,
  currentTime: 0,
  duration: 0,
  queue: [],
  volume: loadInitialVolume(),
  error: null,

  playTrack: (track, opts) =>
    set({
      currentTrack: track,
      isPlaying: true,
      currentTime: 0,
      duration: track.durationSec ?? 0,
      queue: opts?.queue && opts.queue.length > 0 ? opts.queue : [track],
      error: null,
    }),

  togglePlay: () => set((s) => ({ isPlaying: !s.isPlaying })),
  pause: () => set({ isPlaying: false }),
  resume: () => set({ isPlaying: true }),

  seek: (sec) => set({ currentTime: Math.max(0, sec) }),

  setVolume: (v) => {
    const clamped = Math.max(0, Math.min(1, v));
    writeNumber(VOLUME_KEY, clamped);
    set({ volume: clamped });
  },

  next: () => {
    const q = get().queue;
    const cur = get().currentTrack;
    if (!cur) return;
    const idx = q.findIndex((t) => t.id === cur.id);
    if (idx < 0 || idx + 1 >= q.length) return;
    set({
      currentTrack: q[idx + 1],
      isPlaying: true,
      currentTime: 0,
      duration: q[idx + 1].durationSec ?? 0,
      error: null,
    });
  },

  prev: () => {
    const q = get().queue;
    const cur = get().currentTrack;
    if (!cur || q.length < 2) {
      set({ currentTime: 0 });
      return;
    }
    const idx = q.findIndex((t) => t.id === cur.id);
    if (idx <= 0) {
      set({ currentTime: 0 });
      return;
    }
    set({
      currentTrack: q[idx - 1],
      isPlaying: true,
      currentTime: 0,
      duration: q[idx - 1].durationSec ?? 0,
      error: null,
    });
  },

  setCurrentTime: (sec) => set({ currentTime: sec }),
  setDuration: (sec) => set({ duration: sec }),
  clearError: () => set({ error: null }),
}));
