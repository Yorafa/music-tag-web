/**
 * Vitest global setup.
 *
 * Why this file exists
 * --------------------
 * The jsdom environment DOES provide a working `localStorage` — but as of
 * Node 22 (and definitively on Node 26, which this repo currently runs)
 * Node ships its own *experimental* `localStorage` global. Unless the
 * process is started with `--localstorage-file`, that global resolves to
 * `undefined` and, because vitest's jsdom environment assigns the jsdom
 * window's keys onto `globalThis`, the broken Node accessor wins and
 * shadows jsdom's implementation.
 *
 * Symptom before this file: every store spec that calls `localStorage.clear()`
 * in `beforeEach` dies with
 *     TypeError: Cannot read properties of undefined (reading 'clear')
 * and the whole `useWorklistStore` suite goes red even
 * though nothing in `src/` is broken.
 *
 * Why a polyfill instead of `--localstorage-file`
 * ----------------------------------------------
 * The Node flag is not a fix, it is a different failure: it backs
 * localStorage with a real file shared by every worker, so state written
 * by one test *file* leaks into another and the rehydrate-on-init specs
 * fail only when the full suite runs. A fresh in-memory store per test
 * file keeps isolation deterministic on every Node version.
 *
 * So: keep jsdom's storage whenever it is actually usable, and only stand
 * in when it is missing or broken.
 */

/** Minimal subset of the Web Storage API the stores actually touch. */
class MemoryStorage implements Storage {
  private map = new Map<string, string>()

  get length(): number {
    return this.map.size
  }

  key(index: number): string | null {
    // DOMStringList-ish ordering: insertion order, out-of-range => null.
    return [...this.map.keys()][index] ?? null
  }

  getItem(key: string): string | null {
    // Web Storage stringifies keys; normalise so `getItem(1)` and
    // `getItem("1")` hit the same slot, exactly like a real browser.
    return this.map.get(String(key)) ?? null
  }

  setItem(key: string, value: string): void {
    this.map.set(String(key), String(value))
  }

  removeItem(key: string): void {
    this.map.delete(String(key))
  }

  clear(): void {
    this.map.clear()
  }
}

/**
 * A localStorage is "usable" when it is an object and actually retains
 * what we write. Node's experimental stub passes `typeof === "object"`
 * while returning `undefined`, so the write/read round-trip is the only
 * reliable probe.
 */
function isUsableStorage(candidate: unknown): candidate is Storage {
  if (typeof candidate !== 'object' || candidate === null) return false
  const store = candidate as Storage
  try {
    const probe = '__vitest_localstorage_probe__'
    store.setItem(probe, '1')
    const ok = store.getItem(probe) === '1'
    store.removeItem(probe)
    return ok
  } catch {
    return false
  }
}

if (!isUsableStorage(globalThis.localStorage)) {
  const memory = new MemoryStorage()
  // `configurable: true` matters: individual specs call `vi.resetModules()`
  // and re-import the store module, and some of them reassign the global
  // themselves. Redefining has to keep working across those.
  Object.defineProperty(globalThis, 'localStorage', {
    value: memory,
    writable: true,
    configurable: true,
    enumerable: true,
  })
}
