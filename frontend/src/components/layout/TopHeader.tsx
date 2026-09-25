import {
  Music2,
  Sparkles,
  Search,
  History,
  Settings,
  Menu,
} from 'lucide-react';
import type { NavSection } from './Sidebar';
import { TaskCenterDropdown } from './TaskCenterDropdown';
import { useWorklistStore } from '@/store/useWorklistStore';
import { useLibraryStore } from '@/store/useLibraryStore';

interface Props {
  activeSection: NavSection;
  onOpenMobileNav: () => void;
}

const SECTION_HEADERS: Record<
  NavSection,
  { title: string; subtitle: string; icon: React.ElementType }
> = {
  library: {
    title: '本地曲库',
    subtitle: '管理并试听本地音乐库中的音频与标签',
    icon: Music2,
  },
  scraper: {
    title: '智能刮削',
    subtitle: '多源元数据匹配、批量标签更新与文件规范整理',
    icon: Sparkles,
  },
  search: {
    title: '云端检索',
    subtitle: '跨网易云/QQ/酷狗/酷我/咪咕/MusicBrainz/YouTube 全网音源搜索',
    icon: Search,
  },
  audit: {
    title: '操作审计',
    subtitle: '全量操作历史记录、变动追踪与版本审计',
    icon: History,
  },
  settings: {
    title: '系统设置',
    subtitle: '配置默认存储路径、管理音乐源与 YAML 覆写',
    icon: Settings,
  },
};

export function TopHeader({ activeSection, onOpenMobileNav }: Props) {
  const current = SECTION_HEADERS[activeSection];
  const Icon = current.icon;

  const libraryRows = useLibraryStore((s) => s.rows.length);
  const worklistRows = useWorklistStore((s) => s.rows.length);
  const selectedRows = useWorklistStore((s) => s.selectedIds.length);

  return (
    <header className="h-12 glass-header px-4 flex items-center justify-between shrink-0 select-none z-20">
      <div className="flex items-center gap-3 min-w-0">
        {/* Mobile menu trigger */}
        <button
          type="button"
          onClick={onOpenMobileNav}
          className="md:hidden inline-flex items-center justify-center w-8 h-8 rounded-lg text-muted-foreground hover:text-foreground hover:bg-muted/50"
          aria-label="打开导航菜单"
        >
          <Menu className="w-4 h-4" />
        </button>

        {/* Section title & subtitle */}
        <div className="flex items-center gap-2 min-w-0">
          <div className="w-6 h-6 rounded-md bg-primary/10 text-primary flex items-center justify-center shrink-0">
            <Icon className="w-3.5 h-3.5" />
          </div>
          <div className="min-w-0">
            <h1 className="text-xs font-semibold text-foreground truncate flex items-center gap-2">
              <span>{current.title}</span>
              <span className="hidden sm:inline-block text-[11px] text-muted-foreground font-normal opacity-80">
                — {current.subtitle}
              </span>
            </h1>
          </div>
        </div>
      </div>

      {/* Right Stats & Task Center */}
      <div className="flex items-center gap-2 text-xs">
        {activeSection === 'library' && libraryRows > 0 && (
          <span className="hidden sm:inline-block px-2 py-0.5 rounded-full bg-muted/60 text-muted-foreground text-[11px] font-mono">
            共 {libraryRows} 首本地曲目
          </span>
        )}
        {activeSection === 'scraper' && (
          <span className="hidden sm:inline-block px-2 py-0.5 rounded-full bg-muted/60 text-muted-foreground text-[11px] font-mono">
            {selectedRows > 0 ? (
              <span className="text-primary font-medium">已选 {selectedRows} / {worklistRows} 项</span>
            ) : (
              <span>队列 {worklistRows} 项</span>
            )}
          </span>
        )}
        <TaskCenterDropdown />
      </div>
    </header>
  );
}
