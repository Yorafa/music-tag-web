// Opening the song-detail Dialog for a row.
//
// This sequence used to be copy-pasted between PlayView and Worklist,
// differing only in which store received the fetched tags. It is a
// third site now (WorkstationView, which needs it because the scraper's
// detail panel is hidden on mobile), so it lives here: the ordering
// constraints below are subtle enough that a divergence between copies
// would be a silent bug rather than a visible one.
//
// Order matters:
//   1. Identity first (selectedFile + fullPath), because the race guard
//      later compares against them.
//   2. Optimistic hydration from whatever the row already has cached,
//      so the Dialog mounts with content instead of flashing empty.
//   3. Open.
//   4. Only then fetch, and only if the cache is too thin to trust —
//      `needsMusicInfoRefetch`, not "is any field set". The localStorage
//      snapshots keep title/artist but drop data-URI covers, so a
//      populated-looking cache still needs a refetch to restore art.

import { editorActions, useEditorStore } from '@/store/useEditorStore';
import { useNoticeStore } from '@/store/useNoticeStore';
import { needsMusicInfoRefetch } from '@/utils/persistMusicInfo';
import { readTagsFromPath } from '@/lib/id3Reader';
import type { MusicTagInfo } from '@/types';

export interface OpenEditorRow {
  fileName: string;
  fullPath: string;
  /** The row's lazily-cached tags, if any. */
  musicInfo?: Partial<MusicTagInfo> | null;
  /** Called with the freshly fetched tags so the caller can seed its
   *  own row cache and skip the round-trip next time. */
  cache?: (info: Partial<MusicTagInfo>) => void;
}

export async function openEditorForRow(row: OpenEditorRow): Promise<void> {
  const { fileName, fullPath, musicInfo, cache } = row;

  editorActions.setSelectedFile(fileName);
  editorActions.setFullPath(fullPath);
  editorActions.setFadeShowDetail(false);
  // A stale candidate list from a previously-opened file would otherwise
  // be shown next to this file's tags.
  editorActions.setSongList([]);
  editorActions.setMusicInfo(musicInfo ?? {});
  editorActions.setEditorOpen(true);

  if (!needsMusicInfoRefetch(musicInfo)) return;

  try {
    const info = await readTagsFromPath(fullPath);
    // Race guard: a newer click may already have switched the editor to
    // a different row. Without this, a slow response for Row A lands
    // after the user opened Row B and overwrites it.
    const stillCurrent =
      useEditorStore.getState().fullPath === fullPath &&
      useEditorStore.getState().selectedFile === fileName;
    if (!stillCurrent) return;

    // An all-null response means "fetch worked, file has no tags" —
    // keep the optimistic values rather than blanking the form.
    if (Object.values(info).some((v) => v != null)) {
      editorActions.setMusicInfo(info);
      cache?.(info);
    }
  } catch (err) {
    // Distinguish "the fetch failed" from "the file has no tags", which
    // the UI otherwise renders identically.
    const msg = err instanceof Error ? err.message : String(err);
    useNoticeStore.getState().push(`读取标签失败: ${msg}`, 'warn');
  }
}
