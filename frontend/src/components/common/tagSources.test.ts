import { describe, it, expect } from 'vitest';
import { SOURCES, toggleSourceSelection } from './tagSources';
import type { MusicSource } from '@/types';

describe('SOURCES', () => {
  it('is the set the batch toolbar and the detail dialog both offer', () => {
    // The list used to be private to WorkstationToolbar, so the detail
    // dialog could not offer a choice at all. Two copies of this list
    // would drift the same way, which is why it is shared.
    expect(SOURCES.length).toBeGreaterThan(1);
    expect(new Set(SOURCES.map((s) => s.id)).size).toBe(SOURCES.length);
  });
});

describe('toggleSourceSelection', () => {
  it('adds a source that was not selected', () => {
    expect(toggleSourceSelection(['netease'], 'qmusic')).toEqual(['netease', 'qmusic']);
  });

  it('removes a selected one', () => {
    expect(toggleSourceSelection(['netease', 'qmusic'], 'netease')).toEqual(['qmusic']);
  });

  it('refuses to remove the last source', () => {
    // A zero-source search finds nothing for a reason the user cannot see,
    // and reports it as "no matches found". Refusing here keeps the search
    // button's promise — that pressing it searches something.
    expect(toggleSourceSelection(['netease'], 'netease')).toEqual(['netease']);
  });

  it('does not mutate the array it was given', () => {
    const current: MusicSource[] = ['netease', 'qmusic'];
    toggleSourceSelection(current, 'netease');
    expect(current).toEqual(['netease', 'qmusic']);
  });
});
