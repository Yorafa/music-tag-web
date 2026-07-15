import { useEffect } from 'react';
import { AppShell } from '@/components/layout/AppShell';
import { ThemeToggle } from '@/components/ThemeToggle';
import { SettingsButton } from '@/components/settings/SettingsModal';
import { useAppStore } from '@/store/useAppStore';
import { useAuthStore } from '@/store/useAuthStore';
import { getFileList } from '@/api/client';
import { LogOut } from 'lucide-react';

export function HomePage() {
  const { filePath, setTreeData, setFadeShowDetail, checkedIds } = useAppStore();
  const logout = useAuthStore((s) => s.logout);

  const loadFiles = async (path?: string) => {
    const p = path || filePath;
    setFadeShowDetail(false);
    try {
      const res = await getFileList(p);
      if (res.result) {
        setTreeData(res.data);
      }
    } catch {
      // silently fail (401 will auto-logout)
    }
  };

  useEffect(() => {
    loadFiles();
  }, []);

  return (
    <div className="h-screen flex flex-col bg-background text-foreground">
      <header className="h-12 border-b border-border flex items-center px-4 shrink-0 bg-card gap-3">
        <h1 className="text-sm font-semibold tracking-tight">
          🎵 音乐标签 Web 版
        </h1>
        <span className="text-xs text-muted-foreground">
          {checkedIds.length > 0 ? `已选 ${checkedIds.length} 个文件` : ''}
        </span>
        <div className="ml-auto flex items-center gap-1">
          <SettingsButton />
          <ThemeToggle />
          <button
            type="button"
            onClick={logout}
            title="退出登录"
            aria-label="退出登录"
            className="inline-flex items-center justify-center w-9 h-9 rounded-md hover:bg-accent text-muted-foreground hover:text-foreground transition-colors"
          >
            <LogOut className="w-4 h-4" />
          </button>
        </div>
      </header>
      <AppShell onLoadFiles={loadFiles} />
    </div>
  );
}
