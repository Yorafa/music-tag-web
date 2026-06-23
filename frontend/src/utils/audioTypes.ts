export const ALLOWED_TYPES = [
  'flac',
  'mp3',
  'ape',
  'wav',
  'aiff',
  'wv',
  'tta',
  'm4a',
  'ogg',
  'mpc',
  'opus',
  'wma',
  'dsf',
  'dff',
] as const;

/** True when the file's extension is an audio format we recognize. */
export function isAudioFile(name: string): boolean {
  const ext = name.split('.').pop()?.toLowerCase() ?? '';
  return (ALLOWED_TYPES as readonly string[]).includes(ext);
}
