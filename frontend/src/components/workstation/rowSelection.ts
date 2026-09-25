// Row-selection behaviour for the scraper's table.
//
// Extracted from WorkstationView so the breakpoint branch is testable.
// An earlier version of this logic lived inline in the component and was
// "covered" by a spec that re-implemented it — which passed just as
// happily when the component's copy was mutated back to the bug. A
// duplicated test of duplicated code proves nothing; this module is the
// single copy both the component and the spec import.

import { openEditorForRow } from '@/components/editor/openEditor';
import { useWorklistStore } from '@/store/useWorklistStore';
import type { WorklistRow } from '@/types';

export interface SelectRowDeps {
  /** Whether the TrackInspector column is on screen. The column is
   *  `hidden lg:flex`, so this is false below Tailwind's lg (1024px). */
  inspectorVisible: boolean;
  /** Records the active row; drives the inspector's contents on wide
   *  screens and the table's highlight everywhere. */
  setSelectedPath: (fullPath: string) => void;
}

/** Handle a tap on a worklist row.
 *
 *  The reported bug was that on a phone the tap did nothing at all: the
 *  click only set a selection highlight, and the only thing that renders
 *  a selected row's detail — TrackInspector — is hidden below lg. The
 *  selection was recorded somewhere no user could see it.
 *
 *  So the tap always records the selection, and additionally opens the
 *  song-detail Dialog when there is no inspector to show it. Opening it
 *  unconditionally would be its own regression: on a wide screen the
 *  inspector is right there, and a modal the user did not ask for would
 *  replace a two-column workspace with a dialog. */
export function selectWorklistRow(
  row: WorklistRow,
  deps: SelectRowDeps,
): void {
  deps.setSelectedPath(row.fullPath);

  if (deps.inspectorVisible) return;

  void openEditorForRow({
    fileName: row.fileName,
    fullPath: row.fullPath,
    musicInfo: row.musicInfo ?? null,
    cache: (info) =>
      useWorklistStore.getState().setMusicInfo(row.fullPath, info),
  });
}
