import { useState } from 'react';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogFooter } from '@/components/ui/dialog';
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip';
import {
  ChevronsLeft,
  ChevronsRight,
  Search,
  RefreshCw,
  FolderTree,
  Trash2,
  AlertTriangle,
} from 'lucide-react';
import {
  scanFolder,
  fullScanFolder,
  clearCelery,
  tidyFolder,
} from '@/api/client';
import { useAppStore } from '@/store/useAppStore';
import type { ComponentType } from 'react';

interface ToolDef {
  id: string;
  label: string;
  Icon: ComponentType<{ className?: string }>;
  /** 'none' = direct call; otherwise an inline dialog is opened first. */
  dialog: 'tidy' | 'confirm-clear' | 'none';
}

const TOOLS: ToolDef[] = [
  { id: 'scan-full', label: '全盘扫描', Icon: Search, dialog: 'none' },
  { id: 'scan', label: '增量扫描', Icon: RefreshCw, dialog: 'none' },
  { id: 'tidy', label: '整理文件夹', Icon: FolderTree, dialog: 'tidy' },
  { id: 'clear', label: '清空任务队列', Icon: Trash2, dialog: 'confirm-clear' },
];

const COLLAPSED_WIDTH = 48;
const EXPANDED_WIDTH = 180;

interface Props {
  collapsed: boolean;
  onToggle: () => void;
}

export function Toolbar({ collapsed, onToggle }: Props) {
  // Slice selectors — Toolbar only re-renders if these specific fields change.
  const filePath = useAppStore((s) => s.filePath);
  const [activeDialog, setActiveDialog] = useState<ToolDef['dialog']>('none');
  const [running, setRunning] = useState<string | null>(null);
  const [tidyForm, setTidyForm] = useState({
    root_path: '',
    first_dir: 'artist',
    second_dir: '',
  });

  const width = collapsed ? COLLAPSED_WIDTH : EXPANDED_WIDTH;

  const runTool = async (tool: ToolDef) => {
    if (tool.dialog !== 'none') {
      // Pre-fill tidy root_path from current filePath.
      if (tool.dialog === 'tidy' && !tidyForm.root_path) {
        setTidyForm((f) => ({ ...f, root_path: filePath }));
      }
      setActiveDialog(tool.dialog);
      return;
    }
    setRunning(tool.id);
    try {
      if (tool.id === 'scan-full') await fullScanFolder();
      else if (tool.id === 'scan') await scanFolder();
    } catch {
      /* ignore — could surface via toast */
    } finally {
      setRunning(null);
    }
  };

  const handleTidySubmit = async () => {
    setRunning('tidy');
    // filePath ends with "/"; last non-empty segment is the current folder name.
    const folderName = filePath.split('/').filter(Boolean).pop() || 'root';
    try {
      await tidyFolder({
        root_path: tidyForm.root_path,
        first_dir: tidyForm.first_dir,
        second_dir: tidyForm.second_dir,
        file_full_path: filePath,
        select_data: [{ id: 0, name: folderName, icon: 'icon-folder' }],
      });
      setActiveDialog('none');
    } catch {
      /* ignore */
    } finally {
      setRunning(null);
    }
  };

  const handleClearConfirm = async () => {
    setRunning('clear');
    try {
      await clearCelery();
      setActiveDialog('none');
    } catch {
      /* ignore */
    } finally {
      setRunning(null);
    }
  };

  return (
    <>
      <aside
        style={{ width }}
        className="shrink-0 border-r border-border bg-card/40 flex flex-col items-stretch py-2 select-none transition-[width] duration-150"
        aria-label="音乐库工具栏"
      >
        <Tooltip>
          <TooltipTrigger>
            <button
              onClick={onToggle}
              className="mx-auto mb-2 inline-flex items-center justify-center w-8 h-8 rounded-md hover:bg-accent text-muted-foreground hover:text-foreground"
              aria-label={collapsed ? '展开工具栏' : '合并工具栏'}
            >
              {collapsed ? <ChevronsRight className="w-4 h-4" /> : <ChevronsLeft className="w-4 h-4" />}
            </button>
          </TooltipTrigger>
          <TooltipContent side="right">
            {collapsed ? '展开工具栏' : '合并工具栏'}
          </TooltipContent>
        </Tooltip>

        <div className="border-t border-border" />

        <div className="flex flex-col gap-1 mt-2 px-1">
          {TOOLS.map((tool) => {
            const Icon = tool.Icon;
            const isRunning = running === tool.id;
            const button = (
              <button
                onClick={() => runTool(tool)}
                disabled={isRunning}
                aria-label={tool.label}
                className={`w-full flex items-center gap-2 px-2 py-2 rounded-md hover:bg-accent text-muted-foreground hover:text-foreground transition-colors text-sm ${
                  collapsed ? 'justify-center' : ''
                } ${isRunning ? 'opacity-50 cursor-wait' : ''}`}
              >
                <Icon className={`${collapsed ? 'w-5 h-5' : 'w-4 h-4'} shrink-0`} />
                {!collapsed && <span className="truncate">{tool.label}</span>}
              </button>
            );
            return (
              <Tooltip key={tool.id}>
                <TooltipTrigger>{button}</TooltipTrigger>
                {collapsed && <TooltipContent side="right">{tool.label}</TooltipContent>}
              </Tooltip>
            );
          })}
        </div>
      </aside>

      {/* Tidy folder dialog */}
      <Dialog
        open={activeDialog === 'tidy'}
        onOpenChange={(open) => setActiveDialog(open ? 'tidy' : 'none')}
      >
        <DialogContent className="max-w-md">
          <DialogHeader>
            <DialogTitle>整理文件夹</DialogTitle>
          </DialogHeader>
          <div className="space-y-3 py-2">
            <div>
              <Label className="text-xs mb-1 block">整理后的根目录</Label>
              <Input
                value={tidyForm.root_path}
                onChange={(e) => setTidyForm({ ...tidyForm, root_path: e.target.value })}
                placeholder="/app/media/"
              />
            </div>
            <div>
              <Label className="text-xs mb-1 block">一级目录</Label>
              <Input
                value={tidyForm.first_dir}
                onChange={(e) => setTidyForm({ ...tidyForm, first_dir: e.target.value })}
              />
            </div>
            <div>
              <Label className="text-xs mb-1 block">二级目录（可选）</Label>
              <Input
                value={tidyForm.second_dir}
                onChange={(e) => setTidyForm({ ...tidyForm, second_dir: e.target.value })}
              />
            </div>
          </div>
          <DialogFooter showCloseButton>
            <Button onClick={handleTidySubmit} disabled={running === 'tidy'}>
              {running === 'tidy' ? '提交中…' : '提交'}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* Confirm-clear celery dialog */}
      <Dialog
        open={activeDialog === 'confirm-clear'}
        onOpenChange={(open) => setActiveDialog(open ? 'confirm-clear' : 'none')}
      >
        <DialogContent className="max-w-sm">
          <DialogHeader>
            <DialogTitle className="flex items-center gap-2 text-amber-500">
              <AlertTriangle className="w-4 h-4" />
              清空任务队列
            </DialogTitle>
          </DialogHeader>
          <p className="text-sm text-muted-foreground">
            确认要终止所有正在运行的扫描 / 刮削任务吗？此操作不可撤销。
          </p>
          <DialogFooter showCloseButton>
            <Button variant="destructive" onClick={handleClearConfirm} disabled={running === 'clear'}>
              {running === 'clear' ? '处理中…' : '确认清空'}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </>
  );
}
