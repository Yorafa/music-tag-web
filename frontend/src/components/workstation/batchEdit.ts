// Deciding what a batch edit actually sends.
//
// The single-track form (TrackInspector) can afford to be careless about
// which fields it submits: the user is looking at one track, every input is
// seeded from that track's own tags, and a field left empty usually means the
// track has no value there. Spreading the whole form into the payload is
// fine.
//
// A batch of forty cannot. There is no "the track" — the form is one set of
// boxes applied to every selected file, so an untouched empty box means "I
// know nothing about this field", and submitting it as "" used to mean "this
// field is empty", which is how a bulk edit silently wipes the genre off
// every file in the selection.
//
// So each field carries its own instruction, and it is the instruction that
// travels, not the text:
//
//	不修改 (skip, the default) → the key is OMITTED from music_info
//	enabled + text             → the key carries the text
//	enabled + empty            → the key carries null, which the server reads
//	                             as "delete this tag"
//
// Omission is the default on purpose. The failure mode of the other default
// is a button that quietly destroys data; the failure mode of this one is a
// save that does nothing and says so, which the dialog prevents by disabling
// itself when nothing is enabled.
//
// This module is deliberately free of React so the rules can be tested
// directly — the same reason renameResult.ts and tabBar.ts are separate.

export interface BatchFieldDef {
  key: string;
  label: string;
  /** Long values (lyrics, comment) get a textarea rather than an input. */
  multiline?: boolean;
}

/** The fields this dialog exposes, in reading order.
 *
 *  `filename` is deliberately absent, and `album_img` too: artwork can only
 *  be replaced, not removed (removing an embedded picture is an id3v2-only
 *  operation, so a "clear" would work on MP3 and silently do nothing on
 *  FLAC). Offering a control that lies on some formats is worse than not
 *  offering it. Renaming has its own input, because it is a template rather
 *  than a value — see buildBatchPayload. */
export const BATCH_TAG_FIELDS: BatchFieldDef[] = [
  { key: 'title', label: '歌曲标题 (Title)' },
  { key: 'artist', label: '艺术家 (Artist)' },
  { key: 'album', label: '专辑 (Album)' },
  { key: 'albumartist', label: '专辑艺术家 (Album Artist)' },
  { key: 'genre', label: '流派 (Genre)' },
  { key: 'year', label: '年份 (Year)' },
  { key: 'tracknumber', label: '音轨号 (Track#)' },
  { key: 'discnumber', label: '光盘 (Disc#)' },
  { key: 'lyrics', label: '歌词 (Lyrics)', multiline: true },
  { key: 'comment', label: '备注与描述 (Comment)', multiline: true },
];

export interface FieldEdit {
  value: string;
  /** True means "leave this field alone on every selected file". */
  skip: boolean;
}

export type BatchFormState = Record<string, FieldEdit>;

/** A form where nothing is enabled — the state the dialog opens in.
 *
 *  Every field starts skipped, so opening the dialog and hitting save is a
 *  no-op rather than a wipe. The user opts IN to each field they mean. */
export function initialBatchForm(): BatchFormState {
  const form: BatchFormState = {};
  for (const f of BATCH_TAG_FIELDS) {
    form[f.key] = { value: '', skip: true };
  }
  return form;
}

/** The fields this form would change, in field-table order. */
export function batchChangedFields(form: BatchFormState): BatchFieldDef[] {
  return BATCH_TAG_FIELDS.filter((f) => form[f.key] && !form[f.key].skip);
}

export interface BatchPayloadOptions {
  /**
   * A filename template (`${artist} - ${title}`), or empty for no rename.
   *
   * See buildBatchPayload for why this is a template and not a filename.
   */
  renameTemplate?: string;
  /** Mirrors the single-track form: dedup runs unless the user opted out. */
  dedupeEnabled?: boolean;
}

export interface BatchPayload {
  music_info: Record<string, unknown>;
  /** Which tag fields this payload will change. */
  fields: BatchFieldDef[];
  /**
   * False when the payload would change nothing at all.
   *
   * The dialog disables its save button on this, because a request with an
   * empty music_info is not a harmless no-op: it is a write pass over every
   * selected file that produces one audit row saying the user edited
   * nothing.
   */
  hasChanges: boolean;
}

/**
 * Build the music_info for a batch edit.
 *
 * The three-state mapping is documented at the top of this file. The part
 * worth repeating here is the null: `null` is the server's "delete this tag"
 * (see handler.tagIntent), and it is the only representation of "clear".
 * An empty string is NOT one — on the server that has always meant "the
 * caller had nothing to say", and it still does, because the single-track
 * form submits a dozen untouched keys and reading them as deletions would
 * wipe tags the user never saw.
 *
 * `filename` is added only when a template is supplied, and that guard is
 * load-bearing rather than tidiness. `filename` is not a per-row value on
 * this endpoint: it is one shared string that the server expands per file
 * against that file's tags. Send a literal name and the handler asks every
 * selected file to become that one name — forty writes, one winner, and
 * thirty-nine collision failures the user did not ask for. The single-track
 * form seeds `filename` with the row's own current name, so an unchanged
 * value expands to a no-op; there is no such safe default here, because
 * forty different files have forty different current names. Hence: no
 * template, no key.
 */
export function buildBatchPayload(
  form: BatchFormState,
  options: BatchPayloadOptions = {},
): BatchPayload {
  const music_info: Record<string, unknown> = {};
  const fields = batchChangedFields(form);

  for (const f of fields) {
    const edit = form[f.key];
    const value = edit.value.trim();
    music_info[f.key] = value === '' ? null : value;
  }

  const template = (options.renameTemplate ?? '').trim();
  if (template !== '') {
    music_info.filename = template;
  }

  // check_duplicate lives inside music_info: the request struct has no
  // top-level field for it, so a sibling key is dropped by JSON binding
  // without an error. Only the opt-out is ever sent; the server defaults to
  // on. See utils/dedupe.
  if (options.dedupeEnabled === false) {
    music_info.check_duplicate = false;
  }

  return {
    music_info,
    fields,
    hasChanges: fields.length > 0 || template !== '',
  };
}

/** The select_data rows for a batch of relative paths.
 *
 *  Each row is the file's path relative to MUSIC_DIR and nothing else — no
 *  per-row `music_info`, which is what makes the server fall back to the
 *  shared map (handler.perEntry). Rows spanning directories are fine: the
 *  handler joins each name onto the (empty) base path and re-checks
 *  containment, so a relative path with slashes resolves normally and an
 *  escaping one is still refused. */
export function batchSelectData(
  relativePaths: string[],
): Array<{ name: string }> {
  return relativePaths.filter((p) => p !== '').map((name) => ({ name }));
}
