import { useState } from 'react';
import { useAppStore } from '@/store/useAppStore';
import { getMusicId3 } from '@/api/client';
import { Input } from '@/components/ui/input';
import { ScrollArea } from '@/components/ui/scroll-area';
import { Badge } from '@/components/ui/badge';
import { ChevronRight, ChevronDown, Folder, FileAudio, FileText, ArrowLeft, Home } from 'lucide-react';
import type { FileNode } from '@/types';

interface Props {
  onLoadFiles: (path?: string) => void;
}

const ALLOWED_TYPES = ['flac', 'mp3', 'ape', 'wav', 'aiff', 'wv', 'tta', 'm4a', 'ogg', 'mpc', 'opus', 'wma', 'dsf', 'dff'];

export function FileBrowser({ onLoadFiles }: Props) {
  const {
    filePath, setFilePath, treeData, setSelectedFile, setFullPath, setMusicInfo,
    selectedFile, checkedIds, setCheckedIds, setFadeShowDetail, setSongList, setReloadImg,
  } = useAppStore();

  const [searchWord, setSearchWord] = useState('');
  const [expandedDirs, setExpandedDirs] = useState<Set<string>>(new Set());

  const root = treeData[0];
  const children = root?.children || [];
  const dirs = children.filter(c => c.icon === 'icon-folder');
  const files = children.filter(c => c.icon !== 'icon-folder');

  const filteredFiles = searchWord
    ? files.filter(f => f.name.toLowerCase().includes(searchWord.toLowerCase()))
    : files;

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
        setReloadImg(true);
      }
    } catch {
      // ignore
    }
    setFadeShowDetail(false);
    setSongList([]);
  };

  const handleCheck = (nodeId: number, checked: boolean) => {
    const file = files.find(f => f.id === nodeId);
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
      {/* Path bar */}
      <div className="p-3 border-b border-border">
        <div className="flex items-center gap-1 mb-2 text-xs flex-wrap">
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
        <Input
          value={searchWord}
          onChange={e => setSearchWord(e.target.value)}
          placeholder="搜索文件..."
          className="h-7 text-xs"
        />
      </div>

      {/* Content */}
      <ScrollArea className="flex-1">
        <div className="p-2">
          {/* Directories */}
          {dirs.map(dir => (
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
          {dirs.length > 0 && files.length > 0 && (
            <div className="border-t border-border my-2" />
          )}

          {/* Files */}
          {filteredFiles.map(file => {
            const ext = file.name.split('.').pop()?.toLowerCase() || '';
            const isAudio = ALLOWED_TYPES.includes(ext);
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

          {searchWord && filteredFiles.length === 0 && (
            <div className="text-center py-8 text-xs text-muted-foreground">
              未找到匹配的文件
            </div>
          )}
        </div>
      </ScrollArea>
    </div>
  );
}
