// Pure logic behind DirPickerDrawer, split out so it can be tested
// without a DOM (the project has no testing-library; see
// components/scraper/parseAssist.ts for the same shape).
//
// The bug this split was written for: the drawer only ever listed
// SUBdirectories (`children.filter(c => c.icon === 'icon-folder')`) and
// the directory you were standing in was never itself an option. So a
// loose audio file sitting directly in /music — music that lives in no
// artist or album folder — could not be selected from anywhere. The tree
// browser has a "载入全部根目录" shortcut that covers this case, but the
// picker, which is the affordance people reach for when they already know
// where the file is, did not.

import { AUDIO_EXTS } from '@/utils/expandDirs';
import { formatDisplayPath } from '@/utils/path';
import type { FileNode } from '@/types';

/** One selectable row: a subdirectory of the directory being browsed. */
export interface FolderRow {
  /** Relative path under MUSIC_DIR including this folder's name. */
  relPath: string;
  /** Display name (last path segment). */
  name: string;
}

/** One file row: something in the browsed directory that is not a
 *  directory. Every file is listed, not just audio — the request was to see
 *  the directory's whole contents — but only audio is selectable, because
 *  only audio becomes a queue row. A `cover.jpg` that can be ticked and
 *  then enqueues nothing is a broken-looking control. */
export interface FileRow {
  /** Relative path under MUSIC_DIR including the filename. */
  relPath: string;
  /** Display name (last path segment). */
  name: string;
  /** Whether ticking this row adds the file to the queue. */
  selectable: boolean;
  size?: number;
}

/** Compose two path segments into one relPath, never producing
 *  double-slashes. '' (root) + 'foo' → 'foo'; 'foo' + 'bar' → 'foo/bar'. */
export function appendToPath(base: string, name: string): string {
  return base ? `${base}/${name}` : name;
}

/** Strip the last segment of a relPath: 'foo/bar/baz' → 'foo/bar';
 *  '' → '' (root has no parent); 'foo' → ''. */
export function parentOf(relPath: string): string {
  const parts = relPath.split('/').filter(Boolean);
  parts.pop();
  return parts.join('/');
}

/** Case-insensitive extension check. AUDIO_EXTS is the single source of
 *  truth for what counts as audio (utils/expandDirs.ts) — importing it
 *  rather than re-listing extensions is the point: a file the scraper
 *  collects must be selectable here too. */
export function isAudioName(name: string): boolean {
  const ext = name.split('.').pop()?.toLowerCase() ?? '';
  return (AUDIO_EXTS as readonly string[]).includes(ext);
}

/** Convert a raw children array from /api/file_list/ into the folder-row
 *  subset we render. Audio files are not listed — they are reachable
 *  through `wholeDirLabel`'s row, which expands the directory instead of
 *  naming each file.
 *
 *  `currentDir` is the directory whose /api/file_list/ response produced
 *  `treeData`. Folded into each row's `relPath` so the selected-set keys
 *  and the chips display carry the full path under MUSIC_DIR — which is
 *  what enqueueDirs needs. Without the prefix every nested selection
 *  would be a bare folder name (e.g. '流行' instead of '华语/流行') and
 *  expandDirsToAudioFiles would fail the /api/file_list/ lookup,
 *  returning no files. */
export function folderRowsOf(
  treeData: FileNode[] | undefined,
  currentDir: string,
): FolderRow[] {
  const children = treeData?.[0]?.children ?? [];
  return children
    .filter((c) => c.icon === 'icon-folder')
    .map((c) => ({
      relPath: appendToPath(currentDir, c.name),
      name: c.name,
    }))
    .sort((a, b) => a.name.localeCompare(b.name));
}

/** Audio files sitting DIRECTLY in the browsed directory, not in a
 *  subdirectory. These are exactly the files that were unreachable before
 *  the directory itself became selectable, so the caller uses this to
 *  decide whether to surface that row. */
export function directAudioNames(treeData: FileNode[] | undefined): string[] {
  const children = treeData?.[0]?.children ?? [];
  return children
    .filter((c) => c.icon !== 'icon-folder' && isAudioName(c.name))
    .map((c) => c.name)
    .sort((a, b) => a.localeCompare(b));
}

/** The directory's own files, sorted, with the current dir folded into
 *  each path. Audio before the rest, because the selectable rows are the
 *  ones the user came here for and burying them under a folder's worth of
 *  cover art is how they get missed. */
export function fileRowsOf(
  treeData: FileNode[] | undefined,
  currentDir: string,
): FileRow[] {
  const children = treeData?.[0]?.children ?? [];
  return children
    .filter((c) => c.icon !== 'icon-folder')
    .map((c) => ({
      relPath: appendToPath(currentDir, c.name),
      name: c.name,
      selectable: isAudioName(c.name),
      size: c.size,
    }))
    .sort((a, b) => {
      if (a.selectable !== b.selectable) return a.selectable ? -1 : 1;
      return a.name.localeCompare(b.name);
    });
}

/** Label for the "select the directory you are in" row.
 *
 *  `''` is the music root, which has no name of its own — formatDisplayPath
 *  renders it as the stable `/music` alias the rest of the UI uses, so the
 *  row does not read as an empty checkbox. The parenthetical matters: this
 *  row expands to the whole subtree INCLUDING the audio sitting in this
 *  directory, which is not what the subdirectory rows below it do. */
export function wholeDirLabel(currentDir: string): string {
  return `整个 ${formatDisplayPath(currentDir)} 目录（含本层文件）`;
}

/** Whether the "select the directory you are in" row can add anything.
 *  False for a directory with neither subdirectories nor direct audio —
 *  an enabled checkbox that always enqueues zero files is worse than no
 *  checkbox, because the user cannot tell it apart from a broken one. */
export function wholeDirSelectable(
  treeData: FileNode[] | undefined,
  currentDir: string,
): boolean {
  return (
    folderRowsOf(treeData, currentDir).length > 0 ||
    directAudioNames(treeData).length > 0
  );
}
