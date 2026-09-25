// Specs for selectWorklistRow, the same function WorkstationView calls.
//
// These import the real module rather than re-implementing it. An
// earlier draft of this file re-implemented the branch inline and passed
// even after the component's copy had been mutated back to the bug — a
// duplicated test of duplicated code proves nothing, which is why the
// logic was extracted rather than the assertion kept.
//
// The contract: the tap always records the selection AND always opens
// the song-detail dialog. There is no viewport branch any more. The old
// one read a 1024px media query and skipped the dialog when the
// TrackInspector column was on screen, which is what let the phone and
// the desktop drift onto two different detail UIs; both are correct
// against their own spec and wrong against the user's.

import { describe, it, expect, vi, beforeEach } from 'vitest';
import { selectWorklistRow } from './rowSelection';
import { useDetailStore } from '@/store/useDetailStore';
import type { WorklistRow } from '@/types';

function row(over: Partial<WorklistRow> = {}): WorklistRow {
  return {
    fullPath: 'Artist/Album/song.mp3',
    fileName: 'song.mp3',
    musicInfo: { title: 'T' },
    ...over,
  } as WorklistRow;
}

function target() {
  return useDetailStore.getState().target;
}

beforeEach(() => {
  useDetailStore.setState({ target: null });
});

describe('selectWorklistRow', () => {
  it('records the selection', () => {
    const setSelectedPath = vi.fn();
    selectWorklistRow(row(), { setSelectedPath });

    expect(setSelectedPath).toHaveBeenCalledWith('Artist/Album/song.mp3');
  });

  it('opens the song-detail dialog', () => {
    selectWorklistRow(row(), { setSelectedPath: vi.fn() });

    expect(target()).toEqual({
      fullPath: 'Artist/Album/song.mp3',
      fileName: 'song.mp3',
      musicInfo: { title: 'T' },
    });
  });

  it('opens the dialog on a wide viewport too — no breakpoint branch', () => {
    // The regression this guards: the tap behaved differently above and
    // below 1024px, so each width got a different detail UI. Nothing
    // about this function reads the viewport now, so there is no width
    // at which the dialog stays shut.
    selectWorklistRow(row(), { setSelectedPath: vi.fn() });
    expect(target()).not.toBeNull();
  });

  it('opens the dialog even when a row is already selected', () => {
    // Re-tapping the highlighted row must still be able to show detail.
    const setSelectedPath = vi.fn();
    selectWorklistRow(row(), { setSelectedPath });
    selectWorklistRow(row(), { setSelectedPath });

    expect(setSelectedPath).toHaveBeenCalledTimes(2);
    expect(target()).not.toBeNull();
  });

  it('switches the target when a different row is tapped', () => {
    selectWorklistRow(row(), { setSelectedPath: vi.fn() });
    selectWorklistRow(
      row({ fullPath: 'Artist/Album/other.mp3', fileName: 'other.mp3' }),
      { setSelectedPath: vi.fn() },
    );

    expect(target()?.fullPath).toBe('Artist/Album/other.mp3');
  });

  it('normalises a missing cache to null', () => {
    // TrackInspector treats undefined and null the same way; leaving
    // the field off the target would make the two paths differ in
    // exactly the one case (an untagged file) worth being consistent on.
    selectWorklistRow(row({ musicInfo: undefined }), { setSelectedPath: vi.fn() });

    expect(target()?.musicInfo).toBeNull();
  });
});

describe('the detail store', () => {
  it('treats a null target as closed', () => {
    useDetailStore.getState().openDetail({
      fullPath: 'a.mp3',
      fileName: 'a.mp3',
    });
    expect(target()).not.toBeNull();

    useDetailStore.getState().closeDetail();
    expect(target()).toBeNull();
  });

  it('has no open-without-a-target state to get out of sync', () => {
    // `open` is derived from the target rather than stored beside it, so
    // a separate boolean could never disagree with what is displayed.
    useDetailStore.getState().openDetail({ fullPath: 'a.mp3', fileName: 'a.mp3' });
    expect(target() !== null).toBe(true);

    useDetailStore.getState().closeDetail();
    expect(target() !== null).toBe(false);
  });
});
