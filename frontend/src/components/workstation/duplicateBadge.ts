// How a duplicate verdict is presented, and which files a delete may touch.
//
// Split out of the components for two reasons: the badge labels and the
// delete-target rule are both logic with real failure modes, and logic that
// only exists inside a JSX branch is logic nothing can test.
//
// The delete rule is the important one. A row flagged `duplicate` names a
// `duplicate_path` — the copy to KEEP. Deleting the wrong side of a pair
// destroys the only tagged file and leaves the untagged one, which is
// strictly worse than doing nothing. So a bulk delete is only ever offered
// over rows the server itself called a content-level duplicate, and
// contradictory pairs are excluded (see deleteTargetsFor).

import type { RowDuplicate, WorklistRow } from '@/types';

export type DuplicateBadgeTone = 'duplicate' | 'likely' | 'checked';

export interface DuplicateBadgeSpec {
  label: string;
  tone: DuplicateBadgeTone;
  /** Tooltip. Always says which other file, when there is one — a bare
   *  "重复" badge is not actionable. */
  title: string;
}

/** Badge spec for a row's verdict, or null when there is nothing to show.
 *
 *  `likely_duplicate` is deliberately a separate, quieter tone: it is a
 *  name clash or similar metadata, which is a hint — not the same class of
 *  fact as two files with identical audio. Collapsing them would push the
 *  user toward deleting on weak evidence.
 *
 *  `skipped` / `error` get no badge at all. They mean the check could not
 *  conclude, and rendering "已查重" for them would read as a clean result. */
export function duplicateBadgeSpec(
  duplicate: RowDuplicate | undefined,
): DuplicateBadgeSpec | null {
  if (!duplicate) return null;
  const { verdict, duplicatePath, reason } = duplicate;
  const where = duplicatePath ? `：${duplicatePath}` : '';
  const why = reason ? `（${reason}）` : '';

  switch (verdict) {
    case 'duplicate':
      return {
        label: '重复',
        tone: 'duplicate',
        title: `与库内文件内容相同${where}${why}`,
      };
    case 'likely_duplicate':
      return {
        label: '疑似',
        tone: 'likely',
        title: `同名或元数据相似${where}${why}`,
      };
    case 'unique':
      return { label: '唯一', tone: 'checked', title: '未发现重复文件' };
    default:
      // skipped / error — see above.
      return null;
  }
}

/** The rows a 「删除重复文件」 action would actually delete.
 *
 *  Only `verdict === 'duplicate'` qualifies. That is the server's
 *  content-level verdict (SHA-256 or 声纹) — the same evidence that makes
 *  the write path refuse a tag overwrite. `likely_duplicate` is a name
 *  clash, and deleting on a name clash would remove a genuinely different
 *  recording that happens to share a filename.
 *
 *  Mutually-referencing pairs are excluded from BOTH sides. Within one
 *  checked batch A can be flagged as a copy of B while B is flagged as a
 *  copy of A: the funnel compares each against the other, and neither file
 *  has an inherent claim to be the original. The two designations
 *  contradict, so there is no safe pick — better to tell the user to look
 *  than to guess and possibly destroy the only tagged copy. */
export function deleteTargetsFor(rows: WorklistRow[]): WorklistRow[] {
  const flagged = rows.filter((r) => r.duplicate?.verdict === 'duplicate');
  const byId = new Map(flagged.map((r) => [r.id, r]));

  return flagged.filter((row) => {
    const other = row.duplicate?.duplicatePath;
    // A path outside this batch was not itself checked, so it cannot be
    // half of a contradiction — and it is the keeper by definition.
    if (!other) return true;
    const otherRow = byId.get(other);
    if (!otherRow) return true;
    // Contradictory: row says `other` is the keeper, and `other` says row is.
    return otherRow.duplicate?.duplicatePath !== row.id;
  });
}
