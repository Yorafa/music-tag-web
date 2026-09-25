// Row-selection behaviour for the scraper's table.
//
// Extracted from WorkstationView so the logic is testable. An earlier
// version lived inline in the component and was "covered" by a spec that
// re-implemented it — which passed just as happily when the component's
// copy was mutated back to the bug. A duplicated test of duplicated code
// proves nothing; this module is the single copy both the component and
// the spec import.
//
// The breakpoint branch that used to live here is gone, and that is the
// point. It read `inspectorVisible` off a 1024px media query and opened
// the editor only when the TrackInspector column was off screen — so the
// two widths ran genuinely different detail UIs, and the two could drift
// apart without any test noticing, because each was "correct" in
// isolation. The column no longer exists: the detail is a dialog at
// every width, so a tap does the same thing everywhere.

import { useDetailStore } from '@/store/useDetailStore';
import type { WorklistRow } from '@/types';

export interface SelectRowDeps {
  /** Records the active row; drives the table's highlight. */
  setSelectedPath: (fullPath: string) => void;
}

/** Handle a tap on a worklist row: select it, then show its detail. */
export function selectWorklistRow(
  row: WorklistRow,
  deps: SelectRowDeps,
): void {
  deps.setSelectedPath(row.fullPath);
  useDetailStore.getState().openDetail({
    fullPath: row.fullPath,
    fileName: row.fileName,
    musicInfo: row.musicInfo ?? null,
  });
}
