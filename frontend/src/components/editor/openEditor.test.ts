// Specs for the shared openEditorForRow helper.
//
// The sequence was duplicated in PlayView and Worklist, differing only
// in which store received the fetched tags, and needed a third copy in
// WorkstationView because the scraper's detail panel is hidden on
// mobile. The ordering constraints are subtle — identity before the
// race guard, optimistic hydration before opening, fetch strictly after
// — so they are pinned here rather than left to three matching
// implementations.

import { describe, it, expect, vi, beforeEach } from 'vitest';
import { openEditorForRow } from './openEditor';
import { useEditorStore, editorActions } from '@/store/useEditorStore';
import { useNoticeStore } from '@/store/useNoticeStore';

// Mock the network read so the specs control exactly what comes back and
// when. The helper's contract is the sequencing around this call.
const readTagsFromPath = vi.hoisted(() => vi.fn());
vi.mock('@/lib/id3Reader', () => ({ readTagsFromPath }));

const needsMusicInfoRefetch = vi.hoisted(() => vi.fn());
vi.mock('@/utils/persistMusicInfo', () => ({ needsMusicInfoRefetch }));

const TAGS = { title: 'Song', artist: 'Artist', album: 'Album' };

function state() {
  return useEditorStore.getState();
}

beforeEach(() => {
  readTagsFromPath.mockReset();
  needsMusicInfoRefetch.mockReset().mockReturnValue(false);
  editorActions.setSongList([]);
  editorActions.setMusicInfo({});
  editorActions.setEditorOpen(false);
  editorActions.setSelectedFile(null);
  editorActions.setFullPath('');
  useNoticeStore.setState({ messages: [], backlog: [] });
});

describe('openEditorForRow', () => {
  it('opens the dialog for the given row', async () => {
    await openEditorForRow({ fileName: 'a.mp3', fullPath: 'x/a.mp3' });

    expect(state().editorOpen).toBe(true);
    expect(state().selectedFile).toBe('a.mp3');
    expect(state().fullPath).toBe('x/a.mp3');
  });

  it('hydrates from the row cache so the dialog is never blank', async () => {
    await openEditorForRow({
      fileName: 'a.mp3',
      fullPath: 'x/a.mp3',
      musicInfo: TAGS,
    });
    expect(state().musicInfo).toMatchObject(TAGS);
  });

  it('clears a stale candidate list from the previously opened file', async () => {
    // songList drives the scrape-results panel next to the editor. Left
    // over from another file it would show that file's candidates beside
    // this file's tags.
    editorActions.setSongList([
      { id: '1', name: 'Other', artist: 'X', artist_id: '', album: '', album_id: '', album_img: '', year: '' },
    ]);

    await openEditorForRow({ fileName: 'a.mp3', fullPath: 'x/a.mp3' });
    expect(state().songList).toEqual([]);
  });

  it('skips the fetch when the cache is good enough', async () => {
    needsMusicInfoRefetch.mockReturnValue(false);
    await openEditorForRow({ fileName: 'a.mp3', fullPath: 'x/a.mp3', musicInfo: TAGS });
    expect(readTagsFromPath).not.toHaveBeenCalled();
  });

  it('fetches and applies tags when the cache is too thin', async () => {
    needsMusicInfoRefetch.mockReturnValue(true);
    readTagsFromPath.mockResolvedValue({ title: 'Fresh' });

    await openEditorForRow({ fileName: 'a.mp3', fullPath: 'x/a.mp3' });

    expect(readTagsFromPath).toHaveBeenCalledWith('x/a.mp3');
    expect(state().musicInfo).toMatchObject({ title: 'Fresh' });
  });

  it('seeds the caller cache so the next open is instant', async () => {
    needsMusicInfoRefetch.mockReturnValue(true);
    readTagsFromPath.mockResolvedValue({ title: 'Fresh' });
    const cache = vi.fn();

    await openEditorForRow({ fileName: 'a.mp3', fullPath: 'x/a.mp3', cache });
    expect(cache).toHaveBeenCalledWith({ title: 'Fresh' });
  });

  it('ignores a response for a row the user has already left', async () => {
    // The race that matters on mobile: tapping rows quickly while
    // responses are in flight. A slow answer for Row A must not land on
    // top of Row B.
    let release: (v: unknown) => void = () => {};
    // Only row A's read is in flight; B is served from cache so the
    // spec is exercising the race, not a second unresolved promise.
    needsMusicInfoRefetch.mockImplementation((info) => info == null);
    readTagsFromPath.mockReturnValue(new Promise((res) => { release = res; }));

    const pending = openEditorForRow({ fileName: 'a.mp3', fullPath: 'x/a.mp3' });
    // User taps a different row before A's response arrives.
    await openEditorForRow({ fileName: 'b.mp3', fullPath: 'x/b.mp3', musicInfo: TAGS });
    release({ title: 'Stale A' });
    await pending;

    expect(state().fullPath).toBe('x/b.mp3');
    expect(state().musicInfo.title).not.toBe('Stale A');
  });

  it('keeps the optimistic values when the file genuinely has no tags', async () => {
    // An all-null response means "fetch worked, nothing tagged". Blanking
    // the form here would read as a failure.
    needsMusicInfoRefetch.mockReturnValue(true);
    readTagsFromPath.mockResolvedValue({
      title: null, artist: null, album: null,
    });

    await openEditorForRow({
      fileName: 'a.mp3',
      fullPath: 'x/a.mp3',
      musicInfo: TAGS,
    });
    expect(state().musicInfo).toMatchObject(TAGS);
  });

  it('leaves the dialog open when the fetch fails', async () => {
    // The dialog must not close on a transport error; the optimistic
    // values plus a notice are more useful than an empty editor.
    needsMusicInfoRefetch.mockReturnValue(true);
    readTagsFromPath.mockRejectedValue(new Error('boom'));

    await openEditorForRow({ fileName: 'a.mp3', fullPath: 'x/a.mp3' });

    expect(state().editorOpen).toBe(true);
    expect(useNoticeStore.getState().messages.some((n) => n.text.includes('boom'))).toBe(true);
  });

  it('opens the dialog before awaiting the fetch', async () => {
    // A slow or hanging endpoint must not delay the dialog appearing.
    needsMusicInfoRefetch.mockReturnValue(true);
    readTagsFromPath.mockReturnValue(new Promise(() => {}));

    const pending = openEditorForRow({ fileName: 'a.mp3', fullPath: 'x/a.mp3' });
    // Not awaited: the point is that the open already happened.
    expect(state().editorOpen).toBe(true);
    void pending;
  });
});
