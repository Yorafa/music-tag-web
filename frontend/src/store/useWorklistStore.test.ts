import { describe, it, expect, beforeEach, vi } from 'vitest';
import { useWorklistStore } from '@/store/useWorklistStore';

vi.mock('@/api/client', () => ({
  getFileList: vi.fn(async () => ({ result: true, code: '200', data: [], message: 'success' })),
}));

vi.mock('@/lib/id3Reader', () => ({
  readTagsFromPath: vi.fn(async () => ({})),
}));

import { getFileList } from '@/api/client';
const getFileListMock = getFileList as unknown as ReturnType<typeof vi.fn>;

type StubFileNode = {
  id: number;
  name: string;
  icon: 'icon-folder' | 'icon-script-files' | string;
  state: string;
  children?: StubFileNode[];
};

function fileNode(name: string, id = Math.floor(Math.random() * 1e6)): StubFileNode {
  return { id, name, icon: 'icon-script-files', state: 'null' };
}

function listResp(children: StubFileNode[]) {
  return {
    result: true as const,
    code: '200',
    message: 'ok',
    data: [{ id: 0, name: '', icon: 'icon-folder', state: 'null', children }],
  };
}

beforeEach(() => {
  getFileListMock.mockReset();
  getFileListMock.mockResolvedValue({ result: true, code: '200', data: [], message: 'success' });
  vi.spyOn(console, 'debug').mockImplementation(() => {});
  localStorage.clear();
  // Reset EVERY persisted-or-session field so successive tests don't
  // see state carried by `setGrouping('album')` from the previous
  // test. `grouping` would otherwise land on the wrong arm of the
  // cycle; `collapsedGroups` similarly. Both are reset to "no
  // grouping, no collapsed groups" — identical to a fresh mount.
  useWorklistStore.setState({
    rows: [],
    selectedIds: [],
    filter: 'all',
    grouping: 'none',
    collapsedGroups: new Set(),
  });
});

describe('useWorklistStore persistence', () => {
  it('enqueueDirs persists rows to localStorage', async () => {
    getFileListMock.mockResolvedValueOnce(listResp([fileNode('p.mp3', 1)]));
    await useWorklistStore.getState().enqueueDirs(['scrape-me']);

    const stored = JSON.parse(localStorage.getItem('worklist.v1') || 'null');
    expect(stored).not.toBeNull();
    expect(stored.rows.length).toBe(1);
    expect(stored.rows[0].id).toBe('scrape-me/p.mp3');
    expect(stored.rows[0].status).toBe('pending');
  });

  it('setStatus persists scraped status', async () => {
    getFileListMock.mockResolvedValueOnce(listResp([fileNode('p.mp3', 1)]));
    await useWorklistStore.getState().enqueueDirs(['scrape-me']);

    useWorklistStore.getState().setStatus('scrape-me/p.mp3', 'scraped');

    const stored = JSON.parse(localStorage.getItem('worklist.v1') || 'null');
    expect(stored.rows[0].status).toBe('scraped');
  });

  it('setMusicInfo persists lightweight tags and strips data-URI artwork', async () => {
    getFileListMock.mockResolvedValueOnce(listResp([fileNode('p.mp3', 1)]));
    await useWorklistStore.getState().enqueueDirs(['scrape-me']);

    useWorklistStore.getState().setMusicInfo('scrape-me/p.mp3', {
      title: 'Hello',
      artist: 'World',
      artwork: 'data:image/jpeg;base64,' + 'A'.repeat(2000),
      album_img: 'data:image/jpeg;base64,' + 'B'.repeat(2000),
    });

    const stored = JSON.parse(localStorage.getItem('worklist.v1') || 'null');
    expect(stored.rows[0].musicInfo.title).toBe('Hello');
    expect(stored.rows[0].musicInfo.artist).toBe('World');
    expect(stored.rows[0].musicInfo.artwork).toBeUndefined();
    expect(stored.rows[0].musicInfo.album_img).toBeUndefined();
  });

  it('rehydrates from localStorage on store init', async () => {
    localStorage.setItem(
      'worklist.v1',
      JSON.stringify({
        rows: [
          {
            id: 'r/m.flac',
            fullPath: 'r/m.flac',
            fileName: 'm.flac',
            status: 'scraped',
            musicInfo: { title: 'T', artist: 'A' },
          },
        ],
      }),
    );
    vi.resetModules();
    const { useWorklistStore: fresh } = await import('@/store/useWorklistStore');
    expect(fresh.getState().rows.length).toBe(1);
    expect(fresh.getState().rows[0].status).toBe('scraped');
    expect(fresh.getState().rows[0].musicInfo?.title).toBe('T');
  });

  it('boot re-fetches when lightweight tags lack a cover', async () => {
    localStorage.setItem(
      'worklist.v1',
      JSON.stringify({
        rows: [
          {
            id: 'r/c.flac',
            fullPath: 'r/c.flac',
            fileName: 'c.flac',
            status: 'pending',
            musicInfo: { title: 'Stripped', artist: 'OnlyText' },
          },
        ],
      }),
    );

    const { readTagsFromPath } = await import('@/lib/id3Reader');
    const readMock = readTagsFromPath as unknown as ReturnType<typeof vi.fn>;
    readMock.mockResolvedValueOnce({
      title: 'Stripped',
      artist: 'OnlyText',
      artwork: 'data:image/jpeg;base64,coverbytes',
    });

    vi.resetModules();
    const { useWorklistStore: fresh } = await import('@/store/useWorklistStore');
    await new Promise((r) => setTimeout(r, 0));
    await new Promise((r) => setTimeout(r, 0));

    expect(readMock).toHaveBeenCalled();
    expect(fresh.getState().rows[0].musicInfo?.artwork).toBe(
      'data:image/jpeg;base64,coverbytes',
    );
  });

  it('clear resets state and removes the storage key', () => {
    useWorklistStore.setState({
      rows: [
        {
          id: 'a/x',
          fullPath: 'a/x',
          fileName: 'x',
          status: 'pending',
        },
      ],
      selectedIds: ['a/x'],
      filter: 'pending',
    });
    localStorage.setItem('worklist.v1', '{"rows":[]}');
    useWorklistStore.getState().clear();
    expect(useWorklistStore.getState().rows.length).toBe(0);
    expect(useWorklistStore.getState().selectedIds.length).toBe(0);
    expect(localStorage.getItem('worklist.v1')).toBeNull();
  });

  it('remove drops rows and re-persists', async () => {
    getFileListMock.mockResolvedValueOnce(
      listResp([fileNode('a.mp3', 1), fileNode('b.mp3', 2)]),
    );
    await useWorklistStore.getState().enqueueDirs(['d']);
    useWorklistStore.getState().remove(['d/a.mp3']);

    const stored = JSON.parse(localStorage.getItem('worklist.v1') || 'null');
    expect(stored.rows.map((r: { id: string }) => r.id)).toEqual(['d/b.mp3']);
  });
});

// ─── Plan C.3 grouping plane ──────────────────────────────────────────
//
// `grouping` is persisted to `worklist.grouping.v1` (separate from
// `worklist.v1` per Open Details E) and `collapsedGroups` is session-
// only (Open Details A — never restored across reloads).

describe('useWorklistStore grouping', () => {
  it('setGrouping persists to worklist.grouping.v1', () => {
    useWorklistStore.getState().setGrouping('album');
    expect(localStorage.getItem('worklist.grouping.v1')).toBe('album');
    expect(useWorklistStore.getState().grouping).toBe('album');
  });

  it('toggleGrouping cycles none → album → artist → none', () => {
    expect(useWorklistStore.getState().grouping).toBe('none');
    useWorklistStore.getState().toggleGrouping();
    expect(useWorklistStore.getState().grouping).toBe('album');
    useWorklistStore.getState().toggleGrouping();
    expect(useWorklistStore.getState().grouping).toBe('artist');
    useWorklistStore.getState().toggleGrouping();
    expect(useWorklistStore.getState().grouping).toBe('none');
  });

  it('setGrouping coerces unknown values to "none"', () => {
    // Defensive: a stale localStorage with a v2 schema might land us
    // in setGrouping with garbage. Default back to 'none' (matches
    // loadGrouping's own defensive default).
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    useWorklistStore.getState().setGrouping('decade' as any);
    expect(useWorklistStore.getState().grouping).toBe('none');
  });

  it('toggleGroupCollapsed adds then removes a groupKey (Set semantics)', () => {
    const { toggleGroupCollapsed, collapsedGroups } =
      useWorklistStore.getState();
    expect(collapsedGroups.size).toBe(0);
    toggleGroupCollapsed('album:Compilation');
    expect(useWorklistStore.getState().collapsedGroups.has('album:Compilation')).toBe(true);
    toggleGroupCollapsed('album:Compilation');
    expect(useWorklistStore.getState().collapsedGroups.has('album:Compilation')).toBe(false);
  });

  it('collapsedGroups is NOT persisted across reload (Open Details A)', async () => {
    // Add a manual entry to collapsedGroups and confirm no writeJSON ever
    // hits localStorage for it — rehydrate comes back empty.
    useWorklistStore.getState().toggleGroupCollapsed('album:X');
    expect(localStorage.getItem('worklist.v1')).toBeNull();
    expect(localStorage.getItem('worklist.grouping.v1')).toBeNull();

    vi.resetModules();
    const { useWorklistStore: fresh } = await import('@/store/useWorklistStore');
    expect(fresh.getState().collapsedGroups.size).toBe(0);
  });

  it('rehydrates grouping choice from localStorage on store init', async () => {
    localStorage.setItem('worklist.grouping.v1', 'artist');
    vi.resetModules();
    const { useWorklistStore: fresh } = await import('@/store/useWorklistStore');
    expect(fresh.getState().grouping).toBe('artist');
  });
});
