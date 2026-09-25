import { useRef, useState } from 'react';
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogFooter,
} from '@/components/ui/dialog';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import {
  Tabs,
  TabsList,
  TabsTrigger,
  TabsContent,
} from '@/components/ui/tabs';
import { Settings } from 'lucide-react';
import { readString, writeString } from '@/utils/persist';
import { PATH_ALIAS, formatDisplayPath, parseDisplayPath } from '@/utils/path';
import { SourcesTabContent } from './SourcesTabContent';

const DOWNLOAD_PATH_KEY = 'settings.downloadPath';

interface Props {
  open: boolean;
  onOpenChange: (open: boolean) => void;
}

export function SettingsModal({ open, onOpenChange }: Props) {
  // 历史背景：该面板早先同时管「音乐源」与「标签源」（与 SearchPanel 的
  // SourcePickerModal 重复维护同一个 useSourceStore key）和「Subsonic API
  // Token」（Django 时代残留、与现 httpx /api/ 无接线）——这一轮全部移除。
  // 本轮（C.4 / Stage B UI）进一步：把现有的「下载路径」作为「常规」tab，
  // 与新加的「音乐源」tab 并列；选择 shadcn Tabs 作为容器，因为
  // components/ui/tabs.tsx 已 ship（drop-in）。
  //
  // 本轮再次进一步：原本依赖 `useEffect([open])` 在 dialog 打开时从
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
  // downloadPath 在本组件内部只有 handleSave / onBlur / onChange 三处
  // 写入，三处都同时调用 setPathBarInput(formatDisplayPath(parsed))，所以
  // 不需要再有一个 `useEffect(() => setPathBarInput(...), [downloadPath])`
  // 来同步——保留会触发 set-state-in-effect lint。

  const handleSave = () => {
    pathBarEditingRef.current = false;
    const parsed = parseDisplayPath(pathBarInput);
    setDownloadPath(parsed);
    setPathBarInput(formatDisplayPath(parsed));
    writeString(DOWNLOAD_PATH_KEY, parsed);
    onOpenChange(false);
  };

  // Tabs default = "general" — the existing 下载路径 form lives there.
  // Re-mount trick (heavy content's already in SourcesTab's effect): when
  // the dialog opens, the modal mounts fresh; Tabs' defaultValue kicks
  // the user onto the «通用» tab. Switching to «音乐源» is a panel
  // swap, not a remount, so any in-flight reload state survives.
  const [activeTab, setActiveTab] = useState('general');

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-2xl max-h-[85vh] overflow-y-auto">
        <DialogHeader>
          <DialogTitle className="text-sm flex items-center gap-2">
            <Settings className="w-4 h-4" /> 设置
          </DialogTitle>
        </DialogHeader>

        <Tabs value={activeTab} onValueChange={setActiveTab} orientation="horizontal">
          <TabsList>
            <TabsTrigger value="general">通用</TabsTrigger>
            <TabsTrigger value="sources">音乐源</TabsTrigger>
          </TabsList>

          <TabsContent value="general" className="space-y-3 py-2">
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
          </TabsContent>

          <TabsContent value="sources" className="py-2">
            <SourcesTabContent />
          </TabsContent>
        </Tabs>

        {/* Save button only applies to the 通用 form. 音乐源 is a
            read-only registry view, so Save is hidden there rather
            than acting on state it cannot change. */}
        {activeTab === 'general' ? (
          <DialogFooter showCloseButton className="border-t border-border pt-2">
            <Button size="sm" onClick={handleSave}>保存</Button>
          </DialogFooter>
        ) : null}
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
