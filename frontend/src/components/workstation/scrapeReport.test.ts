import { describe, it, expect } from 'vitest';
import {
  noMatchReason,
  sourceLabel,
  summarizeScrape,
  type ScrapeRowOutcome,
} from './scrapeReport';
import { SOURCES } from '@/components/common/tagSources';

function row(partial: Partial<ScrapeRowOutcome>): ScrapeRowOutcome {
  return {
    fullPath: 'a/b.flac',
    fileName: 'b.flac',
    status: 'no_match',
    source: null,
    ...partial,
  };
}

describe('summarizeScrape', () => {
  it('counts each outcome separately', () => {
    // The point of the whole module: "成功 2，未匹配 1" cannot distinguish
    // a track nobody has from a writer refusal from a network error.
    const s = summarizeScrape([
      row({ status: 'written', source: 'netease' }),
      row({ status: 'written', source: 'netease' }),
      row({ status: 'no_match' }),
      row({ status: 'refused' }),
      row({ status: 'error' }),
    ]);
    expect(s).toMatchObject({
      total: 5,
      written: 2,
      noMatch: 1,
      refused: 1,
      errored: 1,
    });
  });

  it('ranks the sources that actually answered, most wins first', () => {
    const s = summarizeScrape([
      row({ status: 'written', source: 'kuwo' }),
      row({ status: 'written', source: 'netease' }),
      row({ status: 'written', source: 'netease' }),
      row({ status: 'written', source: 'qmusic' }),
    ]);
    expect(s.sourcesUsed).toEqual(['netease', 'kuwo', 'qmusic']);
  });

  it('does not credit a source for rows that were never written', () => {
    // A source that returned a candidate which the writer then refused
    // did not win the row; naming it as the source of the tag would be
    // answering a question nobody asked.
    const s = summarizeScrape([
      row({ status: 'refused', source: 'netease' }),
      row({ status: 'no_match', source: 'kuwo' }),
    ]);
    expect(s.sourcesUsed).toEqual([]);
  });

  it('handles an empty run', () => {
    expect(summarizeScrape([])).toMatchObject({ total: 0, written: 0, sourcesUsed: [] });
  });
});

describe('sourceLabel', () => {
  it('uses the display name the picker shows', () => {
    expect(sourceLabel('netease', SOURCES)).toBe('网易云音乐');
  });

  it('passes an unknown id through rather than hiding it', () => {
    expect(sourceLabel('some_new_plugin', SOURCES)).toBe('some_new_plugin');
  });
});

describe('noMatchReason', () => {
  it('leads with a failing source, because that is the actionable one', () => {
    expect(noMatchReason(['netease'], ['kuwo'], false)).toContain('请求失败');
  });

  it('says which sources came back empty when none failed', () => {
    expect(noMatchReason(['netease', 'qmusic'], [], false)).toBe(
      '2 个音源均无结果：netease、qmusic',
    );
  });

  it('blames the auto-apply switch when the search was never the problem', () => {
    // A row with candidates and 自动采纳 off produced no tags; reporting
    // "no source had this track" would be a lie the user cannot check.
    expect(noMatchReason([], [], true)).toContain('自动采纳');
  });

  it('falls back to a plain statement when nothing else explains it', () => {
    expect(noMatchReason([], [], false)).toBe('所选音源没有返回候选');
  });
});
