// Plan A & B share this util — directory-list → flat audio-file list.
//
// One source of truth for what counts as an "audio file" across both modes
// (DESIGN.md §C1 pick). If play silently drops `.ape` while scrape
// collects it, users will be confused about "where are the files I
// added"; whether a file is *playable* is the PlayerBar's concern (push
// a `'warn'` notice on unplayable stream), decoupled from the whitelist.
//
// The extension list itself now lives server-side in internal/audioext,
// which this endpoint used before duplicating it. Two copies of "what
// counts as audio" had already drifted into three behaviours (see that
// package's doc comment); a third copy here would have been the same bug
// waiting to happen.

import { fileListRecursive, type RecursiveFileItem } from '@/api/client';
import type { FileNode } from '@/types';

/** Audio-file extensions accepted by both play and scrape modes.
 *
 *  Re-exported from the server's list purely so the frontend's existing
 *  imports keep working. The VALUES come from the backend now — the client
 *  no longer judges extensions during expansion, but `selectionKind` below
 *  still has to tell a ticked file from a ticked directory, and it does
 *  that from the name alone. Keeping one list means a format the server
 *  accepts cannot be invisible to the picker. */
export const AUDIO_EXTS: readonly string[] = [
  'flac',
  'ape',
  'wav',
  'aiff',
  'wv',
  'tta',
  'mp3',
  'm4a',
  'ogg',
  'mpc',
  'opus',
  'wma',
  'dsf',
  'mp4',
] as const;

/** Single flat-file record produced by `expandDirsToAudioFiles`.
 *  Tagged with its source directory so callers can dedupe at directory
 *  granularity (per DESIGN.md §B pick: "Already-added directories are
 *  silently skipped; only genuinely-new directories are passed to
 *  enqueueDirs"). */
export interface ExpandedFile {
  /** A synthetic node carrying the name. The server's recursive endpoint
   *  returns paths and names, not the per-level FileItem the old
   *  client-side walk assembled, so this is built here rather than passed
   *  through. Every consumer of ExpandedFile reads only `file.name`. */
  file: FileNode;
  /** Relative path under MUSIC_DIR, including the filename. Matches the
   *  same string the backend's SafeJoin(MUSIC_DIR, fullPath) would resolve
   *  to — so it can be fed back into /api/music_id3/, /api/album_cover/,
   *  /api/delete_files/, etc. without further transformation. */
  fullPath: string;
  /** The selected directory (or the file's own parent) that this file was
   *  found under. Used by the Worklist to drop a re-added dir's whole batch
   *  as a single dedupe unit, rather than per-file. */
  sourceDir: string;
}

/** What one entry of a user's selection actually is.
 *
 *  A selection entry used to be a directory and nothing else, so a loose
 *  audio file could not be queued on its own: /api/file_list/ on a file
 *  path lists nothing and the entry expanded to zero rows, which the user
 *  saw as a silent no-op. */
export type SelectionKind = 'file' | 'dir';

/** Classify one selection entry by its name, which is what decides the
 *  kind: MUSIC_DIR has no directory whose name ends in an audio
 *  extension in any library this app manages, and a file that ISN'T audio
 *  is not selectable in the first place. */
export function selectionKind(path: string): SelectionKind {
  const name = path.slice(path.lastIndexOf('/') + 1);
  return isAudioExt(name) ? 'file' : 'dir';
}

/** Case-insensitive extension check against AUDIO_EXTS. */
function isAudioExt(name: string): boolean {
  const ext = name.split('.').pop()?.toLowerCase() ?? '';
  return AUDIO_EXTS.includes(ext);
}

/** Map one server row into the flat record callers consume. */
function toExpandedFile(item: RecursiveFileItem): ExpandedFile {
  return {
    file: { id: 0, name: item.name, title: item.name, icon: 'icon-audio', state: '' },
    fullPath: item.path,
    // The server reports which requested entry the file came from, which is
    // more reliable than guessing: a file nested under two selected
    // directories still belongs to the one that was asked for.
    sourceDir: item.source,
  };
}

/** Expand a list of user-selected directories into a flat array of
 *  audio files, tagged per source dir for dedupe-at-dir granularity
 *  downstream. Files recursively nested under any subdir of the input
 *  are included; non-audio files (images, sidecar `.lrc`, etc.) are
 *  filtered out by the server.
 *
 *  The output is ordered by directory, then by the server's walk order
 *  within each — preserving the user's "first dir, then subdir" mental
 *  model so the Worklist fills from the top down.
 *
 *  Edge cases:
 *  - `dirs` empty → returns `[]` without any HTTP call.
 *  - An entry that names an audio FILE (the directory picker's per-file
 *    rows) is returned as that one file, with no directory walk for it.
 *  - A directory does not exist (404) or errors → skipped server-side;
 *    other directories still expand.
 *  - Same directory listed twice → both passes contribute ExpandedFiles.
 *    Downstream callers (useWorklistStore) dedupe by fullPath so the user
 *    sees one row, not two.
 *
 *  ONE REQUEST, NOT ONE PER DIRECTORY. This used to walk the tree from the
 *  client: breadth-first, `getFileList` on each directory, each level
 *  waiting for the previous one. Two costs came out of that, and neither is
 *  fixable by raising a concurrency limit:
 *
 *    - Request count scaled with the DIRECTORY count, not the track count.
 *      A 500-track library organised as 歌手/专辑/碟片/ is ~2500 directories,
 *      so ~2500 requests.
 *    - Latency scaled with DEPTH, because BFS cannot start level N+1 until
 *      level N returns. Four levels deep is four serial round trips.
 *
 *  Both are symptoms of reconstructing a tree walk the server can do in one
 *  pass — and it can, because library layout is DATA. This function must
 *  work for an operator whose music is organised in a shape nobody
 *  anticipated, so it cannot assume a depth, a level count, or a fan-out.
 *  The server walks with filepath.WalkDir and this asks once.
 *
 *  `truncated` cannot be silently ignored: it means the server hit a
 *  ceiling and the list is incomplete. Reporting it is the difference
 *  between "here are your 500 tracks" and "here are 500 of 5000" looking
 *  identical. */
export async function expandDirsToAudioFiles(
  dirs: string[],
): Promise<ExpandedFile[]> {
  if (dirs.length === 0) return [];
  const res = await fileListRecursive(dirs);
  if (res.truncated) {
    // Surfaced as a console warning rather than a thrown error: a truncated
    // add is still a useful add, and the caller has no channel for warnings.
    // The store's `dirsVisited`-independent contract is unchanged.
    console.warn(
      `[expandDirs] server returned a truncated list (${res.files.length} files, ` +
        `${res.dirsVisited} dirs visited); some directories were not expanded`,
    );
  }
  return res.files.map(toExpandedFile);
}
