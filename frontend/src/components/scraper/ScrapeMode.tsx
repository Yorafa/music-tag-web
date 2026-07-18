// Scrape mode entry component. Replaces the deleted
// components/layout/ScrapeView.tsx + ScrapeView's three-column
// Toolbar/FileBrowser/SearchResults arrangement with a single vertical
// stack: top tools row → Worklist header bar → Worklist body.
//
// The "add directories" affordance owns the DirPickerDrawer open state
// here; the actual drawer component lives elsewhere and accepts the
// destination='worklist' flake so Plan B can reuse it with
// destination='library' without modification.
//
// The detail Dialog (TagEditor + ScrapeResults dual-panel) is NOT
// inside this component — it continues to be rendered at AppShell
// root, since it's mode-agnostic (DESIGN.md §A1). Both this
// component and the route back to play mode share the same Dialog.
//
// Responsive layout: child components own their own `sm:` breakpoints
// (DirPickerDrawer flips to a right-side drawer ≥640px, etc.), so
// ScrapeMode itself doesn't need to know about viewport width.

import { useState } from 'react';
import { ScrapeTopBar } from '@/components/scraper/ScrapeTopBar';
import { WorklistHeaderBar } from '@/components/scraper/WorklistHeaderBar';
import { Worklist } from '@/components/scraper/Worklist';
import { DirPickerDrawer } from '@/components/scraper/DirPickerDrawer';

export function ScrapeMode() {
  const [drawerOpen, setDrawerOpen] = useState(false);

  return (
    <>
      <div className="flex-1 flex flex-col overflow-hidden">
        <ScrapeTopBar />
        <WorklistHeaderBar onOpenDirPicker={() => setDrawerOpen(true)} />
        <Worklist />
      </div>

      <DirPickerDrawer
        open={drawerOpen}
        onOpenChange={setDrawerOpen}
        destination="worklist"
      />
    </>
  );
}
