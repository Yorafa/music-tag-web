// Per-path notes for the rows a batch update refused to write.
//
// The batch endpoints answer with two buckets: `done` (written) and
// `skipped` (not written, each with a `status` and a `reason`). `skipped` is
// not one thing:
//
//   - status "duplicate"  — content evidence said this is already in the
//                           library, so the write was refused on purpose.
//   - status "not_audio"  — the row named something the tag pipeline does
//                           not write at all (a cover, a .lrc, or a name
//                           that is not an existing file). Nothing was even
//                           compared.
//
// Reporting both as "内容与库内文件完全一致" is wrong in a way the user acts
// on: the second is a fix-your-request problem, not a library-cleanup one,
// and it arrives with a `reason` that says which. So the reason is read from
// the entry and the status only supplies the fallback wording.

/** One `skipped` entry as the batch endpoints emit it. */
export interface SkippedEntry {
  file_full_path?: string;
  status?: string;
  reason?: string;
  match_field?: string;
  duplicate_path?: string;
}

/** The generic wording, used only when an entry carries no reason. */
const FALLBACK: Record<string, string> = {
  duplicate: '内容与库内文件完全一致',
  not_audio: '不是可写入标签的音频文件',
};

/**
 * Map every path in `skipped` to the note to show for it. Paths are the
 * keys because a row's identity is its path, and the caller walks its own
 * rows and asks about each one.
 */
export function skippedNotesFromUpdate(res: unknown): Map<string, string> {
  const notes = new Map<string, string>();
  if (typeof res !== 'object' || res === null) return notes;
  const env = res as { data?: { skipped?: unknown }; skipped?: unknown };
  const raw = env.data?.skipped ?? env.skipped;
  if (!Array.isArray(raw)) return notes;

  for (const item of raw) {
    if (typeof item !== 'object' || item === null) continue;
    const e = item as SkippedEntry;
    const path = typeof e.file_full_path === 'string' ? e.file_full_path : '';
    if (!path) continue;
    const reason = typeof e.reason === 'string' && e.reason.trim() ? e.reason : '';
    const status = typeof e.status === 'string' ? e.status : '';
    notes.set(path, reason || FALLBACK[status] || '服务端未写入该文件');
  }
  return notes;
}
