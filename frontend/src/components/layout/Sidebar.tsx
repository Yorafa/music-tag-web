import {
  Sparkles,
  Search,
  History,
  Settings,
  ChevronLeft,
  ChevronRight,
  Radio,
  X,
} from 'lucide-react';
import { useWorklistStore } from '@/store/useWorklistStore';
import { useNoticeStore } from '@/store/useNoticeStore';
import { cn } from '@/lib/utils';

export type NavSection = 'scraper' | 'search' | 'audit' | 'settings';

interface Props {
  activeSection: NavSection;
  onSelectSection: (section: NavSection) => void;
  collapsed: boolean;
  onToggleCollapse: () => void;
  /** Drawer variant. When provided, renders a close control in the
   *  brand header and hides the collapse toggle — "collapse" has no
   *  meaning for an overlay that is either open or closed, and the
   *  toggle used to be wired to dismiss the drawer behind a label that
   *  said "collapse". */
  onClose?: () => void;
  closeLabel?: string;
}

const NAV_ITEMS: { id: NavSection; label: string; icon: React.ElementType; description: string }[] = [
  {
    id: 'scraper',
    label: '智能刮削',
    icon: Sparkles,
    description: '批量标签与自动整理',
  },
  {
    id: 'search',
    label: '云端检索',
    icon: Search,
    description: '多源音乐搜索与下载',
  },
  {
    id: 'audit',
    label: '操作审计',
    icon: History,
    description: '操作历史与变动追踪',
  },
  {
    id: 'settings',
    label: '系统设置',
    icon: Settings,
    description: '路径与音乐源配置',
  },
];

export function Sidebar({
  activeSection,
  onSelectSection,
  collapsed,
  onToggleCollapse,
  onClose,
  closeLabel = '关闭导航',
}: Props) {
  const worklistCount = useWorklistStore((s) => s.rows.length);
  const selectedCount = useWorklistStore((s) => s.selectedIds.length);
  const unreadNotices = useNoticeStore((s) => s.unreadCount());

  return (
    <aside
      className={cn(
        'relative h-full flex flex-col glass-panel border-r border-border/80 transition-all duration-300 z-30 shrink-0 select-none',
        collapsed ? 'w-16' : 'w-56 sm:w-60',
      )}
    >
      {/* Brand Header */}
      <div
        className={cn(
          'h-14 flex items-center px-3.5 border-b border-border/60 gap-3 overflow-hidden',
          collapsed ? 'justify-center' : 'justify-between',
        )}
      >
        <div className="flex items-center gap-2.5 min-w-0">
          <div className="w-8 h-8 rounded-lg bg-gradient-to-br from-indigo-500 via-purple-500 to-pink-500 flex items-center justify-center text-white shadow-md shadow-indigo-500/20 shrink-0">
            <Radio className="w-4 h-4 animate-pulse" />
          </div>
          {!collapsed && (
            <div className="min-w-0">
              <div className="text-xs font-bold tracking-tight text-foreground truncate flex items-center gap-1.5">
                <span>Music Tag Web</span>
                <span className="text-[10px] px-1 py-0.2 rounded bg-primary/10 text-primary font-mono font-normal">
                  v2.2
                </span>
              </div>
              <p className="text-[10px] text-muted-foreground truncate">
                自建音乐标签管理系统
              </p>
            </div>
          )}
        </div>

        {/* Drawer close control. It lives in the header row rather than
            being absolutely positioned by the parent: an absolutely
            placed button had to be anchored to a hardcoded wrapper
            width that did not match this component's own responsive
            width, so it drifted into the gap beside the sidebar and read
            as a stray floating control. */}
        {onClose && (
          <button
            type="button"
            onClick={onClose}
            aria-label={closeLabel}
            title={closeLabel}
            className="ml-auto shrink-0 inline-flex items-center justify-center w-7 h-7 rounded-lg text-muted-foreground hover:text-foreground hover:bg-muted/60 transition-colors"
          >
            <X className="w-4 h-4" />
          </button>
        )}
      </div>

      {/* Navigation Menu */}
      <nav className="flex-1 py-3 px-2 space-y-1 overflow-y-auto">
        {NAV_ITEMS.map((item) => {
          const Icon = item.icon;
          const isActive = activeSection === item.id;
          return (
            <button
              key={item.id}
              type="button"
              onClick={() => onSelectSection(item.id)}
              className={cn(
                'w-full flex items-center gap-3 px-2.5 py-2 rounded-xl text-xs font-medium transition-all relative group',
                isActive
                  ? 'bg-primary text-primary-foreground shadow-sm shadow-primary/25 font-semibold'
                  : 'text-muted-foreground hover:text-foreground hover:bg-muted/50',
                collapsed && 'justify-center px-0 py-2.5',
              )}
              title={collapsed ? `${item.label} — ${item.description}` : undefined}
            >
              <Icon className={cn('w-4 h-4 shrink-0', isActive ? 'text-primary-foreground' : 'text-muted-foreground group-hover:text-foreground')} />

              {!collapsed && (
                <div className="flex-1 text-left truncate flex items-center justify-between">
                  <span>{item.label}</span>
                  {/* Context Badges */}
                  {item.id === 'scraper' && worklistCount > 0 && (
                    <span
                      className={cn(
                        'text-[10px] px-1.5 py-0.2 rounded-full font-mono font-medium',
                        isActive
                          ? 'bg-primary-foreground/20 text-primary-foreground'
                          : 'bg-amber-500/15 text-amber-500 dark:text-amber-400 border border-amber-500/20',
                      )}
                    >
                      {selectedCount > 0 ? `${selectedCount}/${worklistCount}` : worklistCount}
                    </span>
                  )}
                  {item.id === 'audit' && unreadNotices > 0 && (
                    <span className="w-2 h-2 rounded-full bg-indigo-500" />
                  )}
                </div>
              )}

              {/* Active Indicator on Left */}
              {isActive && collapsed && (
                <div className="absolute left-0 top-2 bottom-2 w-1 rounded-r bg-primary-foreground" />
              )}
            </button>
          );
        })}
      </nav>

      {/* Footer — collapse toggle only.
          主题 / 通知 / 登出 used to sit here, below the nav, which is the
          least reachable corner of the app: a phone had to open the nav
          drawer first, and a collapsed desktop sidebar stacked all three
          into a 32px column. They now live in the top header beside the
          task center, which is mounted at every width and in every
          section.

          The footer as a whole is suppressed in the drawer variant, not
          just the toggle inside it. The toggle is the only child, so
          gating the button alone left an empty `border-t` box — a stray
          divider and 16px of padding at the bottom of the mobile drawer.
          A collapse control is also a lie in the drawer, where the panel
          is dismissed via onClose and has no collapsed state. */}
      {!onClose && (
        <div className="p-2 border-t border-border/60 space-y-1">
          <button
            type="button"
            onClick={onToggleCollapse}
            className={cn(
              'w-full flex items-center gap-2 py-1.5 px-2 rounded-lg text-[11px] text-muted-foreground/70 hover:text-foreground hover:bg-muted/40 transition-colors',
              collapsed ? 'justify-center' : 'justify-between',
            )}
            title={collapsed ? '展开侧边栏' : '折叠侧边栏'}
          >
            {!collapsed && <span>收起侧边栏</span>}
            {collapsed ? (
              <ChevronRight className="w-3.5 h-3.5" />
            ) : (
              <ChevronLeft className="w-3.5 h-3.5" />
            )}
          </button>
        </div>
      )}
    </aside>
  );
}
