// Unified modern app shell. Supports 5 core navigation sections:
//   - library:  Local library tracks, playback, filter, instant editor
//   - scraper:  Worklist queue, auto-scraping, filename parser, folder tidy
//   - search:   Cloud multi-source search, preview stream & download
//   - audit:    Operation history logs, action/status filters & details
//   - settings: General paths, music sources manager & hot reload
//
// Hosts the global song-detail Dialog (TrackDetailDialog), bottom
// PlayerBar, mobile navigation sheet, and ToastHost.

import { useState, useCallback } from 'react';
import { TrackDetailDialog } from '@/components/detail/TrackDetailDialog';
import { WorkstationView } from '@/components/workstation/WorkstationView';
import { PlayView } from '@/components/play/PlayView';
import { CloudSearchView } from '@/components/search/CloudSearchView';
import { AuditLogView } from '@/components/audit/AuditLogView';
import { SettingsView } from '@/components/settings/SettingsView';
import { PlayerBar } from '@/components/player/PlayerBar';
import { ToastHost } from '@/components/common/ToastHost';
import { Sidebar, type NavSection } from '@/components/layout/Sidebar';
import { TopHeader } from '@/components/layout/TopHeader';
import { readBool, writeBool } from '@/utils/persist';

export type AppSection = NavSection;
export type AppMode = 'play' | 'scrape'; // Backward compat

interface Props {
  initialSection?: AppSection;
}

const SECTION_KEY = 'appShell.section';
const SIDEBAR_COLLAPSED_KEY = 'appShell.sidebarCollapsed';

function readStoredSection(): AppSection {
  try {
    const s = localStorage.getItem(SECTION_KEY);
    if (s === 'library' || s === 'scraper' || s === 'search' || s === 'audit' || s === 'settings') {
      return s;
    }
    // Backward compatibility for mode
    const mode = localStorage.getItem('appShell.mode');
    if (mode === 'scrape') return 'scraper';
    if (mode === 'play') return 'library';
  } catch {
    /* ignore */
  }
  return 'scraper';
}

export function AppShell({ initialSection }: Props) {
  const [section, setSectionState] = useState<AppSection>(() => initialSection ?? readStoredSection());
  const [sidebarCollapsed, setSidebarCollapsed] = useState<boolean>(() =>
    readBool(SIDEBAR_COLLAPSED_KEY) ?? false,
  );
  const [mobileNavOpen, setMobileNavOpen] = useState(false);

  const setSection = useCallback((next: AppSection) => {
    setSectionState(next);
    setMobileNavOpen(false);
    try {
      localStorage.setItem(SECTION_KEY, next);
      localStorage.setItem('appShell.mode', next === 'scraper' ? 'scrape' : 'play');
    } catch {
      /* ignore */
    }
  }, []);

  const toggleSidebar = useCallback(() => {
    setSidebarCollapsed((prev) => {
      const next = !prev;
      writeBool(SIDEBAR_COLLAPSED_KEY, next);
      return next;
    });
  }, []);


  return (
    <div className="flex h-screen w-screen overflow-hidden bg-background text-foreground">
      {/* Desktop Navigation Sidebar */}
      <div className="hidden md:flex shrink-0 h-full">
        <Sidebar
          activeSection={section}
          onSelectSection={setSection}
          collapsed={sidebarCollapsed}
          onToggleCollapse={toggleSidebar}
        />
      </div>

      {/* Mobile Navigation Drawer Overlay */}
      {mobileNavOpen && (
        <div className="fixed inset-0 z-50 md:hidden">
          {/* Scrim. Dismissal target for taps outside the panel. */}
          <div
            className="absolute inset-0 bg-black/60 backdrop-blur-xs transition-opacity"
            onClick={() => setMobileNavOpen(false)}
          />
          {/* The panel shrink-wraps the Sidebar instead of declaring a
              width of its own. It used to hardcode w-64 (256px) around a
              Sidebar that is w-56 (224px) on phones and sm:w-60 (240px)
              above that: 32px and 16px of scrim showed through as a dark
              strip down the right edge, and the absolutely-positioned
              close button was anchored to the 256px box, so it floated in
              that strip, detached from the sidebar it belonged to. */}
          <div className="relative h-full z-10 flex flex-col shadow-2xl animate-in slide-in-from-left duration-200 max-w-[85vw]">
            <Sidebar
              activeSection={section}
              onSelectSection={setSection}
              collapsed={false}
              onToggleCollapse={() => setMobileNavOpen(false)}
              onClose={() => setMobileNavOpen(false)}
            />
          </div>
        </div>
      )}

      {/* Main Content Workspace Container */}
      <div className="flex-1 flex flex-col min-w-0 h-full overflow-hidden">
        {/* Top Header */}
        <TopHeader
          activeSection={section}
          onOpenMobileNav={() => setMobileNavOpen(true)}
        />

        {/* Dynamic Section Viewport */}
        <div className="flex-1 flex flex-col min-h-0 overflow-hidden relative">
          {section === 'library' && <PlayView />}
          {section === 'scraper' && <WorkstationView />}
          {section === 'search' && <CloudSearchView />}
          {section === 'audit' && <AuditLogView />}
          {section === 'settings' && <SettingsView />}
        </div>

        {/* Persistent Bottom Player Bar */}
        <PlayerBar />
      </div>

      {/* Song detail Dialog — one surface for 音乐库 and 智能刮削 */}
      <TrackDetailDialog />

      {/* Global Toast Host */}
      <ToastHost />
    </div>
  );
}
