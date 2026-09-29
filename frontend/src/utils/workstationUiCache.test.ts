// Tests for the persisted 智能刮削 / 搜索目录 UI state.
//
// Both pieces read untrusted localStorage, and each has a specific silent
// failure mode worth pinning:
//
//   - The 状态筛选 decides what the table renders. A stored value no sidebar
//     chip matches leaves the user on a blank table with no visible way to
//     tell which filter is active or how to get back to 「所有曲目」.
//   - The 搜索目录 box filters the tree by substring. A stale value is
//     invisible — the tree just looks like it has no matches.

import { describe, it, expect, beforeEach, vi } from 'vitest';
import {
  FILTER_KEY,
  SEARCH_KEY,
  MAX_STORED_QUERY,
  sanitizeFilter,
  sanitizeSearchQuery,
} from './workstationUiCache';

describe('sanitizeFilter', () => {
  it('passes every real filter through', () => {
    for (const f of ['all', 'pending', 'scraped', 'failed', 'duplicate'] as const) {
      expect(sanitizeFilter(f)).toBe(f);
    }
  });

  it('falls back to all for an unknown value', () => {
    // The load-bearing case: an unknown filter renders a blank table with
    // no chip highlighted and no way back.
    expect(sanitizeFilter('nonsense')).toBe('all');
  });

  it('falls back to all for a near-miss value', () => {
    // Case and whitespace are not normalized away: a stored 'Failed' is a
    // value no chip matches, so coercing it silently would hide that the
    // stored value was not one this version wrote.
    expect(sanitizeFilter('Failed')).toBe('all');
    expect(sanitizeFilter(' failed')).toBe('all');
    expect(sanitizeFilter('failed ')).toBe('all');
  });

  it('falls back to all for absent / empty / null', () => {
    expect(sanitizeFilter(null)).toBe('all');
    expect(sanitizeFilter('')).toBe('all');
  });

  it('falls back to all for a JSON blob written by something else', () => {
    expect(sanitizeFilter('{"filter":"failed"}')).toBe('all');
  });
});

describe('sanitizeSearchQuery', () => {
  it('returns an empty string for absent input', () => {
    expect(sanitizeSearchQuery(null)).toBe('');
    expect(sanitizeSearchQuery('')).toBe('');
  });

  it('keeps an ordinary query intact', () => {
    expect(sanitizeSearchQuery('beatles')).toBe('beatles');
    expect(sanitizeSearchQuery('华语 经典')).toBe('华语 经典');
  });

  it('caps an absurdly long value', () => {
    // The cap exists so a 5000-char paste is not written to disk on every
    // keystroke, and so a stale wide-tree query cannot bloat storage.
    expect(sanitizeSearchQuery('x'.repeat(5000))).toHaveLength(MAX_STORED_QUERY);
  });

  it('treats a whitespace-only value as empty', () => {
    // A box holding only spaces filters the tree to nothing while looking
    // filled-in — indistinguishable from a search that found nothing.
    expect(sanitizeSearchQuery('   ')).toBe('');
    expect(sanitizeSearchQuery('\t\n')).toBe('');
  });

  it('trims surrounding whitespace but keeps inner spacing', () => {
    // Directory names contain spaces, so inner runs must survive.
    expect(sanitizeSearchQuery('  dark side  ')).toBe('dark side');
  });
});

/** Mount a fresh store with FILTER_KEY pre-seeded — what a reload sees.
 *  `extra` seeds other keys too, for the tests that check one write does not
 *  clobber another. Seeds are applied AFTER the clear. */
async function mountStore(
  stored: string | null,
  extra: Record<string, string> = {},
) {
  localStorage.clear();
  if (stored !== null) localStorage.setItem(FILTER_KEY, stored);
  for (const [k, v] of Object.entries(extra)) localStorage.setItem(k, v);
  vi.resetModules();
  const mod = await import('@/store/useWorklistStore');
  return mod.useWorklistStore;
}

describe('状态筛选 round trip', () => {
  beforeEach(() => {
    localStorage.clear();
    vi.resetModules();
  });

  it('defaults to all on a first visit', async () => {
    const store = await mountStore(null);
    expect(store.getState().filter).toBe('all');
  });

  it('restores a remembered filter', async () => {
    const store = await mountStore('failed');
    expect(store.getState().filter).toBe('failed');
  });

  it('writes the filter when it changes', async () => {
    const store = await mountStore(null);
    store.getState().setFilter('duplicate');
    expect(localStorage.getItem(FILTER_KEY)).toBe('duplicate');
  });

  it('survives a reload', async () => {
    const first = await mountStore(null);
    first.getState().setFilter('scraped');
    // Same storage, new module instance — what a refresh does.
    const second = await mountStore(localStorage.getItem(FILTER_KEY));
    expect(second.getState().filter).toBe('scraped');
  });

  it('coerces an unknown filter passed to setFilter', async () => {
    // Same guard as on load: a bad value must never reach the table,
    // whether it came from storage or from a caller.
    const store = await mountStore(null);
    store.getState().setFilter('bogus' as never);
    expect(store.getState().filter).toBe('all');
    // And the bad value must not be written back, or the next reload would
    // have to reject its own predecessor.
    expect(localStorage.getItem(FILTER_KEY)).toBe('all');
  });

  it('does not let a filter write clobber the queue', async () => {
    // Separate keys on purpose: one combined key would mean selecting a
    // filter rewrote (or cleared) the rows.
    const store = await mountStore(null, {
      'worklist.v1': JSON.stringify({
        rows: [
          {
            id: 'a/1.mp3',
            fullPath: 'a/1.mp3',
            fileName: '1.mp3',
            status: 'pending',
          },
        ],
      }),
    });
    store.getState().setFilter('failed');
    const rows = JSON.parse(localStorage.getItem('worklist.v1') || 'null');
    expect(rows.rows).toHaveLength(1);
  });

  it('does not let the queue write clobber the filter', async () => {
    const store = await mountStore(null);
    store.getState().setFilter('pending');
    await store.getState().enqueueDirs([]);
    expect(localStorage.getItem(FILTER_KEY)).toBe('pending');
  });
});

describe('storage key names', () => {
  it('are distinct from each other and from the queue', () => {
    // A collision here would mean one feature silently overwrites another.
    const keys = new Set([FILTER_KEY, SEARCH_KEY, 'worklist.v1']);
    expect(keys.size).toBe(3);
  });
});
