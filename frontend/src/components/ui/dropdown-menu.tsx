// Shadcn-style DropdownMenu primitive wrapping base-ui's Menu. Used by
// the Worklist's per-row kebab menu (play / detail / remove). Also
// available for Plan B's PlayView when it needs contextual actions.
//
// Implementation note: base-ui's MenuPrimitive accepts a render prop on
// the Positioner that consumes MenuPositionerState (which expects an
// anchorHidden transition field). To keep the type surface flat AND
// avoid that render-prop mismatch, the public-facing component takes a
// narrow props shape (`align`, `sideOffset`, `children`) and dispatches
// them to the right primitive directly. The popup itself reads
// base-ui's MenuPopupState via its own internal render prop, and
// forwards only what we need (className) into the consumer layout.

import * as React from 'react';
import { Menu as MenuPrimitive } from '@base-ui/react/menu';

import { cn } from '@/lib/utils';

function DropdownMenu({ ...props }: MenuPrimitive.Root.Props) {
  return <MenuPrimitive.Root data-slot="dropdown-menu" {...props} />;
}

function DropdownMenuTrigger({ ...props }: MenuPrimitive.Trigger.Props) {
  return <MenuPrimitive.Trigger data-slot="dropdown-menu-trigger" {...props} />;
}

function DropdownMenuPortal({ ...props }: MenuPrimitive.Portal.Props) {
  return <MenuPrimitive.Portal data-slot="dropdown-menu-portal" {...props} />;
}

interface DropdownMenuContentProps {
  className?: string;
  align?: 'start' | 'center' | 'end';
  sideOffset?: number;
  children?: React.ReactNode;
}

function DropdownMenuContent({
  className,
  align = 'start',
  sideOffset = 4,
  children,
}: DropdownMenuContentProps) {
  return (
    <DropdownMenuPortal>
      <MenuPrimitive.Positioner
        align={align}
        sideOffset={sideOffset}
        className="isolate z-50 outline-none"
      >
        <MenuPrimitive.Popup
          data-slot="dropdown-menu-content"
          className={cn(
            'z-50 min-w-[10rem] origin-(--transform-origin) overflow-hidden rounded-md border border-border bg-popover p-1 text-xs text-popover-foreground shadow-md',
            'data-open:animate-in data-open:fade-in-0 data-open:zoom-in-95',
            'data-closed:animate-out data-closed:fade-out-0 data-closed:zoom-out-95',
            className,
          )}
        >
          {children}
        </MenuPrimitive.Popup>
      </MenuPrimitive.Positioner>
    </DropdownMenuPortal>
  );
}

function DropdownMenuItem({
  className,
  ...props
}: MenuPrimitive.Item.Props) {
  return (
    <MenuPrimitive.Item
      data-slot="dropdown-menu-item"
      className={cn(
        'relative flex w-full cursor-pointer select-none items-center gap-2 rounded-sm px-2 py-1.5 text-xs outline-none transition-colors',
        'data-highlighted:bg-accent data-highlighted:text-foreground',
        'data-disabled:pointer-events-none data-disabled:opacity-50',
        className,
      )}
      {...props}
    />
  );
}

export {
  DropdownMenu,
  DropdownMenuTrigger,
  DropdownMenuContent,
  DropdownMenuItem,
};
