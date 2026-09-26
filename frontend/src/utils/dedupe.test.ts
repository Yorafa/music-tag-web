import { describe, it, expect, beforeEach } from 'vitest';
import { dedupeFlag, isDedupeEnabled, setDedupeEnabled } from './dedupe';

describe('dedupeFlag', () => {
  it('says nothing when enabled, because the server already defaults to on', () => {
    expect(dedupeFlag(true)).toEqual({});
  });

  it('sends the explicit opt-out when disabled', () => {
    // The only way to turn the check off: check_duplicate:false must sit
    // INSIDE music_info, since a sibling key is dropped by JSON binding.
    expect(dedupeFlag(false)).toEqual({ check_duplicate: false });
  });
});

describe('isDedupeEnabled', () => {
  beforeEach(() => {
    localStorage.clear();
  });

  it('defaults to on when nothing is stored', () => {
    // The old default was off, and nothing ever sent the opt-in, which is
    // how dedup ended up dead. See the module docstring.
    expect(isDedupeEnabled()).toBe(true);
  });

  it('round-trips a stored preference', () => {
    setDedupeEnabled(false);
    expect(isDedupeEnabled()).toBe(false);
    setDedupeEnabled(true);
    expect(isDedupeEnabled()).toBe(true);
  });
});
