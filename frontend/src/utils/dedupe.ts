// The user's duplicate-detection preference, and how to say it on the wire.
//
// The backend decides what counts as a duplicate (internal/dedup): a name
// clash or a 70%-similar track only warns, and only byte-identical or
// acoustically-identical audio refuses the write. What lives here is the one
// decision that is the user's — whether to let any of that happen at all.
//
// Default is ON, matching the server. It is worth being explicit that this
// is a reversal: the server used to require an opt-in `check_duplicate`, and
// nothing in the app ever sent one, so the whole funnel was dead. Now that
// only content-level evidence can block a save, leaving it on by default is
// the safe side of the trade — and a user who wants both copies tagged can
// turn it off here rather than discovering the problem after a rejected save.
//
// The flag goes INSIDE `music_info`, not beside it. The request structs bind
// `music_info` as the tag map and have no top-level field for it, so a
// sibling key is dropped by JSON binding without an error — which is exactly
// the bug this module's shape is written to prevent.

import { readBool, writeBool } from '@/utils/persist';

const DEDUPE_KEY = 'settings.checkDuplicate';

/** Whether duplicate detection is on. Defaults to true when unset. */
export function isDedupeEnabled(): boolean {
  return readBool(DEDUPE_KEY) ?? true;
}

export function setDedupeEnabled(enabled: boolean): void {
  writeBool(DEDUPE_KEY, enabled);
}

/**
 * The tag-map fragment that expresses the preference.
 *
 * Returns `{}` when enabled, because the server's default is already on and
 * sending `check_duplicate: true` would be noise. Returns the explicit
 * opt-out when disabled, which is the only way to turn it off.
 *
 * Meant to be spread into the per-row `music_info` of a batch, or into a
 * single-track payload. For a batch whose rows each carry their own
 * `music_info`, put it on the shared map instead: the server copies control
 * flags down into overriding rows.
 */
export function dedupeFlag(enabled: boolean): Record<string, never> | { check_duplicate: false } {
  return enabled ? {} : { check_duplicate: false };
}
