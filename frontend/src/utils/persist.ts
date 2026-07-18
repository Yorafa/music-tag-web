const isBrowser = (): boolean => typeof window !== 'undefined';

export function readString(key: string): string | null {
  if (!isBrowser()) return null;
  try {
    return window.localStorage.getItem(key);
  } catch {
    return null;
  }
}

export function writeString(key: string, value: string): void {
  if (!isBrowser()) return;
  try {
    window.localStorage.setItem(key, value);
  } catch {
    /* quota exceeded or storage unavailable — ignore */
  }
}

/** Remove a key entirely (vs. writeString(key, '') which leaves a tombstone).
 *  Pair with readString's `null`-on-absent return so logout/init round-trip
 *  is symmetric: empty key ⟶ `null`, set ⟶ value, remove ⟶ `null`. */
export function removeString(key: string): void {
  if (!isBrowser()) return;
  try {
    window.localStorage.removeItem(key);
  } catch {
    /* storage unavailable — ignore */
  }
}

export function readJson<T>(key: string): T | null {
  const raw = readString(key);
  if (raw === null) return null;
  try {
    return JSON.parse(raw) as T;
  } catch {
    return null;
  }
}

export function writeJson<T>(key: string, value: T): void {
  writeString(key, JSON.stringify(value));
}

export function readNumber(key: string): number | null {
  const raw = readString(key);
  if (raw === null) return null;
  const n = Number(raw);
  return Number.isFinite(n) ? n : null;
}

export function writeNumber(key: string, value: number): void {
  writeString(key, String(value));
}

export function readBool(key: string): boolean | null {
  const raw = readString(key);
  if (raw === null) return null;
  if (raw === 'true') return true;
  if (raw === 'false') return false;
  return null;
}

export function writeBool(key: string, value: boolean): void {
  writeString(key, value ? 'true' : 'false');
}
