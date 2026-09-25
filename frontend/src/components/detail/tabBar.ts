// The song-detail tab bar's class list, extracted so the CSS invariant it
// depends on can actually be asserted.
//
// The bug this guards: the tab bar is a direct child of the Tabs root,
// which is `flex-col`. In a column flex container a child stretches to
// fill the cross axis (its width) — but only while its width is `auto`.
// Declaring an explicit width *and* a horizontal margin gives an used
// outer size of `margin + width + margin`, so `w-full` + `mx-6` renders
// the bar 24px wider than its parent. The ancestor `<aside>` is
// `overflow-hidden`, so the overflow did not spill onto the page; it was
// clipped, which ate 24px of the last grid column and cut into the
// rightmost tab (指纹). The bar looked "slightly too wide" rather than
// obviously broken, which is why it survived a visual pass.
//
// The fix is `w-auto`, not the absence of a width: with no width of its
// own, the flexbox stretch applies and correctly subtracts the margins.
// Removing `w-full` alone would leave the primitive's own `w-fit` in
// place and the bar would shrink to its content instead of spanning the
// dialog, so the override has to be explicit — and explicit, it has to
// be `auto` rather than a number.
//
// Kept in its own module because none of this is visible in the JSX: the
// invariant is a relationship between three utility classes, and the spec
// below is the only place that relationship is written down.

export const TAB_BAR_CLASS =
  'grid grid-cols-5 w-auto h-11 bg-muted/60 p-1 rounded-xl shrink-0 mx-6 mt-5';

/** Does this class list pin a width on a flex item that also has
 *  horizontal margins? If so the item overflows its column-flex parent by
 *  the margin amount and the overflow is clipped rather than shown. */
export function overflowsColumnFlex(className: string): boolean {
  // `auto` is what enables the stretch, and `fit`/`min`/`max` are
  // constraints rather than a definite width, so none of them pin the box
  // the way `w-full` does. The exclusions need \b, not a literal dash:
  // `w-max` is followed by a space, so `max-` never lines up and every
  // `w-max` would read as a pinned width.
  const hasExplicitWidth = /(^|\s)w-(?!auto\b|fit\b|min\b|max\b)/.test(className);
  // `mx-` is two characters before the dash, so the alternation has to
  // name the whole left/right axis — a bare `[mx]-` looks right and never
  // matches, because it consumes the `m` and then demands a `-` where the
  // `x` is. `mt-`/`my-` are vertical and must not count.
  const hasHorizontalMargin = /(^|\s)(mx|ml|mr)-/.test(className);
  return hasExplicitWidth && hasHorizontalMargin;
}
