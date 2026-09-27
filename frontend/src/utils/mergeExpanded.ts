// Merge freshly-expanded directory contents into a store that already holds
// some of the same files.
//
// WHY THIS IS NOT A ONE-LINER: the previous behaviour deduped at *directory*
// granularity — "if ANY file from a source dir is already present, drop the
// whole dir's batch". That reads like an optimisation and is in fact a way of
// making a directory permanently closed to new files. A youtube download lands
// in the library root; the user had already added the root once; every
// subsequent "添加整个目录" matched at least one old file and therefore dropped
// the new one too. Reproduced in a browser: 4 files in the worklist, a 5th
// downloaded into the same directory, re-add → still 4.
//
// Dedupe is therefore per FILE, which is also what the notice already claimed:
// "已收录 N 个新增目录、跳过 M 个重复" counts directories, so `added` and
// `skipped` are both directory counts, and a directory counts as "added" when
// it contributed at least one file the store did not have. Re-adding a
// directory whose files are all known still reports it as skipped, so the
// user-visible behaviour for the no-new-files case is unchanged.

/** The two fields mergeExpandedDirs needs; ExpandedFile and both stores' row
 *  shapes satisfy it, which is what lets the stores share this. */
export interface DirMergeSource {
  fullPath: string;
  sourceDir: string;
}

export interface DirMergeResult<T> {
  /** Entries whose fullPath was in neither the store nor this batch. */
  fresh: T[];
  /** Source dirs that contributed at least one fresh entry. */
  addedDirs: number;
  /** Source dirs the expansion touched but which contributed nothing new. */
  skippedDirs: number;
  /** The added dirs by name — the caller needs these to bookkeep its badges. */
  addedDirNames: string[];
}

export function mergeExpandedDirs<T extends DirMergeSource>(
  expanded: T[],
  existingPaths: Iterable<string>,
): DirMergeResult<T> {
  const existing = new Set(existingPaths);
  const seenInBatch = new Set<string>();
  const touched = new Set<string>();
  const addedDirNames: string[] = [];
  const fresh: T[] = [];

  for (const entry of expanded) {
    touched.add(entry.sourceDir);
    // Two reasons to skip: the store already has it, or an earlier entry in
    // THIS batch contributed the same path (two selected dirs can share a
    // child). Both produce a duplicate row otherwise.
    if (existing.has(entry.fullPath) || seenInBatch.has(entry.fullPath)) continue;
    seenInBatch.add(entry.fullPath);
    fresh.push(entry);
    if (!addedDirNames.includes(entry.sourceDir)) addedDirNames.push(entry.sourceDir);
  }

  return {
    fresh,
    addedDirs: addedDirNames.length,
    skippedDirs: [...touched].filter((d) => !addedDirNames.includes(d)).length,
    addedDirNames,
  };
}
