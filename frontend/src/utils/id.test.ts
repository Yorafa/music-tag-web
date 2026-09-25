// Specs for newId.
//
// The regression: the app is served over plain HTTP on a LAN address, which
// is not a secure context, so `crypto.randomUUID` is undefined and
// `useNoticeStore.push` threw on every toast. These pin each tier of the
// fallback so the insecure-context path cannot silently regress to
// calling a function that isn't there.

import { describe, it, expect, afterEach, vi } from 'vitest';
import { newId } from './id';

const UUID_V4 = /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/;

afterEach(() => {
  vi.unstubAllGlobals();
  // Also needed: unstubAllGlobals does NOT restore vi.spyOn mocks, so a
  // pinned Math.random leaked into the next test and made a 500-id burst
  // collapse to a single distinct value.
  vi.restoreAllMocks();
});

describe('newId', () => {
  it('uses randomUUID when the context is secure', () => {
    const randomUUID = vi.fn(() => '11111111-2222-4333-8444-555555555555');
    vi.stubGlobal('crypto', { randomUUID, getRandomValues: vi.fn() });
    expect(newId()).toBe('11111111-2222-4333-8444-555555555555');
    expect(randomUUID).toHaveBeenCalledTimes(1);
  });

  it('falls back to getRandomValues when randomUUID is absent', () => {
    // The LAN case: `crypto` exists, `randomUUID` does not. Calling it
    // here is what produced "randomUUID is not a function".
    vi.stubGlobal('crypto', {
      randomUUID: undefined,
      getRandomValues: (arr: Uint8Array) => {
        arr.fill(0xab);
        return arr;
      },
    });
    expect(() => newId()).not.toThrow();
    expect(newId()).toMatch(UUID_V4);
  });

  it('does not call a truthy-but-not-callable randomUUID', () => {
    // `typeof x === 'function'`, not a truthiness test. An older browser or
    // a partial polyfill can expose the property as something else, and
    // calling it is the exact TypeError this module exists to prevent.
    // (`vi.fn()` would not test this — a mock IS callable, so the correct
    // implementation calls it.)
    for (const bogus of [true, 'nope', 42, {}]) {
      vi.stubGlobal('crypto', {
        randomUUID: bogus,
        getRandomValues: (arr: Uint8Array) => {
          arr.fill(0xab);
          return arr;
        },
      });
      expect(() => newId()).not.toThrow();
      expect(newId()).toMatch(UUID_V4);
    }
  });

  it('survives having no crypto at all', () => {
    vi.stubGlobal('crypto', undefined);
    const id = newId();
    expect(typeof id).toBe('string');
    expect(id.length).toBeGreaterThan(0);
  });

  it('keeps ids unique in the no-crypto tier on the counter alone', () => {
    // With Math.random and Date.now pinned, the ONLY thing that can make
    // two calls differ is the monotonic counter. Asserting uniqueness
    // without pinning them proves nothing: it passed against a counter
    // that had been neutered to `seq += 0`, because Math.random was
    // still varying. React list keys must not collide, and this is the
    // tier with the least to fall back on.
    vi.stubGlobal('crypto', undefined);
    vi.spyOn(Math, 'random').mockReturnValue(0.5);
    vi.spyOn(Date, 'now').mockReturnValue(1_700_000_000_000);

    const ids = Array.from({ length: 200 }, () => newId());
    expect(new Set(ids).size).toBe(200);
  });

  it('sets the v4 version and variant bits in the getRandomValues tier', () => {
    // 0xff everywhere would otherwise yield a malformed UUID.
    vi.stubGlobal('crypto', {
      randomUUID: undefined,
      getRandomValues: (arr: Uint8Array) => {
        arr.fill(0xff);
        return arr;
      },
    });
    const id = newId();
    expect(id[14]).toBe('4');
    expect('89ab').toContain(id[19]);
    expect(id).toMatch(UUID_V4);
  });

  it('returns distinct ids in a burst', () => {
    vi.stubGlobal('crypto', {
      randomUUID: undefined,
      getRandomValues: (arr: Uint8Array) => {
        for (let i = 0; i < arr.length; i++) arr[i] = Math.floor(Math.random() * 256);
        return arr;
      },
    });
    const ids = new Set(Array.from({ length: 500 }, () => newId()));
    expect(ids.size).toBe(500);
  });
});
