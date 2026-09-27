import axios from 'axios';
import { useAuthStore } from '@/store/useAuthStore';
import { unwrapEnvelope, asArray } from '@/api/envelope';
import type { RowDuplicate, SourceInfo } from '@/types';

const api = axios.create({
  baseURL: '/api/',
  headers: {
    'Content-Type': 'application/json',
  },
});

// Request interceptor: pull the JWT access token from the auth store and
// emit it as 'Authorization: JWT <token>' — the prefix the Go gateway
// middleware/auth.go::JWTAuth recognizes (alongside 'Bearer ').
//
// We deliberately do NOT read or write any cookie here. The Go Login
// handler returns the token in JSON only (handler/auth.go::Login) and
// never c.SetCookie(AUTHORIZATION,…), so a cookie-based flow would have
// nothing to read. The token is instead persisted to localStorage by
// useAuthStore (see store/useAuthStore.ts) so a page reload keeps the
// session. JWT TTL is 7 days server-side (handler/auth.go::generateJWT),
// so storage exposure is bounded to that window — acceptable for a
// self-hosted admin tool. The HttpOnly-cookie alternative is a backend
// change left out of this scope.
api.interceptors.request.use((config) => {
  const token = useAuthStore.getState().accessToken;
  if (token) {
    config.headers.Authorization = `JWT ${token}`;
  }
  return config;
});

// Response interceptor: on 401, force logout so user sees LoginPage again
api.interceptors.response.use(
  (response) => response,
  (error) => {
    if (error?.response?.status === 401) {
      useAuthStore.getState().logout();
    }
    return Promise.reject(error);
  }
);

export async function getFileList(filePath: string, sortedFields: string[] = []) {
  const { data } = await api.post('file_list/', { file_path: filePath, sorted_fields: sortedFields });
  return data;
}

/** POST /api/music_id3/ — server-side embedded-tag read (dhowden/tag).
 *  filePath is the parent dir relative to MUSIC_DIR ('' = root);
 *  fileName is the audio basenamed under that dir. */
export async function getMusicId3(filePath: string, fileName: string) {
  const { data } = await api.post('music_id3/', { file_path: filePath, file_name: fileName });
  return data;
}

export async function updateId3(musicId3Info: Array<Record<string, unknown>>) {
  const { data } = await api.post('update_id3/', { music_id3_info: musicId3Info });
  return data;
}

export async function fetchId3ByTitle(
  title: string,
  resource: string,
  fullPath?: string,
  limit?: number,
) {
  const { data } = await api.post('fetch_id3_by_title/', {
    title,
    resource,
    full_path: fullPath || '',
    // Omitted when the caller has no opinion: the gateway then applies its
    // own default, which is lower than the detail dialog's page of
    // candidates. Sending 0 would ask for zero rows.
    ...(limit && limit > 0 ? { limit } : {}),
  });
  return data;
}

export async function fetchLyric(songId: string, resource: string) {
  const { data } = await api.post('fetch_lyric/', { song_id: songId, resource });
  return data;
}

export async function batchUpdateId3(params: Record<string, unknown>) {
  const { data } = await api.post('batch_update_id3/', params);
  return data;
}

export async function tidyFolder(params: Record<string, unknown>) {
  const { data } = await api.post('tidy_folder/', params);
  return data;
}

export async function uploadImage(file: File) {
  const formData = new FormData();
  formData.append('upload_file', file);
  const { data } = await api.post('upload_image/', formData, {
    headers: { 'Content-Type': 'multipart/form-data' },
  });
  return data;
}

// Library-level operations surfaced in the toolbar.
//
// These are POST (not GET) as of REVIEW.md P1-1: they enqueue asynq tasks,
// and the gateway accepts a JWT from the `AUTHORIZATION` cookie as a
// fallback to the Authorization header (middleware/auth.go). A state-changing
// GET reachable with cookie auth is CSRF-able via a plain link / <img>, since
// SameSite=Lax still sends the cookie on top-level GET navigations. POST with
// SameSite=Lax is not sent cross-site, which closes that path.
export async function fullScanFolder() {
  const { data } = await api.post('full_scan_folder/');
  return data;
}

/** GET /api/active_queue/ — read-only asynq queue snapshot (servers,
 *  queues, pending rows). Read-only, so it stays a GET.
 *
 *  Routed through `api` (not bare axios) so the request interceptor
 *  attaches `Authorization: JWT <token>`. TaskCenterDropdown previously
 *  called `axios.get('/api/active_queue/')` directly, which sent no
 *  Authorization header and authenticated purely on the cookie mirror. */
/** POST /api/prune_empty_folders/ — enqueue a task that deletes directories
 *  left empty by a tidy, a rename or a delete, and drops index rows whose
 *  files are no longer on disk.
 *
 *  Runs as an asynq task rather than inline: it is filesystem mutation over
 *  an unbounded tree, like every other mutation here. Only literally empty
 *  directories go — a directory still holding a cover.jpg or a .lrc is left
 *  alone, because tidying can strand those and they are not disposable.
 *
 *  The row cleanup is not a set difference against what the scan saw — that
 *  is the operation the scanner refuses to do, since a failed sub-scan would
 *  take the whole unseen library with it. Each row is judged by asking the
 *  kernel whether its file exists, and only a definite "no such file" counts;
 *  a permission or I/O error keeps the row. Folder rows are kept, because
 *  their children's parent_id points at them.
 *
 *  Both lists land in 操作审计 as `prune_empty_folders`, under `removed` and
 *  `vanished_rows`.
 * * `sub_paths` restricts the sweep; omit it for the whole library.
 *
 * A directory left holding nothing but album-scoped residue — album.nfo, a
 * cue sheet, a cover — counts as empty: the audio it described is gone, and
 * the residue is what stops the directory from ever being removed. Those files
 * are moved into the delete trash, so this stays recoverable; the directory
 * itself still has to be accepted by os.Remove. */
export async function pruneEmptyFolders(subPaths?: Array<[string, string]>) {
  const { data } = await api.post('prune_empty_folders/', { sub_paths: subPaths ?? [] });
  return data;
}

/** POST /api/prune_empty_folders/preview/ — what the cleanup would remove,
 *  without removing it.
 *
 *  Read-only, so the gateway answers inline instead of going through the
 *  worker queue. The confirmation dialog lists these paths: a count the
 *  user can only nod at is not a confirmation, and a count that is wrong
 *  is worse than no dialog at all.
 *
 *  `sidecars` is the third list and it is not a footnote: a directory
 *  holding nothing but an `album.nfo` is only removable once that file is
 *  taken, and the file goes to the trash rather than being unlinked. The
 *  dialog has to name it, or the user approves a directory and loses a
 *  file they were never shown. */
export async function previewPruneEmpty(subPaths?: Array<[string, string]>) {
  const { data } = await api.post('prune_empty_folders/preview/', {
    sub_paths: subPaths ?? [],
  });
  const payload = unwrapEnvelope<{
    empty_dirs?: string[];
    sidecars?: string[];
    vanished_rows?: string[];
    total?: number;
  }>(data, 'prune_empty_folders/preview');
  return {
    emptyDirs: payload.empty_dirs ?? [],
    sidecars: payload.sidecars ?? [],
    vanishedRows: payload.vanished_rows ?? [],
    total: payload.total ?? 0,
  };
}

export async function getActiveQueue() {
  const { data } = await api.get('active_queue/');
  return data;
}

/** POST /api/check_duplicate/ — read-only duplicate scan over an explicit
 *  list of Worklist rows.
 *
 *  The dedup funnel used to run only on the write path, which made it a
 *  guard rail rather than a feature: a library that already contained
 *  duplicates never said so, and the only way to find out was to attempt a
 *  write. This runs the same four stages with no side effect, so a verdict
 *  here and a verdict on the write path cannot disagree — one checker, one
 *  index, one rule (handler/duplicate.go::CheckDuplicate).
 *
 *  `fileFullPaths` are relative to MUSIC_DIR — the same string a row's
 *  `fullPath` already holds.
 *
 *  Throws via unwrapEnvelope on a malformed / failed envelope, but NOT for
 *  an individual file that could not be checked: those come back as rows
 *  with verdict `error` / `skipped` so one unreadable file cannot hide the
 *  verdict on the other forty. */
export async function checkDuplicate(fileFullPaths: string[]): Promise<DuplicateReport> {
  const { data } = await api.post('check_duplicate/', {
    file_full_paths: fileFullPaths,
  });
  return unwrapEnvelope<DuplicateReport>(data, 'check_duplicate');
}

export interface DuplicateReport {
  results: Array<{
    file_full_path: string;
    /** Typed as RowDuplicate['verdict'] rather than `string` so a caller
     *  cannot smuggle an unhandled verdict past the compiler into the
     *  store's filter logic. The store still validates at runtime — this is
     *  for our own code, not for defending against the server. */
    verdict: RowDuplicate['verdict'];
    match_field?: string;
    duplicate_path?: string;
    reason?: string;
    run?: string[];
  }>;
  summary: {
    duplicate: number;
    likely_duplicate: number;
    unique: number;
    skipped: number;
    error: number;
  };
}/** POST /api/delete_files/ — remove files from the library.
 *
 *  Backs two actions: 「删除重复文件」 (paths a duplicate check flagged) and
 *  「删除选中文件」 (whatever the user selected). Both go through the same
 *  recoverable trash; only the stated reason differs, which is why
 *  `requestedBy` is a parameter — the server used to hardcode
 *  "duplicate_cleanup" and would have kept claiming that for a manual delete.
 *
 *  The server MOVES each file to `DATA_DIR/.trash/<timestamp>/` rather than
 *  unlinking it, preserving its path relative to MUSIC_DIR — the original
 *  is one `mv` away, and because the trash lives outside MUSIC_DIR neither
 *  the scanner nor http.Dir(MUSIC_DIR) can still serve it. The caller does
 *  not need to know this, but the UI does: the confirm dialog says the
 *  files are recoverable rather than claiming a hard delete.
 *
 * Returns per-row outcomes. A row that was already missing is `missing`,
 * not a request failure — a cleanup pass routinely races a manual delete. */
export async function deleteFiles(
  fileFullPaths: string[],
  requestedBy = 'duplicate_cleanup',
): Promise<DeleteFilesReport> {
  const { data } = await api.post('delete_files/', {
    file_full_paths: fileFullPaths,
    requested_by: requestedBy,
  });
  return unwrapEnvelope<DeleteFilesReport>(data, 'delete_files');
}

export interface DeleteFilesReport {
  results: Array<{
    file_full_path: string;
    status: string;
    reason?: string;
    trash_path?: string;
  }>;
  deleted: number;
  failed: number;
}

/** GET /api/trash/ — what 删除选中 / 删除重复 removed, and where it came from.
 *
 *  The delete path moves files to `DATA_DIR/.trash/<timestamp>/` rather than
 *  unlinking them, and the confirm dialog promises they are recoverable. This
 *  endpoint is the other half of that promise: without it the trash is a
 *  dot-directory only reachable by `docker exec`, which makes "recoverable"
 *  true on disk and false in practice.
 *
 *  Batches come back newest first. There is no purge endpoint on purpose —
 *  whoever wants the disk space can remove the directory on the host, where
 *  the action is visible to them. */
export interface TrashFile {
  /** Path relative to MUSIC_DIR: where the file was, and where a restore
   *  puts it back. */
  rel_path: string;
  size: number;
  modified_at: string;
  is_audio: boolean;
  content_type: string;
}

export interface TrashBatch {
  id: string;
  deleted_at: string;
  files: TrashFile[];
  total_size: number;
}

export interface TrashListing {
  batches: TrashBatch[];
  total_files: number;
  truncated: boolean;
  total_size: number;
  root: string;
}

export async function listTrash(): Promise<TrashListing> {
  const { data } = await api.get('trash/');
  return unwrapEnvelope<TrashListing>(data, 'trash');
}

export interface RestoreReport {
  results: Array<{
    rel_path: string;
    status: string;
    reason?: string;
    restored_to?: string;
  }>;
  restored: number;
  failed: number;
}

/** POST /api/trash/restore/ — put files back where they came from.
 *
 *  Refuses rather than overwrites when the destination already exists, so
 *  `exists` in the result is a real state the UI has to show, not an error to
 *  swallow. */
export async function restoreFromTrash(
  batchId: string,
  relPaths: string[],
): Promise<RestoreReport> {
  const { data } = await api.post('trash/restore/', {
    batch_id: batchId,
    rel_paths: relPaths,
  });
  return unwrapEnvelope<RestoreReport>(data, 'trash_restore');
}

export interface PurgeReport {
  results: Array<{
    rel_path: string;
    status: string;
    reason?: string;
  }>;
  purged: number;
  failed: number;
}

/** POST /api/trash/purge/ — destroy files in the trash for good.
 *
 *  The only irreversible endpoint in the app, hence `confirm`. It is not a
 *  politeness flag: the handler refuses without it, so a client that wires
 *  the wrong button cannot destroy the only copy of a file. An empty
 *  `relPaths` purges the whole batch directory, which is the only way an
 *  emptied batch ever stops being listed. */
export async function purgeTrash(
  batchId: string,
  relPaths: string[],
): Promise<PurgeReport> {
  const { data } = await api.post('trash/purge/', {
    batch_id: batchId,
    rel_paths: relPaths,
    confirm: true,
  });
  return unwrapEnvelope<PurgeReport>(data, 'trash_purge');
}

/** POST /api/clear_async_tasks/ — deletes every pending asynq task.
 *  Destructive, hence POST (REVIEW.md P1-1). Same interceptor rationale
 *  as getActiveQueue above. */
export async function clearAsyncTasks() {
  const { data } = await api.post('clear_async_tasks/');
  return data;
}

export async function searchMusic(params: {
  query: string;
  sources: string[];
  pages?: Record<string, number>;
  limit?: number;
}) {
  const { data } = await api.post('search_music/', params);
  return data;
}

/** GET /api/sources/ — Stage A of docs/plugable-plugins.md. Reads the
 *  server's registered plugin map and returns each entry's capability
 *  flags. The frontend's useSourceStore hydrates from this once on mount. */
export async function getSources(): Promise<{
  result: boolean;
  data: SourceInfo[];
  code: string;
  message: string;
}> {
  const { data } = await api.get('sources/');
  return data;
}

/** POST /api/download/ — enqueue a "download source X's track Y into the
 *  library at download_path" task. Backend source-routes by the registry
 *  (only youtube is registered today; future soundcloud add only needs a
 *  new branch in tasks.DownloadHandler). Today the contract is:
 *
 *   - download_path is RELATIVE to MUSIC_DIR on the server (unsafe / .. /
 *     absolute are rejected server-side). Frontend builds it from
 *     artist/title + ext matching what the row shows; the user can
 *     rename post-download in the editor.
 *   - The backend short-circuits when the file is already in the
 *     per-source cache dir (left over from a /api/stream preview
 *     long-poll), so re-clicking 加入库 after a listen is a cp, not a
 *     re-fetch.
 *   - 200 envelope is the asynq enqueued-or-skipped signal. Surfaced to
 *     the user via the toast returned from the caller; no follow-up poll
 *     because the library dir scan refreshes via a separate toolbar button.
 *
 *  `extra_audio_format` defaults to 'ogg' (matches the stream proxy's
 *  ServeFile extension when yt-dlp lands as vorbis/ogg). */
export async function downloadToLibrary(params: {
  source: string;
  video_id: string;
  download_path?: string;
  extra_audio_format?: string;
}): Promise<{ result: boolean; code: string; data: unknown[]; message: string; skipped?: boolean; dest?: string }> {
  const { data } = await api.post('download/', {
    source: params.source,
    video_id: params.video_id,
    download_path: params.download_path ?? '',
    extra_audio_format: params.extra_audio_format ?? 'ogg',
  });
  return data;
}

// NOTE: `sources/refresh/` and `sources/override/` are intentionally
// absent. Both routes have answered 501 since REVIEW.md P0-2 retired
// runtime overrides — the plugins live in their own containers and the
// gRPC contract has no RPC able to carry an override across that
// boundary, so the old handlers could only ever have applied nothing.
// The routes stay registered (see handler/source.go) so a cached bundle
// gets a clear machine-readable answer rather than a 404 that reads like
// a deploy problem; the client just stops asking. Overrides belong in
// `data/sources/*.yaml` and take effect on plugin-container restart.

// ─── 从标签改名: preview → apply round-trip ─────────────────────────

/** One file's planned rename. Mirrors `handler.RenamePlanRow`.
 *
 *  `new_name` carries the extension — that is what lands on disk, and
 *  what the client writes back into the table. `missing` lists template
 *  fields that were empty for this file, which is why the name may have
 *  a gap in it. */
export interface RenamePlanRow {
  path: string;
  old_name: string;
  new_name: string;
  status: 'ok' | 'no_change' | 'taken' | 'blocked' | 'failed';
  missing?: string[];
  detail?: string;
}

/** Mirrors the two `Rename*` status constants. */
export const RENAME_STATUSES = {
  /** The file will move. The only bucket the apply button cares about. */
  ok: 'ok',
  /** The template reproduces the name the file already has. Not a
   *  failure — half a selection already being correct is normal. */
  noChange: 'no_change',
  /** Another file, in this batch or already on disk, wants this name.
   *  os.Rename replaces, so this is the case that loses data. */
  taken: 'taken',
  /** The template cannot be used here: unknown field, no placeholders,
   *  or it rendered to nothing. */
  blocked: 'blocked',
  /** The rename itself errored. */
  failed: 'failed',
} as const;

export interface RenameResponse {
  rows: RenamePlanRow[];
  tally: Record<string, number>;
  dry_run: boolean;
}

/** POST /api/tag/preview_rename_from_tags/ — plan the rename, write
 *  nothing. Returns each file's old and new name plus a status, so a
 *  500-file rename can be read before it is committed.
 *
 *  `template` is a per-file expansion, not a name: `paths` are relative
 *  to MUSIC_DIR (the server rejects `'..'` escapes via SafeJoin) and
 *  every one of them renders `template` against its own tags. */
export async function previewRenameFromTags(
  paths: string[],
  template: string,
): Promise<RenameResponse> {
  const { data } = await api.post('tag/preview_rename_from_tags/', {
    paths,
    template,
  });
  return unwrapEnvelope<RenameResponse>(data, 'preview_rename_from_tags');
}

/** POST /api/tag/apply_rename_from_tags/ — same body as the preview.
 *
 *  The server re-plans every row rather than trusting a client-supplied
 *  plan, so a file renamed between preview and click cannot be renamed
 *  twice, and an edited template cannot apply a stale plan. The
 *  response is therefore the authoritative result, not a receipt for
 *  what was previewed. */
export async function applyRenameFromTags(
  paths: string[],
  template: string,
): Promise<RenameResponse> {
  const { data } = await api.post('tag/apply_rename_from_tags/', {
    paths,
    template,
  });
  return unwrapEnvelope<RenameResponse>(data, 'apply_rename_from_tags');
}

// ─── 解析文件名: preview → apply round-trip ─────────────────────────

/** The tag fields the parser can fill, in the order the modal shows them.
 *
 *  Mirrors `internal/utils.PatternFieldNames`, which is the server's
 *  allow-list. A pattern naming anything else is rejected there, so this
 *  list and that one have to move together. */
export const PARSE_TAG_FIELDS = [
  'title',
  'artist',
  'album',
  'albumartist',
  'genre',
  'year',
  'tracknumber',
  'discnumber',
] as const;

export type ParseTagField = (typeof PARSE_TAG_FIELDS)[number];

/** Chinese labels for the fields, for the modal's column headers. */
export const PARSE_TAG_LABELS: Record<ParseTagField, string> = {
  title: '标题',
  artist: '艺术家',
  album: '专辑',
  albumartist: '专辑艺术家',
  genre: '流派',
  year: '年份',
  tracknumber: '音轨',
  discnumber: '碟片',
};

/** Mirrors `internal/utils.ParseOptions`.
 *
 *  `paths` are RELATIVE to MUSIC_DIR on the server (server-side rejects
 *  `'..'` escapes via SafeJoin).
 *
 *  `pattern` names its capture groups with Go's `(?P<field>...)` syntax
 *  and each name must be one of PARSE_TAG_FIELDS; anything else is a 400
 *  that names the offender. Without a pattern the server splits on the
 *  separator and reads positionally: first part artist, rest title. */
export interface ParseOptions {
  separator?: string;
  pattern?: string;
}

/** Mirrors `internal/cache.ParsedResult`. `path` is the ABSOLUTE
 *  server-side path returned by preview (used as the key for overrides
 *  in apply). The frontend never constructs paths itself. */
export interface ParsedPreviewRow extends Partial<Record<ParseTagField, string>> {
  path: string;
  status: 'ok' | 'ambiguous' | 'unparsable';
}

/** One override entry keyed by path (returned by preview). An empty
 *  field falls back to the parsed value; a non-empty override REPLACES
 *  (not merges) it. There is deliberately no way to CLEAR a tag from
 *  here — an override that says nothing is not a request to delete
 *  something, and the batch editor owns that. */
export type ParseApplyOverride = { path: string } & Partial<
  Record<ParseTagField, string>
>;

/** POST /api/tag/preview_parse_filenames/ — runs the filename parser
 *  server-side and returns a one-shot preview token. Frontend stores
 *  the token + results in modal state and submits the apply call with
 *  any per-row overrides the user typed in the modal table.
 *  Token TTL is 10 min server-side; expired / unknown tokens surface
 *  as 401 "preview_expired" — the modal catches this and re-prompts
 *  via a fresh preview call. */
export async function previewParseFilenames(
  paths: string[],
  options?: ParseOptions,
): Promise<{ token: string; results: ParsedPreviewRow[] }> {
  const { data } = await api.post('tag/preview_parse_filenames/', {
    paths,
    options: options ?? {},
  });
  // Must unwrap: the handler answers through SuccessData, so the token
  // and rows live under `data.data`, not at the top level. Returning the
  // envelope here — as this did — left `results` undefined on EVERY
  // response, and the caller iterated it during render.
  const payload = unwrapEnvelope<{ token?: unknown; results?: unknown }>(
    data,
    'preview_parse_filenames',
  );
  // A missing token is a protocol violation, not an empty preview. Throw
  // rather than defaulting to '' — the modal's effect guards on `token`
  // being truthy, so an empty string would re-fire the preview forever.
  if (typeof payload?.token !== 'string' || payload.token === '') {
    throw new Error('preview_parse_filenames: 响应缺少 token');
  }
  return {
    token: payload.token,
    results: asArray<ParsedPreviewRow>(payload.results),
  };
}

/** POST /api/tag/apply_parsed_filenames/ — consumes the token, applies
 *  per-row overrides, enqueues `TypeApplyParsedFilenames` worker, and
 *  returns the asynq task_id. The worker writes every field the row
 *  carries — no rename, no cover, no sidecar — and a field the row does
 *  not carry is left alone, so this can fill in what a file is missing
 *  but never delete a tag. Override any field of an "unparsable" row;
 *  the backend flips status to "ok" (the typed-in value is
 *  authoritative even when the name could not be read). */
export async function applyParsedFilenames(
  token: string,
  overrides: ParseApplyOverride[] = [],
): Promise<{
  task_id: string;
  type: string;
  queue: string;
  state: string;
  row_count: number;
  apply_target: string;
}> {
  const { data } = await api.post('tag/apply_parsed_filenames/', {
    token,
    overrides,
  });
  // Same unwrap as preview — this one was never reached, because the
  // preview call above crashed first, but `res.task_id.slice(0, 8)` in
  // the modal would have thrown on the next undefined.
  return unwrapEnvelope(data, 'apply_parsed_filenames');
}

export interface OperationLogItem {
  id: number;
  action: string;
  target: string;
  operator: string;
  status: string;
  item_count: number;
  details: string;
  error_msg?: string;
  created_at: string;
}

export interface OperationLogsResponse {
  results: OperationLogItem[];
  count: number;
  page: number;
  page_size: number;
}

export async function getOperationLogs(params?: {
  page?: number;
  page_size?: number;
  action?: string;
  status?: string;
  search?: string;
}): Promise<{
  result: boolean;
  code: string;
  data: OperationLogsResponse;
  message: string;
}> {
  const { data } = await api.get('operation_logs/', { params });
  return data;
}

export async function clearOperationLogs(days?: number): Promise<{
  result: boolean;
  code: string;
  data: { cleared: number };
  message: string;
}> {
  const { data } = await api.post('operation_logs/clear/', { days });
  return data;
}

