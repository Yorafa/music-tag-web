// Top-level page after login. Mounts the unified AppShell layout
// with modern sidebar, header, workspaces, and persistent player.

import { useEffect } from 'react';
import { AppShell } from '@/components/layout/AppShell';
import { useBrowserStore, browserActions } from '@/store/useBrowserStore';
import { editorActions } from '@/store/useEditorStore';
import { getFileList } from '@/api/client';
import { resolveBrowsePath } from '@/utils/path';

export function HomePage() {
  const loadFiles = async (path?: string) => {
    const p = resolveBrowsePath(path, useBrowserStore.getState().filePath);
    editorActions.setFadeShowDetail(false);
    try {
      const res = await getFileList(p);
      if (res.result) {
        browserActions.setTreeData(res.data);
      }
    } catch {
      // silently fail (401 will auto-logout)
    }
  };

  useEffect(() => {
    void loadFiles();
  }, []);

  return <AppShell />;
}

