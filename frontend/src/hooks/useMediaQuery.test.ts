// Specs for the breakpoint hook that makes the scraper's row tap
// behaviour depend on layout.
//
// The bug: WorkstationView's right column (TrackInspector) is
// `hidden lg:flex`, so below 1024px nothing on screen renders the
// selected row's detail. The row's onClick only set a selection
// highlight, so on a phone the tap was completely inert — the one
// gesture a user would try did nothing at all.
//
// The fix branches on the same 1024px the stylesheet uses rather than on
// an independently chosen number, so the hook is the contract. These
// specs pin the two behaviours that could silently regress: a
// non-matching query, and a viewport that changes after mount (a
// rotation, or a desktop window narrowed past the breakpoint).

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { renderHook, flush } from '@/test/renderHook';
import { useMediaQuery } from './useMediaQuery';

type Listener = () => void;

/** Minimal MediaQueryList stand-in.
 *
 *  jsdom exposes window.matchMedia but the object it returns does not
 *  implement addEventListener, so the subscription path — the part that
 *  makes the layout reactive — cannot be exercised without a stub. */
function stubMatchMedia(initial: boolean) {
  const listeners = new Set<Listener>();
  let value = initial;
  const mql = {
    get matches() {
      return value;
    },
    addEventListener: (_type: string, cb: Listener) => {
      listeners.add(cb);
    },
    removeEventListener: (_type: string, cb: Listener) => {
      listeners.delete(cb);
    },
  };
  vi.stubGlobal('matchMedia', vi.fn(() => mql as unknown as MediaQueryList));
  return {
    /** Flip the query and notify subscribers, as a viewport change does. */
    set(next: boolean) {
      value = next;
      for (const cb of listeners) cb();
    },
    get listenerCount() {
      return listeners.size;
    },
  };
}

const QUERY = '(min-width: 1024px)';

describe('useMediaQuery', () => {
  beforeEach(() => {
    vi.unstubAllGlobals();
  });

  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it('reports true when the query matches', () => {
    stubMatchMedia(true);
    const { result } = renderHook(() => useMediaQuery(QUERY));
    expect(result.current).toBe(true);
  });

  it('reports false when the query does not match', () => {
    // The mobile case that was broken: a row tap has to fall through to
    // opening the detail Dialog.
    stubMatchMedia(false);
    const { result } = renderHook(() => useMediaQuery(QUERY));
    expect(result.current).toBe(false);
  });

  it('updates when the viewport crosses the breakpoint', () => {
    // The layout is responsive, so the branch can flip without a reload.
    // A hook that read matchMedia once would strand the user in the
    // wrong behaviour until they refreshed.
    const mq = stubMatchMedia(false);
    const { result } = renderHook(() => useMediaQuery(QUERY));
    expect(result.current).toBe(false);

    flush(() => mq.set(true));
    expect(result.current).toBe(true);

    flush(() => mq.set(false));
    expect(result.current).toBe(false);
  });

  it('unsubscribes on unmount', () => {
    // A leaked listener keeps the component's setState reachable after
    // teardown and fires on every later resize.
    const mq = stubMatchMedia(true);
    const { unmount } = renderHook(() => useMediaQuery(QUERY));
    expect(mq.listenerCount).toBe(1);
    unmount();
    expect(mq.listenerCount).toBe(0);
  });

  it('defaults to false when matchMedia is unavailable', () => {
    // SSR / non-browser. The narrow-viewport branch is the safe default:
    // it opens the Dialog, which is what a small screen needs.
    vi.stubGlobal('matchMedia', undefined);
    const { result } = renderHook(() => useMediaQuery(QUERY));
    expect(result.current).toBe(false);
  });

  it('re-reads the query when it subscribes, not only at init', () => {
    // A viewport change between the initialiser and the effect (fast
    // hydration, quick orientation flip) must still be picked up.
    let value = false;
    const listeners = new Set<Listener>();
    vi.stubGlobal('matchMedia', () => ({
      get matches() {
        return value;
      },
      addEventListener: (_t: string, cb: Listener) => listeners.add(cb),
      removeEventListener: (_t: string, cb: Listener) => listeners.delete(cb),
    }));
    value = true;

    const { result } = renderHook(() => useMediaQuery(QUERY));
    expect(result.current).toBe(true);
  });
});
