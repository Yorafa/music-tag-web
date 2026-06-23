import axios from 'axios';
import { useAuthStore } from '@/store/useAuthStore';

const api = axios.create({
  baseURL: '/api/',
  headers: {
    'Content-Type': 'application/json',
  },
});

// Request interceptor: attach JWT + CSRF tokens from cookies
api.interceptors.request.use((config) => {
  const match = document.cookie.match(/(?:^|;\s*)AUTHORIZATION=([^;]*)/);
  const token = match ? decodeURIComponent(match[1]) : null;
  if (token) {
    config.headers.Authorization = token;
  }

  const csrfMatch = document.cookie.match(/(?:^|;\s*)music_site_csrftoken=([^;]*)/);
  const csrf = csrfMatch ? decodeURIComponent(csrfMatch[1]) : null;
  if (csrf) {
    config.headers['X-CSRFToken'] = csrf;
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

export async function clearCelery() {
  const { data } = await api.get('clear_celery/');
  return data;
}
