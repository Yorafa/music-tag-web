// Stage A of docs/plugable-plugins.md §4.2 — the single source of truth for
// (a) which plugins the backend has registered, and (b) which subset the
// user has enabled. The enabled-set is localStorage-only; the backend
// doesn't persist user preferences in this stage.

import { create } from 'zustand';
import type { SourceInfo } from '@/types';
import { getSources } from '@/api/client';

const STORAGE_KEY = 'app.enabledSources';

interface SourceState {
  sources: SourceInfo[];
  enabled: Set<string>;
  loaded: boolean;
  /** Pull the canonical source list from the backend + reconcile enabled-set. */
  loadSources: () => Promise<void>;
  /** Toggle one source on/off; persists to localStorage. */
  toggle: (name: string) => void;
  /** Replace the entire enabled-set atomically — used by SourcePickerModal
   *  on its Apply path. Filters out names the backend doesn't know about. */
  setEnabled: (names: string[]) => void;
  /** O(1) membership check used by SearchPanel when building the request. */
  isEnabled: (name: string) => boolean;
  /** Reset enabled-set to "all-on" (matches first-boot default). */
  resetToDefault: () => void;
}

/** Returns `null` when localStorage has no entry yet (= "no preference"),
 *  otherwise a set built from the array. Returning `null` lets the caller
 *  decide whether to apply the default-on-everything fallback. */
function loadPersistedEnabled(): Set<string> | null {
  try {
    const raw = localStorage.getItem(STORAGE_KEY);
    if (raw === null) return null;
    const parsed = JSON.parse(raw);
    if (Array.isArray(parsed)) {
      return new Set(parsed.filter((s): s is string => typeof s === 'string'));
    }
  } catch {
    /* corrupt JSON — fall through to null */
  }
  return null;
}

function persistEnabled(set: Set<string>): void {
  try {
    localStorage.setItem(STORAGE_KEY, JSON.stringify(Array.from(set)));
  } catch {
    /* localStorage may be unavailable in private-mode browsers — fail silently */
  }
}

export const useSourceStore = create<SourceState>((set, get) => ({
  sources: [],
  enabled: new Set<string>(),
  loaded: false,

  loadSources: async () => {
    try {
      const res = await getSources();
      if (res?.result && Array.isArray(res.data)) {
        const sources = res.data;
        const persisted = loadPersistedEnabled();
        let enabled: Set<string>;
        if (persisted) {
          // Reconcile: drop any persisted entries that no longer exist on
          // the backend (defends against renames/deletions of built-in
          // plugins). But add anything that's registered but missing from
          // the persisted set, so newly-added backend sources become
          // default-on across upgrades.
          const liveNames = new Set(sources.map((s) => s.name));
          enabled = new Set<string>();
          for (const name of persisted) {
            if (liveNames.has(name)) enabled.add(name);
          }
          for (const name of liveNames) {
            if (!enabled.has(name)) enabled.add(name);
          }
        } else {
          // First boot: enable everything + persist so the user can
          // subsequently toggle and stay consistent.
          enabled = new Set(sources.map((s) => s.name));
          persistEnabled(enabled);
        }
        set({ sources, enabled, loaded: true });
      } else {
        // Backend error — keep state empty but mark loaded so the UI
        // doesn't retry forever.
        set({ loaded: true });
      }
    } catch {
      set({ loaded: true });
    }
  },

  toggle: (name: string) => {
    const enabled = new Set(get().enabled);
    if (enabled.has(name)) {
      enabled.delete(name);
    } else {
      enabled.add(name);
    }
    set({ enabled });
    persistEnabled(enabled);
  },

  setEnabled: (names: string[]) => {
    const liveNames = new Set(get().sources.map((s) => s.name));
    const enabled = new Set<string>();
    for (const name of names) {
      if (liveNames.has(name)) enabled.add(name);
    }
    set({ enabled });
    persistEnabled(enabled);
  },

  isEnabled: (name: string) => get().enabled.has(name),

  resetToDefault: () => {
    const enabled = new Set(get().sources.map((s) => s.name));
    set({ enabled });
    persistEnabled(enabled);
  },
}));
