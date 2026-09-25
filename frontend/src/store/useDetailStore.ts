// The one song-detail surface, shared by every section that lists local
// audio (音乐库 / 智能刮削).
//
// This store exists because the detail UI is hosted at AppShell root but
// opened from two different views that live in different stores
// (useLibraryStore's LibraryRow and useWorklistStore's WorklistRow).
// Neither view can own the open state — closing the dialog has to
// unmount the popup that lives above both of them — and neither can own
// the row data, because the other store owns it.
//
// So this store holds the *intersection* of what the two row types carry,
// plus a nullable `target`: null means closed. Deriving `open` from
// `target !== null` rather than keeping a separate boolean removes the
// one state combination that has no meaning (open with nothing to show).
//
// Why a store and not props drilled from AppShell: `openDetail` is an
// event, not a render output. A row click deep inside a table has to
// reach a dialog rendered above the whole shell; threading a callback
// through PlayView → LibraryTable → row and WorkstationView →
// WorkstationTable → row buys nothing that a store call doesn't, and the
// store is the pattern every other cross-cutting concern here uses
// (player, notices, filters).

import { create } from 'zustand';
import type { MusicTagInfo } from '@/types';

/** Everything the song-detail dialog needs about a file, and the
 *  intersection of `LibraryRow` and `WorklistRow`. Both use fullPath as
 *  the row id, so `fullPath` is a stable identity across the two
 *  stores — which is what lets a save from either section refresh the
 *  other one's cached preview. */
export interface DetailTarget {
  fullPath: string;
  fileName: string;
  /** The row's lazily-cached tags, if any. `null` and `undefined` both
   *  mean "nothing cached yet"; the dialog fills in from the file. */
  musicInfo?: Partial<MusicTagInfo> | null;
}

interface DetailState {
  /** The file whose detail is on screen, or null when the dialog is
   *  closed. */
  target: DetailTarget | null;
  openDetail: (target: DetailTarget) => void;
  closeDetail: () => void;
}

export const useDetailStore = create<DetailState>((set) => ({
  target: null,

  openDetail: (target) => set({ target }),

  closeDetail: () => set({ target: null }),
}));
