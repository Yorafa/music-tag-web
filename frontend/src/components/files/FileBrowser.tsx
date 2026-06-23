import { useEffect, useMemo, useRef, useState } from 'react';
import { useAppStore } from '@/store/useAppStore';
import { getMusicId3 } from '@/api/client';
import { Input } from '@/components/ui/input';
import { ScrollArea } from '@/components/ui/scroll-area';
import { Badge } from '@/components/ui/badge';
import {
  ChevronRight,
  ChevronDown,
  Folder,
  FileAudio,
  FileText,
  ArrowLeft,
  Home,
  ArrowUpDown,
  ArrowUp,
  ArrowDown,
  RefreshCw,
} from 'lucide-react';
import type { FileNode, SortField } from '@/types';
import { readString, writeString } from '@/utils/persist';
import { makeCompareFn } from '@/utils/sortBy';
import { isAudioFile } from '@/utils/audioTypes';

interface Props {
  onLoadFiles: (path?: string) => void;
}

const SORT_OPTIONS: { field: SortField; label: string }[] = [
  { field: 'name', label: '名称' },
  { field: 'size', label: '大小' },
  { field: 'update_time', label: '修改时间' },
];
const PATH_STORAGE_KEY = 'fileBrowser.lastPath';

export function FileBrowser({ onLoadFiles }: Props) {
  const {
    filePath, setFilePath, treeData, setSelectedFile, setFullPath, setMusicInfo,
    selectedFile, checkedIds, setCheckedIds, setFadeShowDetail, setSongList,
    setEditorOpen,
  } = useAppStore();
  const sortField = useAppStore((s) => s.sortField);
  const sortDir = useAppStore((s) => s.sortDir);
  const setSort = useAppStore((s) => s.setSort);
  const setSortDir = useAppStore((s) => s.setSortDir);

  const [searchWord, setSearchWord] = useState('');
  const [expandedDirs, setExpandedDirs] = useState<Set<string>>(new Set());
  const [sortOpen, setSortOpen] = useState(false);
  const [refreshing, setRefreshing] = useState(false);
  const sortRef = useRef<HTMLDivElement>(null);

  const root = treeData[0];
  const children = root?.children || [];

  // Shared comparator (from utils/sortBy.ts) — also consumed by SearchResults
  // so toggling the dropdown reorders both panes in lockstep.
  const compareFn = useMemo(
    () => makeCompareFn<FileNode>(sortField, sortDir),
    [sortField, sortDir],
  );

  // Derive both lists INSIDE the memo so identity is stable when children
  // doesn't change. Otherwise the Inline-derived `rawDirs` array would be a
  // fresh reference each render and defeat the memo.
  const sortedDirs = useMemo(() => {
    const rawDirs = children.filter(c => c.icon === 'icon-folder');
    return [...rawDirs].sort(compareFn);
  }, [children, compareFn]);

  const hasDirs = children.some(c => c.icon === 'icon-folder');

  const sortedFiles = useMemo(() => {
    const files = children.filter(c => c.icon !== 'icon-folder');
    const needle = searchWord.toLowerCase();
    const base = needle
      ? files.filter(f => f.name.toLowerCase().includes(needle))
      : files;
    return [...base].sort(compareFn);
  }, [children, searchWord, compareFn]);

  const handleSortClick = (field: SortField) => {
    if (sortField === field) {
      // Same field: toggle direction. setSortDir writes localStorage inside its setter.
      setSortDir((d) => (d === 'asc' ? 'desc' : 'asc'));
    } else {
      // Switching to a different field — collapse into a single batched write
      // so we flush localStorage once instead of twice (setSortField + setSortDir).
      setSort(field, 'desc');
    }
    setSortOpen(false);
  };

  // Re-fetch the current directory. `onLoadFiles` is already async (backed by
  // HomePage.loadFiles → getFileList), so a plain await tracks the network
  // round-trip naturally. Guard re-entry with `refreshing` so rapid
  // double-clicks don't pile up in-flight requests.
  const handleRefresh = async () => {
    if (refreshing) return;
    setRefreshing(true);
    try {
      await onLoadFiles(filePath);
    } finally {
      setRefreshing(false);
    }
  };

  // Drop click-outside listener while the dropdown is open.
  useEffect(() => {
    if (!sortOpen) return;
    const handler = (e: MouseEvent) => {
      if (sortRef.current && !sortRef.current.contains(e.target as Node)) {
        setSortOpen(false);
      }
    };
    document.addEventListener('mousedown', handler);
    return () => document.removeEventListener('mousedown', handler);
  }, [sortOpen]);

  // Persist the current directory. On first mount, also load the file list so
  // the user lands back in the directory they last viewed.
  const hasHydratedPathRef = useRef(false);
  useEffect(() => {
    if (!hasHydratedPathRef.current) {
      hasHydratedPathRef.current = true;
      const savedPath = readString(PATH_STORAGE_KEY);
      if (savedPath && savedPath !== filePath) {
        setFilePath(savedPath);
        onLoadFiles(savedPath);
        // Skip the writeString on this run — filePath is still the initial
        // value in this closure. The next render will write the correct path.
        return;
      }
    }
    writeString(PATH_STORAGE_KEY, filePath);
  }, [filePath, setFilePath, onLoadFiles]);

  const handleBack = () => {
    const parts = filePath.replace(/\/$/, '').split('/');
    parts.pop();
    const parent = parts.join('/') || '/';
    setFilePath(parent);
    onLoadFiles(parent);
  };

  const breadcrumbParts = filePath.replace(/\/$/, '').split('/').filter(Boolean);

  const handleBreadcrumbClick = (index: number) => {
    if (breadcrumbParts.length === 0) return;
    const prefix =
      index < 0
        ? '/'
        : '/' + breadcrumbParts.slice(0, index + 1).join('/');
    setFilePath(prefix + '/');
    onLoadFiles(prefix);
  };

  const handleDirClick = (dir: FileNode) => {
    const newPath = `${filePath.replace(/\/$/, '')}/${dir.name}`;
    setFilePath(newPath);
    onLoadFiles(newPath);
  };

  const handleFileClick = async (file: FileNode) => {
    setSelectedFile(file.name);
    const full = `${filePath.replace(/\/$/, '')}/${file.name}`;
    setFullPath(full);
    try {
      const res = await getMusicId3(filePath, file.name);
      if (res.result) {
        setMusicInfo({
          ...res.data,
          is_save_lyrics_file: false,
          is_save_album_cover: false,
        });
      }
    } catch {
      // ignore
    }
    setFadeShowDetail(false);
    setSongList([]);
    // Open the floating TagEditor as a modal so the user can edit the file.
    setEditorOpen(true);
  };

  const handleCheck = (nodeId: number, checked: boolean) => {
    const file = sortedFiles.find(f => f.id === nodeId);
    if (!file) return;
    if (checked) {
      setCheckedIds([...checkedIds, nodeId]);
    } else {
      setCheckedIds(checkedIds.filter(id => id !== nodeId));
    }
  };

  const stateColors: Record<string, string> = {
    success: 'bg-emerald-500/10 text-emerald-400 border-emerald-500/30',
    failed: 'bg-red-500/10 text-red-400 border-red-500/30',
    null: 'bg-zinc-500/10 text-zinc-400 border-zinc-500/30',
  };

  return (
    <div className="flex flex-col h-full">
      {/* Path bar — `shrink-0` so flex pressure from the ScrollArea below can't squeeze it. */}
      <div className="shrink-0 p-3 border-b border-border">
        <div className="flex items-start gap-1 mb-3 text-xs flex-wrap max-h-40 overflow-y-auto pr-1">
          <button
            onClick={() => handleBreadcrumbClick(-1)}
            title="返回根目录"
            className="inline-flex items-center gap-1 px-1.5 py-0.5 rounded hover:bg-accent text-muted-foreground hover:text-foreground transition-colors"
          >
            <Home className="w-3 h-3" />
            <span>根目录</span>
          </button>
          {breadcrumbParts.map((part, index) => {
            const isLast = index === breadcrumbParts.length - 1;
            return (
              <div key={`${part}-${index}`} className="flex items-center gap-1 min-w-0">
                <ChevronRight className="w-3 h-3 text-muted-foreground/50 shrink-0" />
                {isLast ? (
                  <span className="font-medium text-foreground px-1.5 py-0.5 truncate max-w-[160px]" title={part}>
                    {part}
                  </span>
                ) : (
                  <button
                    onClick={() => handleBreadcrumbClick(index)}
                    title={`跳转到 ${part}`}
                    className="px-1.5 py-0.5 rounded hover:bg-accent text-muted-foreground hover:text-foreground transition-colors truncate max-w-[160px]"
                  >
                    {part}
                  </button>
                )}
              </div>
            );
          })}
          {breadcrumbParts.length === 0 && (
            <span className="text-muted-foreground px-1.5 py-0.5">/</span>
          )}
        </div>
        <div className="flex items-center gap-2 mb-2">
          <button onClick={handleBack} className="p-1 hover:bg-accent rounded transition-colors">
            <ArrowLeft className="w-4 h-4 text-muted-foreground" />
          </button>
          <Input
            value={filePath}
            onChange={e => setFilePath(e.target.value)}
            onKeyDown={e => e.key === 'Enter' && onLoadFiles()}
            placeholder="请输入文件夹路径"
            className="h-7 text-xs font-mono"
          />
        </div>
        <div className="flex items-center gap-2">
          <Input
            value={searchWord}
            onChange={e => setSearchWord(e.target.value)}
            placeholder="搜索文件..."
            className="h-7 text-xs flex-1"
          />
          <div className="relative" ref={sortRef}>
            <button
              type="button"
              onClick={() => setSortOpen(o => !o)}
              title="排序"
              className="inline-flex items-center gap-1 h-7 px-2 rounded-md border border-border bg-background hover:bg-accent transition-colors text-xs text-foreground"
            >
              <ArrowUpDown className="w-3.5 h-3.5" />
              <span>
                {sortField === 'name' ? '名称' : sortField === 'size' ? '大小' : '修改时间'}
              </span>
              {sortDir === 'asc' ? (
                <ArrowUp className="w-3 h-3" />
              ) : (
                <ArrowDown className="w-3 h-3" />
              )}
              <ChevronDown className="w-3 h-3 text-muted-foreground" />
            </button>
            {sortOpen && (
              <div className="absolute right-0 top-full mt-1 z-[60] min-w-[160px] rounded-md border border-border bg-popover shadow-lg py-1 text-xs">
                {SORT_OPTIONS.map(opt => {
                  const isActive = sortField === opt.field;
                  return (
                    <button
                      key={opt.field}
                      type="button"
                      onClick={() => handleSortClick(opt.field)}
                      className={`w-full flex items-center justify-between px-3 py-1.5 hover:bg-accent transition-colors ${
                        isActive ? 'text-primary font-medium' : 'text-foreground'
                      }`}
                    >
                      <span>{opt.label}</span>
                      {isActive && (
                        sortDir === 'asc' ? (
                          <ArrowUp className="w-3 h-3 text-primary" />
                        ) : (
                          <ArrowDown className="w-3 h-3 text-primary" />
                        )
                      )}
                    </button>
                  );
                })}
              </div>
            )}
          </div>
          <button
            type="button"
            onClick={handleRefresh}
            disabled={refreshing}
            title="刷新当前目录"
            aria-label="刷新当前目录"
            className={`inline-flex items-center justify-center h-7 px-2 rounded-md border border-border bg-background hover:bg-accent transition-colors text-foreground ${
              refreshing ? 'opacity-60 cursor-wait' : ''
            }`}
          >
            <RefreshCw className={`w-3.5 h-3.5 ${refreshing ? 'animate-spin' : ''}`} />
          </button>
        </div>
      </div>

      {/* File list. `min-h-0` is the key fix: without it, the flex child's
          intrinsic content height overrides the parent height so the ScrollArea
          grows unbounded and the inner overflow has nothing to clip — wheel
          events scroll the page instead. */}
      <ScrollArea className="flex-1 min-h-0">
        <div className="p-2">
          {/* Directories */}
          {sortedDirs.map(dir => (
            <div
              key={dir.id}
              onClick={() => handleDirClick(dir)}
              className="flex items-center gap-2 px-2 py-1.5 rounded-md cursor-pointer hover:bg-accent transition-colors text-sm"
            >
              <Folder className="w-4 h-4 text-amber-400 shrink-0" />
              <span className="truncate">{dir.name}</span>
            </div>
          ))}

          {/* Divider */}
          {hasDirs && sortedFiles.length > 0 && (
            <div className="border-t border-border my-2" />
          )}

          {/* Files */}
          {sortedFiles.map(file => {
            const isAudio = isAudioFile(file.name);
            const isChecked = checkedIds.includes(file.id);
            const isSelected = selectedFile === file.name;

            return (
              <div
                key={file.id}
                onClick={() => handleFileClick(file)}
                className={`flex items-center gap-2 px-2 py-1.5 rounded-md cursor-pointer transition-colors text-sm group ${
                  isSelected ? 'bg-primary/10 text-primary-foreground' : 'hover:bg-accent'
                }`}
              >
                {/* Checkbox */}
                <input
                  type="checkbox"
                  checked={isChecked}
                  onChange={e => {
                    e.stopPropagation();
                    handleCheck(file.id, e.target.checked);
                  }}
                  className="w-3.5 h-3.5 rounded border-border accent-primary shrink-0"
                />
                {/* Icon */}
                {file.icon === 'icon-script-files' ? (
                  <FileText className="w-4 h-4 text-blue-400 shrink-0" />
                ) : (
                  <FileAudio className="w-4 h-4 text-purple-400 shrink-0" />
                )}
                {/* Name */}
                <span className="truncate flex-1 text-xs">{file.name}</span>
                {/* State badge */}
                {file.state && file.state !== 'null' && (
                  <Badge variant="outline" className={`text-[10px] px-1.5 py-0 h-4 ${stateColors[file.state] || ''}`}>
                    {file.state === 'success' ? '✓' : '✗'}
                  </Badge>
                )}
              </div>
            );
          })}

          {searchWord && sortedFiles.length === 0 && (
            <div className="text-center py-8 text-xs text-muted-foreground">
              未找到匹配的文件
            </div>
          )}
        </div>
      </ScrollArea>
    </div>
  );
}
