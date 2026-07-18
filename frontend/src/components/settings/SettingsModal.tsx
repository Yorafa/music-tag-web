import { useEffect, useRef, useState } from 'react';
import { Dialog, DialogContent, DialogHeader, DialogTitle } from '@/components/ui/dialog';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Settings } from 'lucide-react';
import { readString, writeString } from '@/utils/persist';
import { PATH_ALIAS, formatDisplayPath, parseDisplayPath } from '@/utils/path';

const DOWNLOAD_PATH_KEY = 'settings.downloadPath';

interface Props {
  open: boolean;
  onOpenChange: (open: boolean) => void;
}

export function SettingsModal({ open, onOpenChange }: Props) {
  // 历史背景：该面板早先同时管「音乐源」与「标签源」（与 SearchPanel 的
  // SourcePickerModal 重复维护同一个 useSourceStore key）和「Subsonic API
  // Token」（Django 时代残留、与现 httpx /api/ 无接线）——这一轮全部移除。
  // 现在只保留下载路径这一项。
  // 本轮进一步：原本依赖 `useEffect([open])` 在 dialog 打开时从
  // localStorage rehydrate（会在 effect 内同步 setState，触发
  // react-hooks/set-state-in-effect 警告），改为父 SettingsButton 用
  // `{open && <SettingsModal />}` 条件挂载，组件 useState 初始化器在
  // 每次 mount 时 fresh 读 localStorage。

  // 状态语义：相对 MUSIC_DIR 的相对路径。`''` 表示 MUSIC_DIR 根（容器内即
  // /app/media），与 useAppStore.filePath 是同一约定。后端任意 /api/ 字段
  // 走 SafeJoin(MUSIC_DIR, root) 安全拼接；前端持久化用相对字面值，显示
  // 加一层 /music/<rel> 包装，让 UI 与文件浏览器 path bar 保持一致。
  const [downloadPath, setDownloadPath] = useState<string>(() =>
    parseDisplayPath(readString(DOWNLOAD_PATH_KEY) || ''),
  );

  // 显示侧本地草稿：用户键入时不立即覆盖 store 的相对值；提交（Enter / 保存
  // / blur）时统一走 parse → 写。结构与 FileBrowser.path bar 一致。
  const [pathBarInput, setPathBarInput] = useState<string>(() =>
    formatDisplayPath(downloadPath),
  );
  const pathBarEditingRef = useRef(false);

  // 不再在 useEffect 里 setState 同步 localStorage：父 SettingsButton
  // 改为 {open && ...} 条件挂载，每次打开都重新挂一个本组件实例，组件的
  // useState 初始化器直接重读 localStorage fresh；这也避免了 React 18 的
  // react-hooks/set-state-in-effect lint 警告。

  // store 变（非用户输入触发）→ display 同步。仅当用户没在编辑才同步，
  // 否则下一次 keystroke 会清掉草稿。
  useEffect(() => {
    if (pathBarEditingRef.current) return;
    const next = formatDisplayPath(downloadPath);
    setPathBarInput((prev) => (prev === next ? prev : next));
  }, [downloadPath]);

  const handleSave = () => {
    pathBarEditingRef.current = false;
    const parsed = parseDisplayPath(pathBarInput);
    setDownloadPath(parsed);
    setPathBarInput(formatDisplayPath(parsed));
    writeString(DOWNLOAD_PATH_KEY, parsed);
    onOpenChange(false);
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-md max-h-[85vh] overflow-y-auto">
        <DialogHeader>
          <DialogTitle className="text-sm flex items-center gap-2">
            <Settings className="w-4 h-4" /> 设置
          </DialogTitle>
        </DialogHeader>

        <div className="space-y-3 py-2">
          <div className="space-y-1.5">
            <Label className="text-xs">默认下载路径</Label>
            <Input
              value={pathBarInput}
              onChange={(e) => {
                pathBarEditingRef.current = true;
                setPathBarInput(e.target.value);
              }}
              onFocus={(e) => {
                pathBarEditingRef.current = true;
                // 选中全部，方便用户输入新值覆盖。
                e.currentTarget.select();
              }}
              onBlur={() => {
                // 失焦即归一化显示 + 落盘：等价输入（'foo/' → 'foo'）不会重复
                // 触发写入，但路径确实改了则跟保存一样写一次。这条隐式自动
                // 保存让用户用 dialog 时不必每次都点「保存」按钮。
                const parsed = parseDisplayPath(pathBarInput);
                setPathBarInput(formatDisplayPath(parsed));
                if (parsed !== downloadPath) {
                  setDownloadPath(parsed);
                  writeString(DOWNLOAD_PATH_KEY, parsed);
                }
                pathBarEditingRef.current = false;
              }}
              onKeyDown={(e) => {
                if (e.key === 'Enter') {
                  handleSave();
                  e.currentTarget.blur();
                } else if (e.key === 'Escape') {
                  // 放弃草稿，恢复到 store 当前值。e.preventDefault() 把
                  // native keydown 标记为 defaultPrevented=true；base-ui
                  // Dialog 的 Esc 关闭是绑在 document.addEventListener 上、
                  // 习惯上会检查 event.defaultPrevented——这会让 dialog 不
                  // 关闭 + 我们走 revert 分支。
                  // 接受"中文/日文 IME candidate Esc 期间 preventDefault
                  // 可能略干扰候选取消"作为代价：下载路径几乎不含 CJK 字
                  // 符，IME 输入中按 Esc 的实际概率很低，maintaining 一致
                  // 的 revert 语义更重要。如果未来需要严格兼容 IME，可以
                  // 在这里加 `if (e.isComposing) return;` 的 guard，但需要
                  // 同时在 DialogContent 上挂 capture-phase keydown listener
                  // 来阻止 BASE-UI 关 dialog（否则 revert guard 反而会让
                  // dialog 仍关——比不大）。
                  pathBarEditingRef.current = false;
                  setPathBarInput(formatDisplayPath(downloadPath));
                  e.preventDefault();
                  e.currentTarget.blur();
                }
              }}
              placeholder={`${PATH_ALIAS}/...`}
              className="h-8 text-sm font-mono"
            />
            <p className="text-[11px] text-muted-foreground">
              搜索结果下载的默认存储路径（相对音乐库根目录{' '}
              <code className="px-1 py-0.5 rounded bg-muted/40 text-[10px] font-mono">
                {PATH_ALIAS}
              </code>
              ）。
            </p>
            <p className="text-[11px] text-muted-foreground">
              输入后失焦自动保存；留空时下载到音乐库根目录。
            </p>
          </div>
        </div>

        <div className="flex justify-end pt-2 border-t border-border">
          <Button size="sm" onClick={handleSave}>保存</Button>
        </div>
      </DialogContent>
    </Dialog>
  );
}

export function SettingsButton() {
  // mount-on-open：每次打开 dialog 都重新挂载一个 SettingsModal 实例，让
  // useState 初始化器重新从 localStorage 读最新值——避免在 useEffect 内
  // setState 触发 cascading renders（react-hooks/set-state-in-effect）。
  // 这是 React 官方推荐的「派生状态靠重新挂载」模式，且组件本身仍然是
  // 受控的（open 由父 owner、close 仍然走 onOpenChange）。
  const [open, setOpen] = useState(false);
  return (
    <>
      <Button
        variant="ghost"
        size="icon"
        className="h-7 w-7"
        onClick={() => setOpen(true)}
        title="设置"
      >
        <Settings className="w-4 h-4" />
      </Button>
      {open && <SettingsModal open={open} onOpenChange={setOpen} />}
    </>
  );
}
