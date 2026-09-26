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

export async function fetchId3ByTitle(title: string, resource: string, fullPath?: string) {
  const { data } = await api.post('fetch_id3_by_title/', {
    title,
    resource,
    full_path: fullPath || '',
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
 *
 *  `sub_paths` restricts the sweep; omit it for the whole library. */
export async function pruneEmptyFolders(subPaths?: Array<[string, string]>) {
  const { data } = await api.post('prune_empty_folders/', { sub_paths: subPaths ?? [] });
  return data;
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
}

/** POST /api/delete_files/ — remove files from the library.
 *
 *  Backs the 「删除重复文件」 action, so it only ever receives paths a
 *  duplicate check already flagged.
 *
 *  The server MOVES each file to `DATA_DIR/.trash/<timestamp>/` rather than
 *  unlinking it, preserving its path relative to MUSIC_DIR — the original
 *  is one `mv` away, and because the trash lives outside MUSIC_DIR neither
 *  the scanner nor http.Dir(MUSIC_DIR) can still serve it. The caller does
 *  not need to know this, but the UI does: the confirm dialog says the
 *  files are recoverable rather than claiming a hard delete.
 *
 *  Returns per-row outcomes. A row that was already missing is `missing`,
 *  not a request failure — a cleanup pass routinely races a manual delete. */
export async function deleteFiles(fileFullPaths: string[]): Promise<DeleteFilesReport> {
  const { data } = await api.post('delete_files/', {
    file_full_paths: fileFullPaths,
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

/** POST /api/clear_celery/ — deletes every pending asynq task.
 *  Destructive, hence POST (REVIEW.md P1-1). Same interceptor rationale
 *  as getActiveQueue above. */
export async function clearAsyncTasks() {
  const { data } = await api.post('clear_celery/');
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

// ─── C.2 Filename Parse: preview → apply round-trip ────────────────

/** Mirrors `internal/utils.ParseOptions` + `status: 'ok'|'ambiguous'|'unparsable'`.
 *  `paths` are RELATIVE to MUSIC_DIR on the server (server-side rejects
 *  `'..'` escapes via SafeJoin). */
export interface ParseOptions {
  separator?: string;
  fallbackRegex?: string;
}

/** Mirrors `internal/cache.ParsedResult`. `path` is the ABSOLUTE
 *  server-side path returned by preview (used as the key for overrides
 *  in apply). The frontend never constructs paths itself. */
export interface ParsedPreviewRow {
  path: string;
  artist?: string;
  title?: string;
  status: 'ok' | 'ambiguous' | 'unparsable';
}

/** One override entry keyed by path (returned by preview). Empty
 *  artist/title fields fall back to the parsed values; a non-empty
 *  override REPLACES (not merges) the parsed value. */
export interface ParseApplyOverride {
  path: string;
  artist?: string;
  title?: string;
}

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
 *  returns the asynq task_id. The worker writes Artist + Title to
 *  each row's tag (no rename, no cover, no sidecar — C.2 stays scoped.
 *  Override an "unparsable" row's artist OR title; the backend will
 *  flip status to "ok" (the user's manual override is treated as
 *  authoritative even when the filename couldn't be parsed). */
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

