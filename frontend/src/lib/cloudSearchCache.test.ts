import { beforeEach, describe, expect, it } from 'vitest';
import {
  CLOUD_SEARCH_DEFAULTS,
  CLOUD_SEARCH_STORAGE_KEY,
  loadCloudSearchSnapshot,
  reconcileSnapshot,
  saveCloudSearchResults,
  saveCloudSearchSettings,
} from './cloudSearchCache';
import type { SearchResult } from '@/types';

function result(p: Partial<SearchResult> = {}): SearchResult {
  return {
    id: 'r1',
    source: 'netease',
    name: 'Song',
    artist: 'Artist',
    ...p,
  };
}

function storeRaw(value: unknown): void {
  localStorage.setItem(CLOUD_SEARCH_STORAGE_KEY, JSON.stringify(value));
}

beforeEach(() => {
  localStorage.clear();
});

describe('loadCloudSearchSnapshot', () => {
  it('opens as a first visit when nothing is stored', () => {
    expect(loadCloudSearchSnapshot()).toEqual({
      ...CLOUD_SEARCH_DEFAULTS,
      results: [],
    });
  });

  it('restores the query, the selection and the mode', () => {
    storeRaw({
      query: '周杰伦',
      multiSource: false,
      singleSource: 'kugou',
      selectedSources: ['kugou', 'kuwo'],
      results: [result()],
    });
    const s = loadCloudSearchSnapshot();
    expect(s.query).toBe('周杰伦');
    expect(s.multiSource).toBe(false);
    expect(s.singleSource).toBe('kugou');
    expect(s.selectedSources).toEqual(['kugou', 'kuwo']);
    expect(s.results).toHaveLength(1);
  });

  // The stored value is parsed localStorage: another build, a truncated
  // write, a hand edit. None of those may take the page down.
  it('falls back to the defaults on corrupt JSON', () => {
    localStorage.setItem(CLOUD_SEARCH_STORAGE_KEY, '{not json');
    expect(loadCloudSearchSnapshot().query).toBe('');
  });

  it('falls back to the defaults on a non-object payload', () => {
    for (const bad of [42, 'x', [1, 2, 3], null]) {
      storeRaw(bad);
      expect(loadCloudSearchSnapshot().selectedSources).toEqual(
        CLOUD_SEARCH_DEFAULTS.selectedSources,
      );
    }
  });

  it('replaces fields of the wrong type instead of trusting them', () => {
    storeRaw({
      query: 7,
      multiSource: 'yes',
      singleSource: null,
      selectedSources: 'netease',
      results: 'nope',
    });
    const s = loadCloudSearchSnapshot();
    expect(s.query).toBe('');
    expect(s.multiSource).toBe(true);
    expect(s.singleSource).toBe('netease');
    expect(s.selectedSources).toEqual(CLOUD_SEARCH_DEFAULTS.selectedSources);
    expect(s.results).toEqual([]);
  });

  // A search with no sources returns nothing and looks exactly like "this
  // song does not exist". The one outcome the dialog must never show.
  it('never restores an empty source selection', () => {
    for (const bad of [[], ['', null, 3], 'netease']) {
      storeRaw({ selectedSources: bad });
      expect(loadCloudSearchSnapshot().selectedSources.length).toBeGreaterThan(0);
    }
  });

  it('drops duplicate and non-string sources, keeping the order', () => {
    storeRaw({ selectedSources: ['kuwo', 'kugou', 'kuwo', 3, null] });
    expect(loadCloudSearchSnapshot().selectedSources).toEqual(['kuwo', 'kugou']);
  });

  it('caps a runaway query length', () => {
    storeRaw({ query: 'x'.repeat(5000) });
    expect(loadCloudSearchSnapshot().query.length).toBe(200);
  });

  it('drops a result row that is missing what the card needs', () => {
    storeRaw({
      results: [
        result({ id: '' }),
        result({ source: '' }),
        result({ artist: undefined as never }),
        result(),
      ],
    });
    const rows = loadCloudSearchSnapshot().results;
    expect(rows).toHaveLength(1);
    expect(rows[0].id).toBe('r1');
  });

  // Same rule as the worklist: an embedded cover is a multi-KB data-URI
  // and the quota is shared. Losing a thumbnail beats losing the source
  // selection.
  it('strips embedded covers but keeps remote cover URLs', () => {
    const dataUri = `data:image/jpeg;base64,${'A'.repeat(50)}`;
    storeRaw({
      results: [result({ album_img: dataUri, cover: 'https://img/1.jpg' })],
    });
    const row = loadCloudSearchSnapshot().results[0];
    expect(row.album_img).toBeUndefined();
    expect(row.cover).toBe('https://img/1.jpg');
  });

  it('strips an oversized cover URL too', () => {
    storeRaw({ results: [result({ album_img: `https://x/${'a'.repeat(3000)}` })] });
    expect(loadCloudSearchSnapshot().results[0].album_img).toBeUndefined();
  });

  it('caps the restored list at one page of results', () => {
    storeRaw({ results: Array.from({ length: 60 }, (_, i) => result({ id: `r${i}` })) });
    expect(loadCloudSearchSnapshot().results).toHaveLength(25);
  });

  it('keeps every optional field the card and the player read', () => {
    storeRaw({
      results: [
        result({
          title: 'T',
          album: 'Al',
          album_id: 'aid',
          genre: 'Pop',
          mid: 'm1',
          url: 'https://u/1',
          duration: 210,
        }),
      ],
    });
    expect(loadCloudSearchSnapshot().results[0]).toEqual(
      result({
        title: 'T',
        album: 'Al',
        album_id: 'aid',
        genre: 'Pop',
        mid: 'm1',
        url: 'https://u/1',
        duration: 210,
      }),
    );
  });
});

describe('saving', () => {
  it('round-trips through the loader', () => {
    saveCloudSearchSettings({
      query: 'test',
      multiSource: false,
      singleSource: 'kuwo',
      selectedSources: ['kuwo'],
    });
    saveCloudSearchResults([result({ id: 'x', title: 'T' })]);
    expect(loadCloudSearchSnapshot()).toEqual({
      query: 'test',
      multiSource: false,
      singleSource: 'kuwo',
      selectedSources: ['kuwo'],
      results: [result({ id: 'x', title: 'T' })],
    });
  });

  // The bug this split exists to prevent: one writer that carries the
  // other half along cannot tell "keep what was there" from "clear it",
  // and the settings write fires on mount — so it would have wiped the
  // results it had just restored.
  it('a settings change leaves the stored results alone', () => {
    saveCloudSearchSettings({
      query: 'a',
      multiSource: true,
      singleSource: 'netease',
      selectedSources: ['netease'],
    });
    saveCloudSearchResults([result({ id: 'keep' })]);
    saveCloudSearchSettings({
      query: 'b',
      multiSource: true,
      singleSource: 'netease',
      selectedSources: ['netease', 'qmusic'],
    });
    const s = loadCloudSearchSnapshot();
    expect(s.query).toBe('b');
    expect(s.results.map((r) => r.id)).toEqual(['keep']);
  });

  it('a results change leaves the stored settings alone', () => {
    saveCloudSearchSettings({
      query: 'keep me',
      multiSource: false,
      singleSource: 'kuwo',
      selectedSources: ['kuwo'],
    });
    saveCloudSearchResults([result({ id: 'new' })]);
    const s = loadCloudSearchSnapshot();
    expect(s.query).toBe('keep me');
    expect(s.multiSource).toBe(false);
    expect(s.selectedSources).toEqual(['kuwo']);
  });

  // The reason this module does not use writeJson: a swallowed
  // QuotaExceededError would cost the source selection too, and that is the
  // thing the user asked to stop redoing.
  it('keeps the settings when the results do not fit', () => {
    const real = localStorage.setItem.bind(localStorage);
    const big = JSON.stringify({
      ...CLOUD_SEARCH_DEFAULTS,
      query: 'keep me',
      results: Array.from({ length: 25 }, () => result({ name: 'x'.repeat(500) })),
    });
    localStorage.setItem = ((k: string, v: string) => {
      if (v.length > 1000) throw new Error('QuotaExceededError');
      real(k, v);
    }) as Storage['setItem'];
    try {
      saveCloudSearchSettings({
        query: 'keep me',
        multiSource: true,
        singleSource: 'netease',
        selectedSources: ['netease', 'qmusic'],
      });
      saveCloudSearchResults(
        Array.from({ length: 25 }, () => result({ name: 'x'.repeat(500) })),
      );
    } finally {
      localStorage.setItem = real;
    }
    expect(big.length).toBeGreaterThan(1000);
    const s = loadCloudSearchSnapshot();
    expect(s.query).toBe('keep me');
    expect(s.results).toEqual([]);
  });
});

describe('reconcileSnapshot', () => {
  const snap = {
    ...CLOUD_SEARCH_DEFAULTS,
    selectedSources: ['kugou', 'vanished'],
    singleSource: 'vanished',
  };

  it('drops a source this build no longer offers', () => {
    const out = reconcileSnapshot(snap, ['netease', 'kugou']);
    expect(out.selectedSources).toEqual(['kugou']);
  });

  // singleSource is what a single-source search actually sends, so leaving
  // a dead name there means that mode silently searches nothing.
  it('moves singleSource off a vanished source', () => {
    expect(reconcileSnapshot(snap, ['netease', 'kugou']).singleSource).toBe('kugou');
  });

  it('keeps singleSource when it is still live', () => {
    const s = { ...CLOUD_SEARCH_DEFAULTS, singleSource: 'kuwo', selectedSources: ['kuwo'] };
    expect(reconcileSnapshot(s, ['kuwo', 'kugou']).singleSource).toBe('kuwo');
  });

  it('falls back through the defaults when the whole selection is gone', () => {
    const out = reconcileSnapshot(
      { ...CLOUD_SEARCH_DEFAULTS, selectedSources: ['gone', 'also-gone'] },
      ['kuwo', 'migu'],
    );
    expect(out.selectedSources).toEqual(['kuwo']);
  });

  it('falls back to the first offered source when the defaults are gone too', () => {
    const out = reconcileSnapshot({ ...CLOUD_SEARCH_DEFAULTS, selectedSources: ['gone'] }, ['zzz']);
    expect(out.selectedSources).toEqual(['zzz']);
    expect(out.singleSource).toBe('zzz');
  });

  // The source list is not loaded yet on the first render; reconciling
  // against nothing must not wipe the restored selection.
  it('is a no-op before the source list arrives', () => {
    expect(reconcileSnapshot(snap, [])).toEqual(snap);
  });
});
