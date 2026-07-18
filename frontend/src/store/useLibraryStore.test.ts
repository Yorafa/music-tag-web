import { describe, it, expect, beforeEach, vi } from 'vitest';
import { useLibraryStore } from '@/store/useLibraryStore';

// Mock @/api/client.getFileList — the store delegates to expandDirsToAudioFiles
// which calls getFileList per-directory recursively (one call per level).
// Each test builds a small directory -> {data: [{children: [...]}]} shape
// matching the backend's file_list response contract.
//
// FileNode shape (subset the store consumes):
//   { id: number; name: string; icon: 'icon-folder' | 'icon-script-files' | any;
//     state: string; children?: FileNode[] }

vi.mock('@/api/client', () => ({
  getFileList: vi.fn(async () => ({ result: true, code: '200', data: [], message: 'success' })),
}));

vi.mock('@/lib/id3Reader', () => ({
  // Since useLibraryStore.enqueueDirs fires fire-and-forget hydration
  // through lib/hydrateTags.ts → readTagsFromPath, every enqueueDirs in
  // these tests would otherwise fire a real fetch. Returning {}
  // makes hydrateTags take the silent "empty parse" path and emit
  // the dev-only console.debug breadcrumb; the spy below drops it.
  readTagsFromPath: vi.fn(async () => ({})),
}));

import { getFileList } from '@/api/client';
const getFileListMock = getFileList as unknown as ReturnType<typeof vi.fn>;

// localStorage shim — vitest jsdom provides one but writeJson in src/utils/persist
// reads through window.localStorage. Reset between tests so persist-side state
// doesn't leak. console.debug silence is set here so the auto-hydrator's
// dev breadcrumb (lib/hydrateTags.ts) doesn't leak into test output.
beforeEach(() => {
  getFileListMock.mockReset();
  getFileListMock.mockResolvedValue({ result: true, code: '200', data: [], message: 'success' });
  vi.spyOn(console, 'debug').mockImplementation(() => {});
  localStorage.clear();
  useLibraryStore.setState({ rows: [], dirs: [], query: '' });
});

// FileNode minimal structural type per the test fixtures.
type StubFileNode = {
  id: number;
  name: string;
  icon: 'icon-folder' | 'icon-script-files' | string;
  state: string;
  children?: StubFileNode[];
};

function dirNode(name: string, children: StubFileNode[] = []): StubFileNode {
  return { id: Math.random(), name, icon: 'icon-folder', state: 'null', children };
}
function fileNode(name: string, id = Math.floor(Math.random() * 1e6)): StubFileNode {
  return { id, name, icon: 'icon-script-files', state: 'null' };
}

// Build a file_list response shape: the backend returns data as an array
// whose first element is the directory marker; its `children` are the
// listing entries. expandDirsToAudioFiles walks res.data[0].children.
function listResp(children: StubFileNode[]) {
  return {
    result: true as const,
    code: '200',
    message: 'ok',
    data: [{ id: 0, name: '', icon: 'icon-folder', state: 'null', children }],
  };
}

describe('useLibraryStore', () => {
  it('enqueueDirs flattens a single directory listing into audio files', async () => {
    // dir 'foo' listing: subdir 'sub' + two top-level audio files.
    // b.txt is NOT audio, dropped.
    getFileListMock.mockResolvedValueOnce(
      listResp([
        dirNode('sub', [fileNode('a.flac'), fileNode('b.txt')]),
        fileNode('c.mp3'),
        fileNode('d.m4a'),
      ]),
    );
    // Recursive subdir fetch 'foo/sub' — Plan A's expandOne re-issues
    // getFileList('foo/sub') and walks the response's `data[0].children`
    // rather than trusting the FileNode.children the parent already
    // returned. Re-populate with the same audio listing here so a.flac
    // is emitted. b.txt is dropped as a non-audio file.
    getFileListMock.mockResolvedValueOnce(
      listResp([fileNode('a.flac'), fileNode('b.txt')]),
    );

    const res = await useLibraryStore.getState().enqueueDirs(['foo']);
    expect(getFileListMock).toHaveBeenCalledTimes(2);
    expect(getFileListMock).toHaveBeenNthCalledWith(1, 'foo');
    expect(getFileListMock).toHaveBeenNthCalledWith(2, 'foo/sub');
    // b.txt is NOT audio, dropped. a.flac / c.mp3 / d.m4a are audio.
    expect(res.added).toBe(3);
    const rows = useLibraryStore.getState().rows;
    expect(rows.map((r) => r.fileName).sort()).toEqual(['a.flac', 'c.mp3', 'd.m4a']);
    // Each row's fullPath = parent path / name. 'a.flac' lives under 'foo/sub'
    // so its fullPath is 'foo/sub/a.flac' (matches Plan A's recursive joinPath).
    expect(rows.find((r) => r.fileName === 'a.flac')?.fullPath).toBe('foo/sub/a.flac');
    expect(rows.find((r) => r.fileName === 'c.mp3')?.fullPath).toBe('foo/c.mp3');
    // dir tracked in store.dirs for the drawer's existingDirs badge.
    expect(useLibraryStore.getState().dirs).toEqual(['foo']);
  });

  it('enqueueDirs dedupes by directory granularity on re-add', async () => {
    // First add: dir 'd1' contains x.wav
    getFileListMock.mockResolvedValueOnce(listResp([fileNode('x.wav', 100)]));
    await useLibraryStore.getState().enqueueDirs(['d1']);
    expect(useLibraryStore.getState().rows.length).toBe(1);

    // Re-add the same dir 'd1' returning the SAME file x.wav → the directory
    // has overlap with existing rows → the whole dir batch is dropped.
    getFileListMock.mockResolvedValueOnce(listResp([fileNode('x.wav', 100)]));
    const res = await useLibraryStore.getState().enqueueDirs(['d1']);
    expect(res.added).toBe(0);
    expect(res.skipped).toBe(1);
    expect(useLibraryStore.getState().rows.length).toBe(1);
    // dirs remains just ['d1'] — no duplicate badge entry.
    expect(useLibraryStore.getState().dirs).toEqual(['d1']);
  });

  it('enqueueDirs adds a different dir with different files', async () => {
    getFileListMock.mockResolvedValueOnce(listResp([fileNode('p.mp3', 1)]));
    await useLibraryStore.getState().enqueueDirs(['d1']);

    getFileListMock.mockResolvedValueOnce(listResp([fileNode('q.flac', 2)]));
    const res = await useLibraryStore.getState().enqueueDirs(['d2']);
    expect(res.added).toBe(1);
    expect(useLibraryStore.getState().rows.map((r) => r.fileName).sort()).toEqual(['p.mp3', 'q.flac']);
    expect(useLibraryStore.getState().dirs).toEqual(['d1', 'd2']);
  });

  it('enqueueDirs persists rows and dirs to localStorage', async () => {
    getFileListMock.mockResolvedValueOnce(listResp([fileNode('p.mp3', 1)]));
    await useLibraryStore.getState().enqueueDirs(['persist-me']);
    const stored = JSON.parse(localStorage.getItem('library.v1') || 'null');
    expect(stored).not.toBeNull();
    expect(stored.dirs).toEqual(['persist-me']);
    expect(stored.rows.length).toBe(1);
    expect(stored.rows[0].id).toBe('persist-me/p.mp3');
  });

  it('rehydrates from localStorage on store init', async () => {
    localStorage.setItem('library.v1', JSON.stringify({
      rows: [{ id: 'r/m.flac', fullPath: 'r/m.flac', fileName: 'm.flac' }],
      dirs: ['r'],
    }));
    vi.resetModules();
    const { useLibraryStore: fresh } = await import('@/store/useLibraryStore');
    expect(fresh.getState().rows.length).toBe(1);
    expect(fresh.getState().rows[0].fileName).toBe('m.flac');
    expect(fresh.getState().dirs).toEqual(['r']);
  });

  it('search filters by substring (case-insensitive) on fileName', () => {
    useLibraryStore.setState({
      rows: [
        { id: 'a/FooBar.flac', fullPath: 'a/FooBar.flac', fileName: 'FooBar.flac' },
        { id: 'b/baz.mp3', fullPath: 'b/baz.mp3', fileName: 'baz.mp3' },
      ],
      dirs: [], query: '',
    });
    useLibraryStore.getState().search('foo');
    expect(useLibraryStore.getState().getFiltered().map((r) => r.id)).toEqual(['a/FooBar.flac']);
  });

  it('search with empty query returns all rows', () => {
    useLibraryStore.setState({
      rows: [
        { id: 'a/x.flac', fullPath: 'a/x.flac', fileName: 'x.flac' },
        { id: 'b/y.mp3', fullPath: 'b/y.mp3', fileName: 'y.mp3' },
      ],
      dirs: [], query: '',
    });
    useLibraryStore.getState().search('');
    expect(useLibraryStore.getState().getFiltered().length).toBe(2);
  });

  it('remove drops one row by id and re-persists', () => {
    useLibraryStore.setState({
      rows: [
        { id: 'a/x.flac', fullPath: 'a/x.flac', fileName: 'x.flac' },
        { id: 'b/y.mp3', fullPath: 'b/y.mp3', fileName: 'y.mp3' },
      ],
      dirs: ['a', 'b'], query: '',
    });
    useLibraryStore.getState().remove('a/x.flac');
    expect(useLibraryStore.getState().rows.map((r) => r.id)).toEqual(['b/y.mp3']);
    // dirs preserved — remove() doesn't prune directories.
    expect(useLibraryStore.getState().dirs).toEqual(['a', 'b']);
    const stored = JSON.parse(localStorage.getItem('library.v1') || 'null');
    expect(stored.rows.length).toBe(1);
    expect(stored.dirs).toEqual(['a', 'b']);
  });

  it('clear resets state and removes the storage key', () => {
    useLibraryStore.setState({
      rows: [{ id: 'a/x', fullPath: 'a', fileName: 'x' }],
      dirs: ['a'], query: 'foo',
    });
    localStorage.setItem('library.v1', '{"x":1}');
    useLibraryStore.getState().clear();
    expect(useLibraryStore.getState().rows.length).toBe(0);
    expect(useLibraryStore.getState().dirs.length).toBe(0);
    expect(useLibraryStore.getState().query).toBe('');
    expect(localStorage.getItem('library.v1')).toBeNull();
  });
});
