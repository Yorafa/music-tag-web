import { describe, it, expect } from 'vitest';
import { usageLine, sourceRows, guardNote, clearSummary } from './audioCacheText';
import type { AudioCacheClearResult, AudioCacheUsage } from '@/api/client';

function usage(over: Partial<AudioCacheUsage> = {}): AudioCacheUsage {
  return {
    root: '/tmp/audio_cache',
    exists: true,
    bytes: 1024,
    bytes_human: '1.0 KiB',
    files: 2,
    by_source: {
      youtube: { bytes: 1024, bytes_human: '1.0 KiB', files: 2 },
    },
    auto_prune: {
      enabled: true,
      max_mb: 2048,
      max_human: '2.0 GiB',
      min_age_minutes: 30,
    },
    min_age_minutes: 30,
    ...over,
  };
}

function clearResult(over: Partial<AudioCacheClearResult> = {}): AudioCacheClearResult {
  return {
    removed: 12,
    freed_bytes: 1024,
    freed_human: '1.0 KiB',
    kept_recent: 0,
    kept_recent_human: '0 B',
    failed: {},
    all: false,
    before_human: '2.0 KiB',
    after: usage(),
    ...over,
  };
}

describe('usageLine', () => {
  it('says it is still loading rather than showing a zero', () => {
    expect(usageLine(null)).toBe('正在读取…');
  });

  it('reports a cache directory that does not exist yet', () => {
    // A fresh install has none. Rendering that as "0 B" would be true and
    // would also read as "everything got cleaned up".
    expect(usageLine(usage({ exists: false }))).toBe('缓存目录尚未创建');
  });

  it('includes the cap when the automatic prune is on', () => {
    expect(usageLine(usage())).toBe('1.0 KiB · 2 个文件 · 上限 2.0 GiB');
  });

  it('says the cap is off instead of leaving the number out', () => {
    const line = usageLine(
      usage({
        auto_prune: { enabled: false, max_mb: 0, max_human: '0 B', min_age_minutes: 30 },
      }),
    );
    expect(line).toContain('未设置自动清理上限');
  });
});

describe('sourceRows', () => {
  it('is empty while loading', () => {
    expect(sourceRows(null)).toEqual([]);
  });

  it('orders the source holding the most files first', () => {
    // A Go map has no order, so an unsorted render would reshuffle between
    // refreshes and the row the user was reading would move.
    const rows = sourceRows(
      usage({
        by_source: {
          youtube: { bytes: 10, bytes_human: '10 B', files: 1 },
          migu: { bytes: 20, bytes_human: '20 B', files: 9 },
        },
      }),
    );
    expect(rows.map((r) => r.source)).toEqual(['migu', 'youtube']);
  });
});

describe('guardNote', () => {
  it('names the window and both reasons it exists', () => {
    const note = guardNote(usage());
    expect(note).toContain('30 分钟');
    expect(note).toContain('正在播放');
  });

  it('warns that a zero window protects nothing', () => {
    expect(guardNote(usage({ min_age_minutes: 0 }))).toContain('正在播放的文件也会被删除');
  });
});

describe('clearSummary', () => {
  it('does not claim success when nothing was eligible', () => {
    expect(clearSummary(clearResult({ removed: 0 }))).toBe('缓存已经是空的。');
  });

  it('explains an empty result that was really the guard', () => {
    const s = clearSummary(
      clearResult({ removed: 0, kept_recent: 3, kept_recent_human: '30 MiB' }),
    );
    expect(s).toContain('保留窗口');
    expect(s).toContain('30 MiB');
  });

  it('states what was kept, not only what was removed', () => {
    const s = clearSummary(
      clearResult({ kept_recent: 2, kept_recent_human: '8 MiB' }),
    );
    expect(s).toContain('已清理 12 个文件');
    expect(s).toContain('保留 2 个（8 MiB）');
  });

  it('surfaces per-file failures rather than rounding to success', () => {
    const s = clearSummary(clearResult({ failed: { '/x.ogg': 'permission denied' } }));
    expect(s).toContain('1 个删除失败');
  });

  it('omits clauses for things that did not happen', () => {
    const s = clearSummary(clearResult());
    expect(s).toBe('已清理 12 个文件，释放 1.0 KiB。');
  });
});
