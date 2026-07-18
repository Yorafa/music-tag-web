import axios from 'axios';
import { useAuthStore } from '@/store/useAuthStore';
import type { SourceInfo } from '@/types';

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

export async function batchAutoUpdateId3(params: Record<string, unknown>) {
  const { data } = await api.post('batch_auto_update_id3/', params);
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
export async function scanFolder() {
  const { data } = await api.get('task1/');
  return data;
}

export async function fullScanFolder() {
  const { data } = await api.get('full_scan_folder/');
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
 *  `extra_audio_format` defaults to 'mp3' (matches the stream proxy's
 *  ServeFile extension when yt-dlp lands). */
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
    extra_audio_format: params.extra_audio_format ?? 'mp3',
  });
  return data;
}
