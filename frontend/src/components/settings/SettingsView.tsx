import { useState, useRef } from 'react';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Separator } from '@/components/ui/separator';
import {
  Tabs,
  TabsList,
  TabsTrigger,
  TabsContent,
} from '@/components/ui/tabs';
import { readString, writeString } from '@/utils/persist';
import { PATH_ALIAS, formatDisplayPath, parseDisplayPath } from '@/utils/path';
import { useNoticeStore } from '@/store/useNoticeStore';
import { SourcesTabContent } from './SourcesTabContent';
import { AudioCacheSection } from './AudioCacheSection';

const DOWNLOAD_PATH_KEY = 'settings.downloadPath';

export function SettingsView() {
  const [downloadPath, setDownloadPath] = useState<string>(() =>
    parseDisplayPath(readString(DOWNLOAD_PATH_KEY) || ''),
  );
  const [pathBarInput, setPathBarInput] = useState<string>(() =>
    formatDisplayPath(downloadPath),
  );
  const pathBarEditingRef = useRef(false);

  const handleSave = () => {
    pathBarEditingRef.current = false;
    const parsed = parseDisplayPath(pathBarInput);
    setDownloadPath(parsed);
    setPathBarInput(formatDisplayPath(parsed));
    writeString(DOWNLOAD_PATH_KEY, parsed);
    useNoticeStore.getState().push('下载路径设置已保存', 'info');
  };

  return (
    <div className="flex-1 flex flex-col overflow-hidden bg-surface-1 min-h-0 p-4">
      <div className="max-w-4xl w-full mx-auto flex-1 flex flex-col min-h-0 bg-surface-2/60 border border-border/70 rounded-2xl p-4 md:p-6 shadow-sm overflow-y-auto">
        <Tabs defaultValue="general" orientation="horizontal" className="space-y-4">
          <TabsList className="bg-muted/60 p-1 rounded-xl">
            <TabsTrigger value="general" className="rounded-lg text-xs">通用设置</TabsTrigger>
            <TabsTrigger value="sources" className="rounded-lg text-xs">音乐源管理</TabsTrigger>
          </TabsList>

          {/* General Tab */}
          <TabsContent value="general" className="space-y-4 py-2">
            <div className="space-y-2 max-w-lg">
              <Label className="text-xs font-semibold">默认下载与存储路径</Label>
              <div className="flex gap-2">
                <Input
                  value={pathBarInput}
                  onChange={(e) => {
                    pathBarEditingRef.current = true;
                    setPathBarInput(e.target.value);
                  }}
                  onBlur={() => {
                    const parsed = parseDisplayPath(pathBarInput);
                    setPathBarInput(formatDisplayPath(parsed));
                    if (parsed !== downloadPath) {
                      setDownloadPath(parsed);
                      writeString(DOWNLOAD_PATH_KEY, parsed);
                    }
                    pathBarEditingRef.current = false;
                  }}
                  onKeyDown={(e) => {
                    if (e.key === 'Enter') handleSave();
                  }}
                  placeholder={`${PATH_ALIAS}/...`}
                  className="h-9 text-xs font-mono bg-background"
                />
                <Button size="sm" onClick={handleSave} className="h-9 px-4 text-xs shrink-0">
                  保存
                </Button>
              </div>
              <p className="text-[11px] text-muted-foreground">
                云端搜索下载的默认存储路径（相对音乐库根目录{' '}
                <code className="px-1 py-0.5 rounded bg-muted/50 text-[10px] font-mono">
                  {PATH_ALIAS}
                </code>
                ）。留空时默认下载到音乐库根目录。
              </p>
            </div>

            <Separator />

            <AudioCacheSection />
          </TabsContent>

          {/* Sources Tab */}
          <TabsContent value="sources" className="py-2">
            <SourcesTabContent />
          </TabsContent>
        </Tabs>
      </div>
    </div>
  );
}
