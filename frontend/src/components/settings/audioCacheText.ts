import type { AudioCacheClearResult, AudioCacheUsage } from '@/api/client';

// Wording for the download-cache section, kept out of the component so it
// can be tested without a DOM and so the numbers it states are auditable
// in one place.
//
// Every size here is one the server already formatted (`bytes_human`).
// utils/formatBytes labels the same number "GB" where the server says
// "GiB", and a section showing both reads as two different quantities.

/** The one-line size summary, e.g. "1.2 GiB · 340 个文件 · 上限 2.0 GiB". */
export function usageLine(usage: AudioCacheUsage | null): string {
  if (!usage) return '正在读取…';
  if (!usage.exists) return '缓存目录尚未创建';
  const size = `${usage.bytes_human} · ${usage.files} 个文件`;
  if (!usage.auto_prune.enabled) return `${size} · 未设置自动清理上限`;
  return `${size} · 上限 ${usage.auto_prune.max_human}`;
}

/** Per-source rows for the breakdown, largest first.
 *
 *  Sorted so the source actually eating the disk is the first thing read;
 *  an unsorted map would render in insertion order, which for a Go map is
 *  neither stable nor meaningful. */
export function sourceRows(usage: AudioCacheUsage | null): Array<{
  source: string;
  files: number;
  bytesHuman: string;
}> {
  if (!usage) return [];
  return Object.entries(usage.by_source)
    .map(([source, u]) => ({ source, files: u.files, bytesHuman: u.bytes_human }))
    .sort((a, b) => b.files - a.files || a.source.localeCompare(b.source));
}

/** The sentence explaining what the clear will and will not touch. */
export function guardNote(usage: AudioCacheUsage | null): string {
  const min = usage?.min_age_minutes ?? 0;
  if (min <= 0) {
    return '没有保留窗口：正在播放的文件也会被删除。';
  }
  return `${min} 分钟内写入的文件会保留——正在播放的那首，和刚下载、正准备复制进曲库的那次下载。`;
}

/** The result sentence after a clear.
 *
 *  Every clause is conditional on something actually happening. A clear
 *  that removed nothing must not read as a success, and one that left
 *  files behind must say so: "cleared 12 files" alone would let the user
 *  conclude the cache is empty when 3 recent ones are still on disk. */
export function clearSummary(result: AudioCacheClearResult): string {
  if (result.removed === 0) {
    if (result.kept_recent > 0) {
      return `没有可清理的文件：${result.kept_recent} 个都在保留窗口内（${result.kept_recent_human}）。`;
    }
    return '缓存已经是空的。';
  }
  const parts = [`已清理 ${result.removed} 个文件，释放 ${result.freed_human}`];
  if (result.kept_recent > 0) {
    parts.push(`保留 ${result.kept_recent} 个（${result.kept_recent_human}）`);
  }
  const failed = Object.keys(result.failed ?? {}).length;
  if (failed > 0) {
    parts.push(`${failed} 个删除失败，详见操作审计`);
  }
  return `${parts.join('，')}。`;
}
