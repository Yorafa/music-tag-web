"use client"

import * as React from "react"
import { ScrollArea as ScrollAreaPrimitive } from "@base-ui/react/scroll-area"

import { cn } from "@/lib/utils"

/** Classes every ScrollArea root carries.
 *
 * `min-h-0` is load-bearing, not decoration.
 *
 * A flex item defaults to `min-height: auto`, which resolves to its
 * content's min-content height. The spec exempts only boxes whose OWN
 * overflow is not `visible`, and this Root is `relative` with visible
 * overflow — so it does NOT get the exemption. With `flex-1` in a
 * column, a tall child therefore refuses to shrink, the column outgrows
 * its parent, and any ancestor with `overflow-hidden` clips the overflow
 * with no scrollbar reachable.
 *
 * That is what made the scrape-results panel look truncated: every
 * result rendered, but everything past the first screenful was
 * permanently out of view. Three other call sites had already worked
 * around it by hand-writing "flex-1 min-h-0"; this puts the fix in one
 * place so the next flex-child usage cannot reintroduce it.
 */
export const SCROLL_AREA_BASE_CLASS = "relative min-h-0"

function ScrollArea({
  className,
  children,
  viewportRef,
  ...props
}: ScrollAreaPrimitive.Root.Props & {
  /** Ref to the inner scrollable element.
   *
   *  The Root is `position: relative` with visible overflow — it does NOT
   *  scroll. The Viewport is the element that does, so anything needing the
   *  scroll position (a virtualized list reading `scrollTop`, or code that
   *  calls `scrollTo`) needs THIS ref, not one on the Root. */
  viewportRef?: React.Ref<HTMLDivElement>;
}) {
  return (
    <ScrollAreaPrimitive.Root
      data-slot="scroll-area"
      className={cn(SCROLL_AREA_BASE_CLASS, className)}
      {...props}
    >
      <ScrollAreaPrimitive.Viewport
        ref={viewportRef}
        data-slot="scroll-area-viewport"
        className="size-full rounded-[inherit] transition-[color,box-shadow] outline-none focus-visible:ring-[3px] focus-visible:ring-ring/50 focus-visible:outline-1"
      >
        {children}
      </ScrollAreaPrimitive.Viewport>
      <ScrollBar />
      <ScrollAreaPrimitive.Corner />
    </ScrollAreaPrimitive.Root>
  )
}

function ScrollBar({
  className,
  orientation = "vertical",
  ...props
}: ScrollAreaPrimitive.Scrollbar.Props) {
  return (
    <ScrollAreaPrimitive.Scrollbar
      data-slot="scroll-area-scrollbar"
      data-orientation={orientation}
      orientation={orientation}
      className={cn(
        "flex touch-none p-px transition-colors select-none data-horizontal:h-2.5 data-horizontal:flex-col data-horizontal:border-t data-horizontal:border-t-transparent data-vertical:h-full data-vertical:w-2.5 data-vertical:border-l data-vertical:border-l-transparent",
        className
      )}
      {...props}
    >
      <ScrollAreaPrimitive.Thumb
        data-slot="scroll-area-thumb"
        className="relative flex-1 rounded-full bg-border"
      />
    </ScrollAreaPrimitive.Scrollbar>
  )
}

export { ScrollArea, ScrollBar }
