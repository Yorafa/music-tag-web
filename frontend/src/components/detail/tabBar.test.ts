// Specs for the song-detail tab bar's layout invariant.
//
// The overflow this guards was invisible in the JSX and did not break the
// build: `w-full` + `mx-6` on a child of a `flex-col` root renders 24px
// wider than its parent, and the `overflow-hidden` on the ancestor clipped
// the excess into the rightmost tab. A reviewer reading the class list has
// no reason to compute that, which is why the rule is written down here
// instead of left implicit in the markup.

import { describe, it, expect } from 'vitest';
import { TAB_BAR_CLASS, overflowsColumnFlex } from './tabBar';

describe('the song-detail tab bar', () => {
  it('does not overflow its column-flex parent', () => {
    // The regression: an explicit width disables align-self:stretch, so
    // `mx-6` + `w-full` = 100% + 48px inside a 100%-wide parent.
    expect(overflowsColumnFlex(TAB_BAR_CLASS)).toBe(false);
  });

  it('overrides the primitive w-fit rather than inheriting it', () => {
    // TabsList ships `w-fit`. With no width of its own the bar would
    // shrink to its labels instead of spanning the dialog, so the
    // override has to be explicit — and it has to be `auto`, not a number.
    expect(TAB_BAR_CLASS).toMatch(/(^|\s)w-auto(\s|$)/);
  });

  it('keeps the app pill idiom', () => {
    // SettingsView's: bg-muted/60 p-1 rounded-xl. The active tab then
    // lifts via the primitive's own data-active rules.
    expect(TAB_BAR_CLASS).toContain('bg-muted/60');
    expect(TAB_BAR_CLASS).toContain('p-1');
    expect(TAB_BAR_CLASS).toContain('rounded-xl');
  });

  it('divides the five tabs evenly', () => {
    // Without grid-cols-5 the bar's width comes from the labels, and the
    // rows below it start at a different x than the header above.
    expect(TAB_BAR_CLASS).toContain('grid');
    expect(TAB_BAR_CLASS).toContain('grid-cols-5');
  });

  it('does not shrink vertically', () => {
    // shrink-0 keeps the bar from being compressed when the panels below
    // it are tall; without it flex would eat the height first.
    expect(TAB_BAR_CLASS).toContain('shrink-0');
  });
});

describe('overflowsColumnFlex', () => {
  it('flags the exact regression it was written for', () => {
    expect(overflowsColumnFlex('grid w-full mx-6')).toBe(true);
  });

  it('accepts auto width with margins', () => {
    expect(overflowsColumnFlex('grid w-auto mx-6')).toBe(false);
  });

  it('accepts an explicit width with no margins', () => {
    expect(overflowsColumnFlex('grid w-full')).toBe(false);
  });

  it('is not fooled by margin utilities in a calc or a variant', () => {
    // `mt-5` is a vertical margin and must not read as a horizontal one;
    // `w-max`/`w-min` are not a pinned width.
    expect(overflowsColumnFlex('w-full mt-5')).toBe(false);
    expect(overflowsColumnFlex('w-max mx-6')).toBe(false);
  });
});
