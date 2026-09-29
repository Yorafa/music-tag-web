// Remembering 云端检索 across visits.
//
// The view used to open blank every time: `selectedSources` was a hardcoded
// ['netease', 'qmusic'], the single/multi toggle and the single-source pick
// reset, the query box was empty and the result list was gone. Re-picking
// sources on every visit is a small tax that adds up — the choice is
// deliberate and made once, and the rest of the toolbar already remembers
// the equivalent (see tagSources.ts for the scrape side).
//
// So the whole search state is persisted: the query, the source selection,
// which mode it was in, AND the results. Restoring the results is what
// makes it worth doing — a five-source fan-out is a real network cost, and
// re-issuing it just to look at the same list again is the waste. What comes
// back is visibly marked as the previous search, and the 搜索音源 button is
// the way to actually re-run it.
//
// # Everything read back is untrusted
//
// It is parsed localStorage. Another version of this app, a half-finished
// write, or a hand-edited value can leave anything there, and the failure
// mode of a bad source list is silent: a search against a source that no
// longer exists returns nothing, and the user reads that as "no matches".
// Every field is therefore validated and narrowed here rather than trusted,
// and an unusable stored value falls back to the defaults — never to an
// empty selection, which would mean "search nothing".
//
// # Two writes, not one
//
// Settings and results are saved separately, each a read-modify-write that
// leaves the other half alone. One combined write cannot work: the view
// saves settings on every chip click and results only when a search
// returns, so a single writer would have to carry the other half along —
// and "carry it along" is indistinguishable from "clear it", which meant
// the settings write on mount would erase the results it had just
// restored.
//
// # Why the write is not writeJson
//
// Because this needs to know whether the write SUCCEEDED. localStorage is
// ~5 MB shared with the worklist and the source prefs, and a result set
// carrying embedded cover art can be large. A failed write here would mean
// losing the source selection — the part the user actually asked for —
// along with the results. So the results are the thing that gets dropped,
// not the settings.
//
// (It still rolls its own `tryWrite` rather than using persist.ts's
// writeString even though that now returns a boolean: the fallback has to
// re-write the SAME key with different content, which writeString cannot
// express, and a half-applied snapshot is exactly what this module exists
// to avoid.)

import type { SearchResult } from '@/types';

const STORAGE_KEY = 'cloudSearch.v1';

/** The half that describes HOW to search. Saved on every control change. */
export interface CloudSearchSettings {
  query: string;
  multiSource: boolean;
  singleSource: string;
  selectedSources: string[];
}

/** What a visit to 云端检索 restores. */
export interface CloudSearchSnapshot extends CloudSearchSettings {
  results: SearchResult[];
}

/** What a first visit looks like. Two sources, multi, no results. */
export const CLOUD_SEARCH_DEFAULTS: CloudSearchSnapshot = {
  query: '',
  multiSource: true,
  singleSource: 'netease',
  selectedSources: ['netease', 'qmusic'],
  results: [],
};

/** How many results to keep. The view asks for 25, so this is the whole
 *  page; capping lower would restore a list that disagrees with the one the
 *  user last saw, which is the thing this is meant to avoid. */
const MAX_RESULTS = 25;

/** A longer query is not a query. Also the only bound that matters for the
 *  size of the stored string. */
const MAX_QUERY = 200;

/** Cover fields, by the same rule persistMusicInfo.ts uses for the worklist:
 *  a remote http(s) URL is a short string worth keeping, an embedded
 *  data-URI is not. Dropping it costs a gradient placeholder on a restored
 *  row and saves the quota that keeps the source selection alive. */
const COVER_KEYS = ['cover', 'album_img'] as const;
const MAX_COVER_LEN = 2048;

function isString(v: unknown): v is string {
  return typeof v === 'string';
}

function cleanSources(saved: unknown): string[] {
  if (!Array.isArray(saved)) return [];
  const out: string[] = [];
  for (const s of saved) {
    if (isString(s) && s !== '' && !out.includes(s)) out.push(s);
  }
  return out;
}

function cleanCover(v: unknown): unknown {
  if (typeof v !== 'string') return v;
  if (v.startsWith('data:')) return undefined;
  return v.length > MAX_COVER_LEN ? undefined : v;
}

/** One row, or null if it is not shaped like a search result.
 *
 *  `name` and `artist` are required because the card renders them directly
 *  and a row missing them is a blank card that looks like a bug in the
 *  view. `id` + `source` are required because they are the React key and
 *  the download address — a row without them cannot be acted on. */
function cleanResult(saved: unknown): SearchResult | null {
  if (saved === null || typeof saved !== 'object') return null;
  const r = saved as Record<string, unknown>;
  if (!isString(r.id) || r.id === '') return null;
  if (!isString(r.source) || r.source === '') return null;
  if (!isString(r.name) || !isString(r.artist)) return null;

  // Built as a plain record and narrowed once at the end: assigning field
  // by field onto a typed SearchResult would need a cast per field, and a
  // cast per field is a place for one of them to be wrong.
  const out: Record<string, unknown> = {
    id: r.id,
    source: r.source,
    name: r.name,
    artist: r.artist,
  };
  for (const k of ['title', 'album', 'album_id', 'genre', 'mid'] as const) {
    if (isString(r[k])) out[k] = r[k];
  }
  for (const k of COVER_KEYS) {
    const v = cleanCover(r[k]);
    if (v !== undefined) out[k] = v;
  }
  if (isString(r.duration) || typeof r.duration === 'number') out.duration = r.duration;
  if (isString(r.url)) out.url = r.url;
  return out as unknown as SearchResult;
}

function cleanResults(saved: unknown): SearchResult[] {
  if (!Array.isArray(saved)) return [];
  const out: SearchResult[] = [];
  for (const r of saved) {
    if (out.length >= MAX_RESULTS) break;
    const clean = cleanResult(r);
    if (clean) out.push(clean);
  }
  return out;
}

/** The stored snapshot, with every field validated, or the defaults.
 *
 *  A corrupt or absent entry is not an error worth reporting: the view
 *  simply opens as it did before this existed. */
export function loadCloudSearchSnapshot(): CloudSearchSnapshot {
  let saved: unknown;
  try {
    const raw = typeof window === 'undefined' ? null : localStorage.getItem(STORAGE_KEY);
    if (raw === null) return { ...CLOUD_SEARCH_DEFAULTS, results: [] };
    saved = JSON.parse(raw);
  } catch {
    return { ...CLOUD_SEARCH_DEFAULTS, results: [] };
  }
  if (saved === null || typeof saved !== 'object') {
    return { ...CLOUD_SEARCH_DEFAULTS, results: [] };
  }
  const s = saved as Record<string, unknown>;
  const selected = cleanSources(s.selectedSources);
  return {
    query: isString(s.query) ? s.query.slice(0, MAX_QUERY) : CLOUD_SEARCH_DEFAULTS.query,
    multiSource: typeof s.multiSource === 'boolean' ? s.multiSource : CLOUD_SEARCH_DEFAULTS.multiSource,
    singleSource: isString(s.singleSource) ? s.singleSource : CLOUD_SEARCH_DEFAULTS.singleSource,
    // An empty stored selection is a bug in whatever wrote it, not a user
    // choice — a search with no sources finds nothing and looks broken.
    selectedSources: selected.length > 0 ? selected : [...CLOUD_SEARCH_DEFAULTS.selectedSources],
    results: cleanResults(s.results),
  };
}

/** Whether a write landed. Distinguishes "stored" from "silently dropped". */
function tryWrite(key: string, value: string): boolean {
  if (typeof window === 'undefined') return false;
  try {
    localStorage.setItem(key, value);
    return true;
  } catch {
    return false;
  }
}

/** Write the snapshot, results first and settings whatever happens.
 *
 *  The order is the whole point. The source selection is what the user
 *  asked to stop re-doing, and the results are a convenience; when the
 *  quota is tight the convenience goes, not the setting. A store that
 *  dropped the settings instead would leave them re-typing sources with no
 *  way to tell that is why. */
function writeSnapshot(snapshot: CloudSearchSnapshot): void {
  if (tryWrite(STORAGE_KEY, JSON.stringify(snapshot))) return;
  tryWrite(STORAGE_KEY, JSON.stringify({ ...snapshot, results: [] }));
}

/** Save the search settings, leaving the stored results alone. */
export function saveCloudSearchSettings(settings: CloudSearchSettings): void {
  const current = loadCloudSearchSnapshot();
  writeSnapshot({
    query: settings.query,
    multiSource: settings.multiSource,
    singleSource: settings.singleSource,
    selectedSources: settings.selectedSources,
    // NOT `current.results`: re-serialising them on every chip click is
    // the write this split exists to avoid, and writing an empty list here
    // would throw away the results the user came back to see.
    results: current.results,
  });
}

/** Save a fresh result set, leaving the stored settings alone. */
export function saveCloudSearchResults(results: SearchResult[]): void {
  const current = loadCloudSearchSnapshot();
  writeSnapshot({
    query: current.query,
    multiSource: current.multiSource,
    singleSource: current.singleSource,
    selectedSources: current.selectedSources,
    results: cleanResults(results),
  });
}

/** Narrow a restored snapshot to the sources this build actually offers.
 *
 *  Run once the backend's source list arrives, which is after the first
 *  render — the loader cannot filter on a list it does not have yet. A
 *  plugin that was renamed or removed leaves its name in the stored
 *  selection, and searching a source the gateway no longer has returns
 *  nothing at all: the results panel would say "没有匹配的云端歌曲" and the
 *  user would conclude the song does not exist.
 *
 *  If the whole remembered selection is gone, fall back through the
 *  defaults and finally to the first offered source. Never to nothing. */
export function reconcileSnapshot(
  snapshot: CloudSearchSnapshot,
  offered: string[],
): CloudSearchSnapshot {
  if (offered.length === 0) return snapshot;
  const live = new Set(offered);

  const kept = snapshot.selectedSources.filter((s) => live.has(s));
  let selectedSources = kept;
  if (kept.length === 0) {
    selectedSources = CLOUD_SEARCH_DEFAULTS.selectedSources.filter((s) => live.has(s));
  }
  if (selectedSources.length === 0) selectedSources = [offered[0]];

  const singleSource = live.has(snapshot.singleSource)
    ? snapshot.singleSource
    : selectedSources[0];

  return { ...snapshot, selectedSources, singleSource };
}

/** The key, exported for tests and for anything that needs to wipe it. */
export const CLOUD_SEARCH_STORAGE_KEY = STORAGE_KEY;
