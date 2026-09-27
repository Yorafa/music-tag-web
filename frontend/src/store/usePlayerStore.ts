// Cross-mode player state. Lives in its own Zustand store so the audio
// element, transport bars, and inline row buttons all share one source of
// truth. Mounted in <PlayerBar> at the bottom of AppShell, but consumed
// from anywhere a row wants to start a preview (WorkstationTable's PlayButton,
// CloudSearchView's PlayButton).

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
  /** Lyric body for display in the player. Either plain text or LRC with
   *  `[mm:ss.xx]` time tags — LyricsView parses both. Populated from the
   *  local file's id3 `lyrics` tag (PlayView) or the inspector's edited
   *  lyrics (TrackInspector). Absent for most streaming rows. */
  lyrics?: string;
  source: PlayerSource;
}

interface PlayerState {
  currentTrack: PlayerTrack | null;
  isPlaying: boolean;
  currentTime: number;
  duration: number;
  /** 0..1. Persisted across page reloads so the user doesn't lose mix on
   *  tab-nap. NOT persisted to localStorage on every render — debounced by
   *  PlayerBar's setter wrapper. */
  volume: number;
  error: string | null;
  /** True while the <audio> element is buffering (waiting for enough
   *  buffered bytes to resume). Set on onWaiting + cleared on
   *  onPlaying / onCanPlay — see PlayerBar.tsx. The flag is consumed
   *  by PlayButton to render the spin loader, surfacing the "this
   *  particular preview is stalling" signal right next to the row
   *  that triggered it, rather than burying it in a global toast. */
  isBuffering: boolean;

  /** Start playing a new track. Replaces currentTrack; resets currentTime
   *  to 0. Single-track only — there is no multi-track queue (previews are
   *  started one row at a time), so there is no next/prev navigation. */
  playTrack: (track: PlayerTrack) => void;
  togglePlay: () => void;
  pause: () => void;
  resume: () => void;
  /** Seek (seconds). Updates currentTime so the slider thumb follows; the
   *  <audio> element is brought in sync by the PlayerBar's slider
   *  onChange, not by a reactive effect (avoids a loop with onTimeUpdate). */
  seek: (sec: number) => void;
  setVolume: (v: number) => void;

  // Mirrors from native <audio> events. Don't call from UI directly.
  setCurrentTime: (sec: number) => void;
  setDuration: (sec: number) => void;
  /** Toggle isBuffering. Called from PlayerBar's onWaiting / onPlaying /
   *  onCanPlay handlers so the buffering signal is observable outside
   *  the <audio> element (PlayButton, future spinner, etc.). */
  setIsBuffering: (b: boolean) => void;
  clearError: () => void;
}

const VOLUME_KEY = 'player.volume';

function loadInitialVolume(): number {
  const raw = readNumber(VOLUME_KEY);
  if (raw === null) return 1;
  return Math.max(0, Math.min(1, raw));
}

export const usePlayerStore = create<PlayerState>((set) => ({
  currentTrack: null,
  isPlaying: false,
  currentTime: 0,
  duration: 0,
  volume: loadInitialVolume(),
  error: null,
  isBuffering: false,

  playTrack: (track) =>
    set({
      currentTrack: track,
      isPlaying: true,
      currentTime: 0,
      duration: track.durationSec ?? 0,
      error: null,
      // New track → the <audio> element will fire onWaiting the moment it
      // starts fetching, and onCanPlay once it has enough buffered.
      // Setting isBuffering=false here means: until onWaiting fires, we
      // don't pre-emptively show a spinner, which is correct because the
      // stream may resolve instantly (cached file on disk).
      isBuffering: false,
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

  setCurrentTime: (sec) => set({ currentTime: sec }),
  setDuration: (sec) => set({ duration: sec }),
  setIsBuffering: (b) => set({ isBuffering: b }),
  clearError: () => set({ error: null }),
}));
