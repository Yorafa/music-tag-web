// Plan A & B share this util — directory-list → flat audio-file list.
//
// One source of truth for what counts as an "audio file" across both modes
// (DESIGN.md §C1 pick). If play silently drops `.ape` while scrape
// collects it, users will be confused about "where are the files I
// added"; whether a file is *playable* is the PlayerBar's concern (push
// a `'warn'` notice on unplayable stream), decoupled from the whitelist.
//
// Committed early on a base branch so Plan B can re-use it without
// blocking on this PR (Plan-A §Handshake).

import { getFileList } from '@/api/client';
import type { FileNode } from '@/types';

/** Audio-file extensions accepted by both play and scrape modes.
 *
 *  Source of truth per DESIGN.md §Data Flow. Note: includes `mp4` (audio
 *  stream of an mp4 container), excludes `dff` (a niche DSD format
 *  without broad toolchain support). The legacy `utils/audioTypes.ts`
 *  ALLOWED_TYPES array — which FileBrowser / SearchResults used to
 *  filter their right-panel lists — differs (`dff` instead of `mp4`).
 *  Those components are deleted at the end of PR2; do not extend or
 *  import from audioTypes.ts. */
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
  /** The FileNode exactly as the backend sent it; carries its
   *  short-lived `id` from this single `/api/file_list/` response. */
  file: FileNode;
  /** Relative path under MUSIC_DIR, including the filename. Matches
   *  the same string the backend's SafeJoin(MUSIC_DIR, fullPath)
   *  would resolve to — so it can be fed back into /api/music_id3/,
   *  /api/update_id3/, etc. without further transformation. */
  fullPath: string;
  /** The top-level directory that expansion started from. Used by the
   *  Worklist / Library store to drop a re-added dir's whole batch as a
   *  single dedupe unit, rather than per-file. */
  sourceDir: string;
}

/** Case-insensitive extension check against AUDIO_EXTS. */
function isAudioExt(name: string): boolean {
  const ext = name.split('.').pop()?.toLowerCase() ?? '';
  return AUDIO_EXTS.includes(ext);
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

/** The containing directory of a relPath; '' for a top-level entry. */
function parentPath(relPath: string): string {
  const i = relPath.lastIndexOf('/');
  return i === -1 ? '' : relPath.slice(0, i);
}

/** A single selected file as the one ExpandedFile it expands to.
 *
 *  `sourceDir` is the directory it sits in, so the dir-granular dedupe and
 *  the "已收录 N 个目录" count still talk about real directories rather
 *  than inventing one per file. The FileNode is synthetic: the picker
 *  already had the real one, but expandDirs works from paths alone (it is
 *  also called with paths the picker never saw), and every consumer of
 *  ExpandedFile reads only `file.name`. */
function fileEntry(fullPath: string): ExpandedFile {
  const name = fullPath.slice(fullPath.lastIndexOf('/') + 1);
  return {
    file: { id: 0, name, title: name, icon: 'icon-audio', state: '' },
    fullPath,
    sourceDir: parentPath(fullPath),
  };
}

/** Build the relative path of a child node under `currentPath`. The
 *  backend's `file_list` response nests `FileNode.children` recursively,
 *  so callers walk into subdirs by visiting `currentPath/child.name`.
 *  Strip trailing `/` from currentPath so the resulting string never has
 *  a double-slash — `SafeJoin` would still tolerate it but no point. */
function joinPath(currentPath: string, name: string): string {
  const base = currentPath.replace(/\/+$/, '');
  return base ? `${base}/${name}` : name;
}

/** Recursively fetch one directory's contents and append audio files to
 *  `out`. Sequential per-directory: the backend doesn't fan out on
 *  parallel `/api/file_list/` calls very well (uses sync DB lookups
 *  inside each call), and one user's typical expansion is a handful of
 *  dirs — so a simple await-loop keeps the load behaviour predictable
 *  and avoids hammering the backend. */
async function expandOne(
  rootDir: string,
  currentPath: string,
  out: ExpandedFile[],
): Promise<void> {
  let res;
  try {
    res = await getFileList(currentPath);
  } catch {
    // 401 auto-logs-out via the response interceptor; other failures
    // (404, 500) silently skip this subtree so a single bad dir
    // doesn't torpedo the whole batch.
    return;
  }
  if (!res?.result || !Array.isArray(res.data)) return;
  // Backend's file_list response is shaped as [{ ..., children: [...] }]
  // — the root is the first element. Subdirs' children follow the same
  // shape. We always want the root's children list.
  const root = res.data[0];
  const children = root?.children ?? [];
  for (const child of children) {
    if (child.icon === 'icon-folder') {
      await expandOne(rootDir, joinPath(currentPath, child.name), out);
      continue;
    }
    if (!isAudioExt(child.name)) continue;
    out.push({
      file: child,
      fullPath: joinPath(currentPath, child.name),
      sourceDir: rootDir,
    });
  }
}

/** Expand a list of user-selected directories into a flat array of
 *  audio files, tagged per source dir for dedupe-at-dir granularity
 *  downstream. Files recurively nested under any subdir of the input
 *  are included; non-audio files (images, sidecar `.lrc`, etc.) are
 *  filtered out.
 *
 *  The output is ordered by directory then by BFS traversal inside each
 *  dir — preserves the user's "first dir, then subdir" mental model so
 *  the Worklist fills from the top down.
 *
 *  Edge cases:
 *  - `dirs` empty → returns `[]` without any HTTP call.
 *  - An entry that names an audio FILE (the directory picker's per-file
 *    rows) expands to that one file, with no HTTP call at all.
 *  - A directory does not exist (404) or backend errors on it → silently
 *    skipped; other directories still expand.
 *  - Same directory listed twice → both passes run and both contribute
 *    ExpandedFiles. Downstream callers (useWorklistStore) dedupe by
 *    fullPath so the user sees one row, not two. */
export async function expandDirsToAudioFiles(
  dirs: string[],
): Promise<ExpandedFile[]> {
  if (dirs.length === 0) return [];
  const out: ExpandedFile[] = [];
  // Sequential — see expandOne(). For typical workloads (a handful of
  // dirs) the latency is dominated by per-call DB lookups on the
  // backend, not by RTT, so parallelism wouldn't help and just
  // complicates error handling.
  for (const dir of dirs) {
    // A file the user ticked in the picker is already the answer; asking
    // the backend to list it as a directory would return nothing.
    if (selectionKind(dir) === 'file') {
      out.push(fileEntry(dir));
      continue;
    }
    await expandOne(dir, dir, out);
  }
  return out;
}
