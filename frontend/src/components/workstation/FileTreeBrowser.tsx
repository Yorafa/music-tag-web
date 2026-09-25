import { useEffect, useState, useCallback, useMemo } from 'react';
import {
  Folder,
  FolderOpen,
  ChevronRight,
  ChevronDown,
  RefreshCw,
  Search,
  Music2,
  Clock,
  CheckCircle2,
  AlertCircle,
  Sparkles,
  HardDrive,
  FolderPlus,
} from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Badge } from '@/components/ui/badge';
import { ScrollArea } from '@/components/ui/scroll-area';
import { getFileList } from '@/api/client';
import { useWorklistStore, type WorklistFilter } from '@/store/useWorklistStore';
import { useNoticeStore } from '@/store/useNoticeStore';
import { cn } from '@/lib/utils';
import type { FileNode } from '@/types';

interface TreeNode {
  path: string;
  name: string;
  isDir: boolean;
  children?: TreeNode[];
  loaded?: boolean;
  expanded?: boolean;
}

interface Props {
  className?: string;
  onOpenDirPicker?: () => void;
}

export function FileTreeBrowser({ className, onOpenDirPicker }: Props) {
  const rows = useWorklistStore((s) => s.rows);
  const filter = useWorklistStore((s) => s.filter);
  const setFilter = useWorklistStore((s) => s.setFilter);
  const enqueueDirs = useWorklistStore((s) => s.enqueueDirs);

  const [treeRoot, setTreeRoot] = useState<TreeNode[]>([]);
  const [loading, setLoading] = useState(false);
  const [activeDirPath, setActiveDirPath] = useState<string>('');
  const [searchQuery, setSearchQuery] = useState<string>('');
  const [expandedPaths, setExpandedPaths] = useState<Set<string>>(new Set(['']));

  // Fetch directory nodes
  const loadDirectory = useCallback(async (dirPath: string) => {
    try {
      const res = await getFileList(dirPath);
      const items: FileNode[] = res?.data?.[0]?.children ?? [];
      const dirNodes: TreeNode[] = items
        .filter((item) => item.icon === 'icon-folder')
        .map((item) => {
          const full = dirPath ? `${dirPath}/${item.name}` : item.name;
          return {
            path: full,
            name: item.name,
            isDir: true,
            children: [],
            loaded: false,
          };
        });
      return dirNodes;
    } catch {
      return [];
    }
  }, []);

  // Initial root directory load
  const refreshRoot = useCallback(async () => {
    setLoading(true);
    try {
      const rootDirs = await loadDirectory('');
      setTreeRoot(rootDirs);
    } finally {
      setLoading(false);
    }
  }, [loadDirectory]);

  useEffect(() => {
    let mounted = true;
    loadDirectory('').then((rootDirs) => {
      if (mounted) {
        setTreeRoot(rootDirs);
      }
    });
    return () => {
      mounted = false;
    };
  }, [loadDirectory]);

  // Toggle expand folder
  const toggleExpand = async (node: TreeNode) => {
    const nextSet = new Set(expandedPaths);
    if (nextSet.has(node.path)) {
      nextSet.delete(node.path);
      setExpandedPaths(nextSet);
    } else {
      nextSet.add(node.path);
      setExpandedPaths(nextSet);
      if (!node.loaded) {
        const children = await loadDirectory(node.path);
        node.children = children;
        node.loaded = true;
        setTreeRoot([...treeRoot]);
      }
    }
  };

  // Enqueue directory directly into worklist
  const handleLoadDir = async (dirPath: string, e: React.MouseEvent) => {
    e.stopPropagation();
    try {
      const { added, skipped } = await enqueueDirs([dirPath]);
      if (added > 0) {
        useNoticeStore.getState().push(`已载入 ${added} 首音乐 (跳过 ${skipped} 重复项)`, 'info');
      } else {
        useNoticeStore.getState().push('该目录下无新音频文件或已全部收录', 'warn');
      }
    } catch (err) {
      const msg = err instanceof Error ? err.message : String(err);
      useNoticeStore.getState().push(`载入失败: ${msg}`, 'error');
    }
  };

  // Smart collection counts
  const counts = useMemo(() => {
    return {
      all: rows.length,
      pending: rows.filter((r) => r.status === 'pending').length,
      scraped: rows.filter((r) => r.status === 'scraped').length,
      failed: rows.filter((r) => r.status === 'failed').length,
    };
  }, [rows]);

  const filterItems: Array<{ id: WorklistFilter; label: string; icon: React.ElementType; count: number; color?: string }> = [
    { id: 'all', label: '所有曲目', icon: Music2, count: counts.all },
    { id: 'pending', label: '待刮削', icon: Clock, count: counts.pending, color: 'text-amber-500' },
    { id: 'scraped', label: '已刮削', icon: CheckCircle2, count: counts.scraped, color: 'text-emerald-500' },
    { id: 'failed', label: '失败/需复核', icon: AlertCircle, count: counts.failed, color: 'text-destructive' },
  ];

  // Render a directory item recursively
  const renderDirItem = (node: TreeNode, depth: number = 0) => {
    const isExpanded = expandedPaths.has(node.path);
    const isSelected = activeDirPath === node.path;
    const matchesSearch = !searchQuery || node.name.toLowerCase().includes(searchQuery.toLowerCase());

    if (searchQuery && !matchesSearch && (!node.children || node.children.length === 0)) {
      return null;
    }

    return (
      <div key={node.path} className="select-none">
        <div
          onClick={() => {
            setActiveDirPath(node.path);
            toggleExpand(node);
          }}
          style={{ paddingLeft: `${depth * 14 + 10}px` }}
          className={cn(
            'group flex items-center justify-between pr-2 py-1.5 rounded-lg text-xs transition-all cursor-pointer',
            isSelected
              ? 'bg-primary/15 text-primary font-medium shadow-xs'
              : 'text-muted-foreground hover:bg-muted/60 hover:text-foreground',
          )}
        >
          <div className="flex items-center gap-1.5 min-w-0 flex-1">
            <span
              onClick={(e) => {
                e.stopPropagation();
                toggleExpand(node);
              }}
              className="p-0.5 hover:bg-muted rounded text-muted-foreground/80"
            >
              {isExpanded ? (
                <ChevronDown className="w-3.5 h-3.5 shrink-0" />
              ) : (
                <ChevronRight className="w-3.5 h-3.5 shrink-0" />
              )}
            </span>
            {isExpanded ? (
              <FolderOpen className="w-3.5 h-3.5 text-amber-400 shrink-0" />
            ) : (
              <Folder className="w-3.5 h-3.5 text-amber-500/80 shrink-0" />
            )}
            <span className="truncate text-xs">{node.name}</span>
          </div>

          {/* Quick Enqueue Button */}
          <Button
            variant="ghost"
            size="icon-xs"
            onClick={(e) => handleLoadDir(node.path, e)}
            className="opacity-0 group-hover:opacity-100 hover:bg-primary/20 hover:text-primary transition-opacity h-5 w-5"
            title={`载入目录「${node.name}」中的音频`}
          >
            <FolderPlus className="w-3 h-3" />
          </Button>
        </div>

        {isExpanded && node.children && node.children.length > 0 && (
          <div className="mt-0.5 space-y-0.5">
            {node.children.map((child) => renderDirItem(child, depth + 1))}
          </div>
        )}
      </div>
    );
  };

  return (
    <aside
      className={cn(
        'flex flex-col h-full bg-surface-1 border-r border-border/80 overflow-hidden',
        className,
      )}
    >
      {/* Header: Title & Action */}
      <div className="p-3 border-b border-border flex items-center justify-between shrink-0 bg-surface-2/40">
        <div className="flex items-center gap-2">
          <HardDrive className="w-4 h-4 text-primary" />
          <span className="text-xs font-bold uppercase tracking-wider text-foreground">
            曲库与目录浏览
          </span>
        </div>
        <Button
          variant="ghost"
          size="icon-xs"
          onClick={refreshRoot}
          disabled={loading}
          className="h-6 w-6 text-muted-foreground hover:text-foreground"
          title="刷新目录列表"
        >
          <RefreshCw className={`w-3 h-3 ${loading ? 'animate-spin' : ''}`} />
        </Button>
      </div>

      {/* Smart Collections Section */}
      <div className="p-2 border-b border-border/60 shrink-0 space-y-1">
        <div className="text-[10px] font-semibold text-muted-foreground uppercase px-2 py-0.5">
          状态筛选
        </div>
        <div className="space-y-0.5">
          {filterItems.map((item) => {
            const Icon = item.icon;
            const active = filter === item.id;
            return (
              <button
                key={item.id}
                type="button"
                onClick={() => setFilter(item.id)}
                className={cn(
                  'w-full flex items-center justify-between px-2.5 py-1.5 rounded-lg text-xs transition-colors',
                  active
                    ? 'bg-primary text-primary-foreground font-semibold shadow-xs'
                    : 'text-muted-foreground hover:bg-muted/60 hover:text-foreground',
                )}
              >
                <div className="flex items-center gap-2">
                  <Icon className={cn('w-3.5 h-3.5', active ? 'text-primary-foreground' : item.color)} />
                  <span>{item.label}</span>
                </div>
                <Badge
                  variant={active ? 'secondary' : 'outline'}
                  className={cn(
                    'text-[10px] px-1.5 py-0 h-4 min-w-[20px] justify-center font-mono',
                    active ? 'bg-primary-foreground/20 text-primary-foreground border-transparent' : '',
                  )}
                >
                  {item.count}
                </Badge>
              </button>
            );
          })}
        </div>
      </div>

      {/* Folder Tree Filter Input */}
      <div className="p-2 border-b border-border/60 shrink-0">
        <div className="relative">
          <Search className="w-3.5 h-3.5 text-muted-foreground absolute left-2.5 top-1/2 -translate-y-1/2" />
          <Input
            value={searchQuery}
            onChange={(e) => setSearchQuery(e.target.value)}
            placeholder="搜索目录..."
            className="h-7 text-xs pl-8 pr-2 bg-surface-2"
          />
        </div>
      </div>

      {/* Directory Tree Scroll List */}
      <ScrollArea className="flex-1 p-2">
        <div className="space-y-0.5">
          {/* Root Directory load shortcut */}
          <div
            onClick={(e) => handleLoadDir('', e)}
            className="flex items-center justify-between px-2 py-1.5 rounded-lg text-xs text-muted-foreground hover:bg-muted/60 hover:text-foreground cursor-pointer group"
          >
            <div className="flex items-center gap-1.5">
              <Sparkles className="w-3.5 h-3.5 text-primary" />
              <span className="font-medium text-foreground">全部音乐根目录</span>
            </div>
            <Button
              variant="ghost"
              size="icon-xs"
              className="opacity-0 group-hover:opacity-100 hover:bg-primary/20 hover:text-primary transition-opacity h-5 w-5"
              title="载入全部根目录"
            >
              <FolderPlus className="w-3 h-3" />
            </Button>
          </div>

          {treeRoot.length === 0 && !loading && (
            <div className="py-8 text-center text-xs text-muted-foreground space-y-2">
              <Folder className="w-6 h-6 text-muted-foreground/40 mx-auto" />
              <p>暂未扫描到子目录</p>
              {onOpenDirPicker && (
                <Button variant="outline" size="sm" onClick={onOpenDirPicker} className="text-xs h-7">
                  从目录选择器添加
                </Button>
              )}
            </div>
          )}

          {treeRoot.map((node) => renderDirItem(node))}
        </div>
      </ScrollArea>
    </aside>
  );
}
