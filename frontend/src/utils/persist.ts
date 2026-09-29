// Thin localStorage wrapper.
//
// WHY THESE RETURN A RESULT INSTEAD OF SWALLOWING: every write here used to
// `catch {}` a QuotaExceededError, which made "the worklist didn't save"
// indistinguishable from "the worklist didn't save because the user never
// added anything". A ~24000-row worklist crosses the 5 MB origin quota
// (measured: 4000 rows → 0.89 MB, linear), and past that the user got a
// full-looking table that silently vanished on reload. writeString /
// writeJson now report success as a boolean and set a `lastPersistError`
// the caller can surface. readString / readJson stay null-on-failure: a
// missing or corrupt value is a legitimate "nothing stored" answer, not an
// error the caller can do anything about.

const isBrowser = (): boolean => typeof window !== 'undefined';

/** Why the most recent write failed, for a caller that wants to say so.
 *  `null` after a successful write. Not persisted — it describes the last
 *  operation, and a caller that wants to report it should do so immediately
 *  rather than poll. */
let lastWriteError: string | null = null;

/** The reason the most recent writeString / writeJson failed, or null.
 *
 *  Distinct names are kept apart because they mean different things to a
 *  user: a quota error means the data was too big to save (actionable —
 *  clear space, add less), while "unavailable" means private browsing /
 *  disabled storage, where nothing will ever save and a quota-shaped
 *  message would be a lie. */
export function lastPersistError(): string | null {
  return lastWriteError;
}

function describeWriteError(err: unknown): string {
  const name = (err as { name?: string } | null)?.name;
  // 22 = QuotaExceededError, 1014 = Firefox's NS_ERROR_DOM_QUOTA_REACHED.
  // Browsers disagree on which they throw for the same condition.
  if (name === 'QuotaExceededError' || name === 'NS_ERROR_DOM_QUOTA_REACHED') {
    return 'quota';
  }
  return 'unavailable';
}

export function readString(key: string): string | null {
  if (!isBrowser()) return null;
  try {
    return window.localStorage.getItem(key);
  } catch {
    return null;
  }
}

/** Write `value` under `key`. Returns false when storage refused it —
 *  quota exceeded, private browsing, storage disabled. A caller that
 *  ignores the result gets the old silent behaviour; one that checks it
 *  can tell the user their work did not survive a reload. */
export function writeString(key: string, value: string): boolean {
  if (!isBrowser()) return false;
  try {
    window.localStorage.setItem(key, value);
    lastWriteError = null;
    return true;
  } catch (err) {
    lastWriteError = describeWriteError(err);
    return false;
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

export function writeJson<T>(key: string, value: T): boolean {
  // JSON.stringify first: a value that can't be serialized throws a
  // TypeError, which must be reported as a failure rather than escaping
  // through writeString's quota catch and being misreported.
  let encoded: string;
  try {
    encoded = JSON.stringify(value);
  } catch {
    lastWriteError = 'unavailable';
    return false;
  }
  return writeString(key, encoded);
}

export function readNumber(key: string): number | null {
  const raw = readString(key);
  if (raw === null) return null;
  const n = Number(raw);
  return Number.isFinite(n) ? n : null;
}

export function writeNumber(key: string, value: number): boolean {
  return writeString(key, String(value));
}

export function readBool(key: string): boolean | null {
  const raw = readString(key);
  if (raw === null) return null;
  if (raw === 'true') return true;
  if (raw === 'false') return false;
  return null;
}

export function writeBool(key: string, value: boolean): boolean {
  return writeString(key, value ? 'true' : 'false');
}
