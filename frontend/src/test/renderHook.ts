// Plain-TSX renderHook for specs.
//
// The project has no component-renderer dependency — @testing-library
// appears nowhere in package.json, and every existing spec is a
// pure-logic test. Adding a library for one hook would be a poor trade,
// so this builds the ~20 lines on react-dom/client and React's own
// `act`, which is the same mechanism the renderer uses internally.
//
// Kept in src/test/ rather than beside the hook so future hook specs
// share one implementation instead of each rolling their own.
//
// The .tsx twin exists because this file is plain .ts: JSX in a spec is
// not worth renaming every future test file to .tsx for.

import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { createElement, type ReactElement } from 'react';

export interface HookHandle<TValue> {
  /** Stable box, so `result.current` reads like a ref. */
  result: { current: TValue };
  rerender: () => void;
  unmount: () => void;
}

/** React only treats `act()` as authoritative when it knows it is under
 *  test. Without this flag every spec renders fine but React logs
 *  "The current testing environment is not configured to support
 *  act(...)" and skips the effect flush, which would make the
 *  subscription assertions below pass for the wrong reason. Set here
 *  rather than in the spec so any future hook spec inherits it. */
function enableActEnvironment(): void {
  (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
}

/**
 * Render a hook that takes no props and return a handle to it.
 *
 * `render()` is a tiny wrapper around `renderHook` for the common
 * zero-props case.
 */
export function renderHook<TValue>(
  callback: () => TValue,
): HookHandle<TValue> {
  enableActEnvironment();
  const container = document.createElement('div');
  document.body.appendChild(container);

  const result = { current: undefined as TValue };
  let root!: Root;

  function Probe(): null {
    result.current = callback();
    return null;
  }

  act(() => {
    root = createRoot(container);
    root.render(createElement(Probe));
  });

  return {
    result,
    rerender() {
      act(() => {
        root.render(createElement(Probe));
      });
    },
    unmount() {
      act(() => {
        root.unmount();
      });
      container.remove();
    },
  };
}

/** Run `fn` inside act(), flushing effects and state updates. */
export function flush(fn: () => void): void {
  enableActEnvironment();
  act(() => {
    fn();
  });
}

export type { ReactElement };
