import { useState, useEffect } from 'react';
import { Dialog, DialogContent, DialogHeader, DialogTitle } from '@/components/ui/dialog';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Switch } from '@/components/ui/switch';
import { Settings, RefreshCw, Search, FileMusic } from 'lucide-react';
import { useSourceStore } from '@/store/useSourceStore';
import type { SourceInfo } from '@/types';

const DOWNLOAD_PATH_KEY = 'settings.downloadPath';

interface Props {
  open: boolean;
  onOpenChange: (open: boolean) => void;
}

export function SettingsModal({ open, onOpenChange }: Props) {
  // Lazy initializer avoids the `react-hooks/set-state-in-effect` lint rule
  // by reading localStorage exactly once at modal mount. The Dialog is
  // mounted persistently behind a trigger (it does not unmount on close),
  // so a single read is enough. The save path below updates localStorage
  // directly, so the next mount picks up any in-session edits.
  const [downloadPath, setDownloadPath] = useState(() =>
    localStorage.getItem(DOWNLOAD_PATH_KEY) || '/app/media/music/downloads'
  );

  const sources = useSourceStore((s) => s.sources);
  const sourcesLoaded = useSourceStore((s) => s.loaded);
  const toggle = useSourceStore((s) => s.toggle);
  const resetToDefault = useSourceStore((s) => s.resetToDefault);
  const loadSources = useSourceStore((s) => s.loadSources);

  // Trigger hydration if not already done — covers opening Settings
  // without ever visiting the 搜索 tab (where SearchPanel triggers it).
  // `loadSources` is a Zustand action, not a React setState call, so it
  // does not trip react-hooks/set-state-in-effect.
  useEffect(() => {
    if (open && !sourcesLoaded) {
      void loadSources();
    }
  }, [open, sourcesLoaded, loadSources]);

  const handleSave = () => {
    localStorage.setItem(DOWNLOAD_PATH_KEY, downloadPath);
    onOpenChange(false);
  };

  // Group sources by kind for the same "音乐源 / 下载源" split as the picker.
  const tagSources = sources.filter((s) => s.kind === 'tag');
  const downloadSources = sources.filter((s) => s.kind === 'download');

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-md max-h-[85vh] overflow-y-auto">
        <DialogHeader>
          <DialogTitle className="text-sm flex items-center gap-2">
            <Settings className="w-4 h-4" /> 设置
          </DialogTitle>
        </DialogHeader>

        <div className="space-y-5 py-2">
          {/* Default download path — pre-existing, untouched. */}
          <div className="space-y-1.5">
            <Label className="text-xs">默认下载路径</Label>
            <Input
              value={downloadPath}
              onChange={(e) => setDownloadPath(e.target.value)}
              className="h-8 text-sm"
              placeholder="/app/media/music/downloads"
            />
            <p className="text-[11px] text-muted-foreground">搜索结果下载的默认存储路径</p>
          </div>

          {/* Subsonic API Token — pre-existing, untouched. */}
          <div className="border-t border-border pt-3">
            <p className="text-xs text-muted-foreground mb-2">Subsonic API Token</p>
            <p className="text-[11px] text-muted-foreground">
              内部音乐库需要使用 Subsonic API Token 进行认证。你可以在用户设置中找到它。
            </p>
          </div>

          {/* Music / tag sources — Stage A of docs/plugable-plugins.md. */}
          <div className="border-t border-border pt-3">
            <div className="flex items-center justify-between mb-2">
              <p className="text-xs text-muted-foreground">音乐源 / 标签源</p>
              <Button
                variant="ghost"
                size="sm"
                className="h-6 px-2 text-[11px] text-muted-foreground hover:text-foreground"
                onClick={resetToDefault}
                disabled={!sourcesLoaded}
                title="恢复默认（全部启用）"
              >
                <RefreshCw className="w-3 h-3 mr-1" />
                重置
              </Button>
            </div>
            <p className="text-[11px] text-muted-foreground mb-3">
              关闭后该源不会出现在搜索结果与自动刮削中。源列表来自后端插件注册表，新加源只改后端。
            </p>
            {!sourcesLoaded ? (
              <p className="text-[11px] text-muted-foreground italic">加载源中...</p>
            ) : sources.length === 0 ? (
              <p className="text-[11px] text-muted-foreground italic">后端没有可用源</p>
            ) : (
              <div className="space-y-3">
                <SourceGroup
                  title="音乐标签源"
                  icon={<FileMusic className="w-3 h-3" />}
                  sources={tagSources}
                  onToggle={toggle}
                />
                <SourceGroup
                  title="下载源"
                  icon={<Search className="w-3 h-3" />}
                  sources={downloadSources}
                  onToggle={toggle}
                />
              </div>
            )}
          </div>
        </div>

        <div className="flex justify-end pt-2 border-t border-border">
          <Button size="sm" onClick={handleSave}>保存</Button>
        </div>
      </DialogContent>
    </Dialog>
  );
}

function SourceGroup({
  title,
  icon,
  sources,
  onToggle,
}: {
  title: string;
  icon: React.ReactNode;
  sources: SourceInfo[];
  onToggle: (name: string) => void;
}) {
  if (sources.length === 0) return null;
  return (
    <div className="space-y-1.5">
      <div className="text-[11px] text-muted-foreground flex items-center gap-1 px-1">
        {icon}
        {title}
      </div>
      {sources.map((src) => (
        <div
          key={src.name}
          className="flex items-center gap-2 px-1 py-1 rounded hover:bg-muted/30 transition-colors"
        >
          <Switch
            checked={useSourceStore.getState().isEnabled(src.name)}
            onCheckedChange={() => onToggle(src.name)}
          />
          <div className="flex-1 min-w-0">
            <div className="text-sm truncate">{src.display_name}</div>
            <div className="flex items-center gap-1.5 text-[10px] text-muted-foreground">
              {src.searchable && <span>搜索</span>}
              {src.lyric && <span>歌词</span>}
              {src.supports_id3 && <span>ID3</span>}
              <span className="opacity-60">{src.name}</span>
            </div>
          </div>
        </div>
      ))}
    </div>
  );
}

export function SettingsButton() {
  const [open, setOpen] = useState(false);
  return (
    <>
      <Button variant="ghost" size="icon" className="h-7 w-7" onClick={() => setOpen(true)} title="设置">
        <Settings className="w-4 h-4" />
      </Button>
      <SettingsModal open={open} onOpenChange={setOpen} />
    </>
  );
}
