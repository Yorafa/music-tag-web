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

export interface UpdateWarningEntry {
  file_full_path?: string;
  sidecar?: string;
  target?: string;
  reason?: string;
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

/** Human-readable notes about sidecars that did not follow their audio
 *  file, for a toast. Empty when the save was clean.
 *
 *  A sidecar is a separate file named after the track (`.lrc`) or its
 *  album (`cover-<album>.jpg`). Renaming a track moves the audio; if the
 *  sidecar cannot move with it — a read-only directory, a vanished
 *  source, a name clash the OS refuses — the save still counts as done,
 *  because the tags and the new name both landed. Reporting it as a
 *  failure would be a lie, and staying quiet would leave an orphan the
 *  user has no way to learn about. */
export function sidecarWarningsFromUpdate(res: unknown): string[] {
  if (typeof res !== 'object' || res === null) return [];
  const env = res as { data?: { warnings?: unknown }; warnings?: unknown };

  const warnings = asArray<UpdateWarningEntry>(env.data?.warnings ?? env.warnings);

  return warnings.map((w) => {
    const sidecar = typeof w?.sidecar === 'string' ? w.sidecar : 'sidecar';
    const target = typeof w?.target === 'string' ? w.target : '';
    const reason = typeof w?.reason === 'string' ? w.reason : '未知原因';
    return target
      ? `${sidecar} 未能改名为 ${target}：${reason}`
      : `${sidecar} 未能跟随重命名：${reason}`;
  });
}

export interface DuplicateWarningEntry {
  file_full_path?: string;
  verdict?: string;
  /** filename | hash | fingerprint | meta — which stage fired. */
  match_field?: string;
  duplicate_path?: string;
  reason?: string;
}

/** Notes about suspected duplicates that did NOT stop the write.
 *
 *  A different channel from `skipped`, and the distinction matters: a
 *  skipped entry means the tags were not written, while everything here
 *  means they were. These are name clashes and metadata-similar tracks —
 *  weak evidence the server deliberately refuses to treat as grounds for
 *  refusing a save — surfaced so the user learns the library holds two
 *  copies without losing their edit. */
export function duplicateWarningsFromUpdate(res: unknown): string[] {
  if (typeof res !== 'object' || res === null) return [];
  const env = res as { data?: { duplicate_warnings?: unknown }; duplicate_warnings?: unknown };

  const warnings = asArray<DuplicateWarningEntry>(
    env.data?.duplicate_warnings ?? env.duplicate_warnings,
  );

  return warnings.map((w) => {
    const other = typeof w?.duplicate_path === 'string' ? w.duplicate_path : '';
    const reason = typeof w?.reason === 'string' ? w.reason : '疑似重复';
    return other ? `疑似重复（${reason}）：${other}` : `疑似重复：${reason}`;
  });
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

/** Every path that moved, keyed by the path the request used.
 *
 *  `renamedPathFromUpdate` answers "where did THIS row go", which is the
 *  question a single-track save asks. A batch asks it once per file, and
 *  answering by re-scanning the report per row makes the cost O(n²) for a
 *  report that already holds every answer — cheap at forty rows, but the
 *  wrong shape to grow with.
 *
 *  Same reasoning as the single-row version for reading the report itself:
 *  the client does not re-derive the new path, the handler reports the base
 *  name it used, and this only joins it back onto the row's directory. */
export function renamedPathsFromUpdate(res: unknown): Map<string, string> {
  const moved = new Map<string, string>();
  if (typeof res !== 'object' || res === null) return moved;
  const env = res as { data?: { done?: unknown }; done?: unknown };
  const done = asArray<UpdateDoneEntry>(env.data?.done ?? env.done);

  for (const entry of done) {
    const from = entry?.file_full_path;
    const newName = entry?.new_file_name;
    if (typeof from !== 'string' || from === '') continue;
    if (typeof newName !== 'string' || newName === '') continue;
    moved.set(from, joinDir(from, newName));
  }
  return moved;
}
