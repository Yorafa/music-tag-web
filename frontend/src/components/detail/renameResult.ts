// Reading the rename out of an update_id3/ response.
//
// A row's identity in this app IS its path: `WorklistRow.id` and
// `LibraryRow.id` are both `fullPath`. So when the detail dialog's 文件名
// row renames a file on save, every row still holding the old path points
// at something that no longer exists — streaming it 404s, re-editing it
// fails, saving it again errors. The rename has to be reflected in the
// stores or the file has effectively been dropped from the queue.
//
// The client cannot work the new path out for itself. The handler
// appends the file's extension when the requested name lacks one, runs
// the name through `utils.SanitizePath`, expands `$artist` / `$title`
// templates, and refuses a target that already exists. Re-deriving any of
// that here would be a second implementation of rules that live in Go,
// free to drift. So the handler reports the base name it actually used
// and this module only turns that into the relative path the stores use.

import { asArray } from '@/api/envelope';

export interface UpdateDoneEntry {
  file_full_path?: string;
  status?: string;
  /** Present only when the call also renamed the file. */
  new_file_name?: string;
}

/** The new relative path for `oldPath`, or null when it did not move.
 *
 *  Accepts either the raw axios body (the `{result, code, data, message}`
 *  envelope) or an already-unwrapped `data`, because callers in this
 *  codebase sit on both sides of that boundary. */
export function renamedPathFromUpdate(
  res: unknown,
  oldPath: string,
): string | null {
  if (typeof res !== 'object' || res === null) return null;
  const env = res as { data?: { done?: unknown }; done?: unknown };

  // The report lives at `data.done` inside the envelope, and at `done`
  // when a caller already unwrapped it. Read both, envelope first, and
  // let asArray absorb anything that is neither.
  const done = asArray<UpdateDoneEntry>(env.data?.done ?? env.done);

  const entry = done.find((d) => d?.file_full_path === oldPath);
  const newName = entry?.new_file_name;
  if (typeof newName !== 'string' || newName === '') return null;

  return joinDir(oldPath, newName);
}

/** The file name part of a relative path, without a dependency on
 *  node's `path` (this ships to the browser and the stores already hold
 *  POSIX separators regardless of the host OS). */
export function baseNameOf(p: string): string {
  const cut = p.lastIndexOf('/');
  return cut === -1 ? p : p.slice(cut + 1);
}

/** Rebuild the relative path from the old one and the new base name.
 *
 *  A Dir+Base join, and nothing more, because the handler only ever
 *  renames within the same parent directory. Going through `path.join`
 *  rather than string surgery keeps the POSIX separators the stores
 *  already hold, even when the gateway runs on Windows. */
export function joinDir(oldPath: string, newBaseName: string): string {
  const cut = oldPath.lastIndexOf('/');
  return cut === -1 ? newBaseName : `${oldPath.slice(0, cut)}/${newBaseName}`;
}
