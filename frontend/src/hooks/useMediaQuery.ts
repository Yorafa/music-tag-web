import { useEffect, useState } from 'react';

/** Subscribe to a CSS media query.
 *
 *  Exists because several layouts decide behaviour — not just styling —
 *  from a Tailwind breakpoint. WorkstationView's right column is
 *  `hidden lg:flex`, so below 1024px there is no TrackInspector on
 *  screen and a row tap has nowhere to reveal its detail. That is a
 *  behavioural branch, and reading it off `window.matchMedia` keeps it
 *  tied to the same 1024px the stylesheet uses rather than to a second,
 *  independently-chosen number.
 *
 *  Returns false during SSR and before the first effect run, which is
 *  the safe default: it picks the narrow-viewport behaviour, and the
 *  effect corrects it immediately after mount. */
export function useMediaQuery(query: string): boolean {
  const [matches, setMatches] = useState<boolean>(() => {
    if (typeof window === 'undefined' || !window.matchMedia) return false;
    return window.matchMedia(query).matches;
  });

  useEffect(() => {
    if (typeof window === 'undefined' || !window.matchMedia) return;
    const mq = window.matchMedia(query);
    const onChange = () => setMatches(mq.matches);
    // Re-read on subscribe: the viewport may have changed between the
    // initialiser and this effect (hydration, a fast orientation flip).
    onChange();
    mq.addEventListener('change', onChange);
    return () => mq.removeEventListener('change', onChange);
  }, [query]);

  return matches;
}
