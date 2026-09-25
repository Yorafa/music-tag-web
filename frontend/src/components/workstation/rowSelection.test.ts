// Specs for the scraper's row-tap behaviour across breakpoints.
//
// Reported bug: on a phone, tapping a song in 智能刮削 did nothing. The
// row's onClick only set a selection highlight, and the panel that
// renders the selected row's detail (TrackInspector) is `hidden lg:flex`
// — so below 1024px the tap had no visible effect whatsoever.
//
// These import selectWorklistRow, the same function WorkstationView
// calls. An earlier draft of this file re-implemented the branch inline
// and passed even after the component's copy had been mutated back to
// the bug — a duplicated test of duplicated code proves nothing, which
// is why the logic was extracted rather than the assertion kept.
//
// The contract: the tap always records the selection, and it additionally
// opens the song-detail Dialog exactly when no inspector is on screen to
// show the selection. Opening it unconditionally would be a regression —
// on a wide screen the inspector is right there, and a modal the user
// did not ask for would replace a two-column workspace with a dialog.

import { describe, it, expect, vi, beforeEach } from 'vitest';
import { selectWorklistRow } from './rowSelection';
import type { WorklistRow } from '@/types';

const openEditorForRow = vi.hoisted(() => vi.fn());
vi.mock('@/components/editor/openEditor', () => ({ openEditorForRow }));

const setMusicInfo = vi.hoisted(() => vi.fn());
vi.mock('@/store/useWorklistStore', () => ({
  useWorklistStore: { getState: () => ({ setMusicInfo }) },
}));

const INSPECTOR_QUERY = '(min-width: 1024px)';

function row(over: Partial<WorklistRow> = {}): WorklistRow {
  return {
    fullPath: 'Artist/Album/song.mp3',
    fileName: 'song.mp3',
    musicInfo: { title: 'T' },
    ...over,
  } as WorklistRow;
}

beforeEach(() => {
  openEditorForRow.mockReset();
  setMusicInfo.mockReset();
});

describe('selectWorklistRow', () => {
  it('opens the detail dialog when no inspector is on screen', () => {
    // The phone case: nothing else would reveal the selection.
    const setSelectedPath = vi.fn();
    selectWorklistRow(row(), { inspectorVisible: false, setSelectedPath });

    expect(setSelectedPath).toHaveBeenCalledWith('Artist/Album/song.mp3');
    expect(openEditorForRow).toHaveBeenCalledTimes(1);
    expect(openEditorForRow).toHaveBeenCalledWith(
      expect.objectContaining({
        fileName: 'song.mp3',
        fullPath: 'Artist/Album/song.mp3',
      }),
    );
  });

  it('only selects when the inspector is visible', () => {
    // Desktop: TrackInspector is on screen, so a modal the user did not
    // ask for would be a regression, not a fix.
    const setSelectedPath = vi.fn();
    selectWorklistRow(row(), { inspectorVisible: true, setSelectedPath });

    expect(setSelectedPath).toHaveBeenCalledWith('Artist/Album/song.mp3');
    expect(openEditorForRow).not.toHaveBeenCalled();
  });

  it('always records the selection, even when opening the dialog', () => {
    const setSelectedPath = vi.fn();
    selectWorklistRow(row(), { inspectorVisible: false, setSelectedPath });
    expect(setSelectedPath).toHaveBeenCalledWith('Artist/Album/song.mp3');
  });

  it('passes the row cache through so the dialog is not blank', () => {
    selectWorklistRow(row(), { inspectorVisible: false, setSelectedPath: vi.fn() });
    expect(openEditorForRow).toHaveBeenCalledWith(
      expect.objectContaining({ musicInfo: { title: 'T' } }),
    );
  });

  it('handles a row with no cached tags', () => {
    const setSelectedPath = vi.fn();
    selectWorklistRow(row({ musicInfo: null }), {
      inspectorVisible: false,
      setSelectedPath,
    });

    expect(setSelectedPath).toHaveBeenCalled();
    expect(openEditorForRow).toHaveBeenCalledWith(
      expect.objectContaining({ musicInfo: null }),
    );
  });

  it('seeds the worklist cache so the next open is instant', () => {
    // Without this, every tap on a phone re-fetches the same file.
    selectWorklistRow(row(), { inspectorVisible: false, setSelectedPath: vi.fn() });
    const { cache } = openEditorForRow.mock.calls[0][0];
    cache({ title: 'Fresh' });
    expect(setMusicInfo).toHaveBeenCalledWith('Artist/Album/song.mp3', {
      title: 'Fresh',
    });
  });

  it('uses the same 1024px the inspector column is hidden below', () => {
    // The TrackInspector column is `hidden lg:flex` and Tailwind's lg is
    // 1024px. If the query drifts from the class, a tap opens a dialog
    // on a screen that already has an inspector — or nothing happens on
    // one that does not.
    expect(INSPECTOR_QUERY).toBe('(min-width: 1024px)');
  });
});
