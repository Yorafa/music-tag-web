import { useCallback, useEffect, useState } from 'react';
import { HardDrive, Loader2, RefreshCw, Trash2 } from 'lucide-react';

import { Button } from '@/components/ui/button';
import { Label } from '@/components/ui/label';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import {
  clearAudioCache,
  getAudioCache,
  type AudioCacheUsage,
} from '@/api/client';
import { useNoticeStore } from '@/store/useNoticeStore';
import { clearSummary, guardNote, sourceRows, usageLine } from './audioCacheText';

/** The download cache (AUDIO_CACHE_DIR): what the worker keeps on disk for
 *  playback and for "already downloaded, skip the fetch", and the two ways
 *  to get rid of it.
 *
 *  Fetched on mount, which is the same rule the task centre follows after
 *  its own fix — this section only exists on a page the user navigated to,
 *  so the request is a consequence of looking rather than of existing. */
export function AudioCacheSection() {
  const [usage, setUsage] = useState<AudioCacheUsage | null>(null);
  const [loading, setLoading] = useState(true);
  const [clearing, setClearing] = useState(false);
  const [confirming, setConfirming] = useState(false);
  /** null until a clear has happened, so the result line is absent rather
   *  than claiming something before anything was done. */
  const [result, setResult] = useState<string | null>(null);

  // Split so the mount effect never calls a function that setStates
  // synchronously — that is a cascading render, and this page does not
  // need one. `apply`/`fail` are the only writers, and both call sites
  // reach them through a promise callback.
  const apply = useCallback((u: AudioCacheUsage) => {
    setUsage(u);
    setLoading(false);
  }, []);
  const fail = useCallback((e: unknown) => {
    setLoading(false);
    const msg = e instanceof Error ? e.message : String(e);
    useNoticeStore.getState().push(`读取下载缓存信息失败: ${msg}`, 'error');
  }, []);

  useEffect(() => {
    let live = true;
    void getAudioCache().then(
      (u) => {
        if (live) apply(u);
      },
      (e) => {
        if (live) fail(e);
      },
    );
    return () => {
      live = false;
    };
  }, [apply, fail]);

  const runClear = async (all: boolean) => {
    setClearing(true);
    try {
      const res = await clearAudioCache(all);
      setUsage(res.after);
      setResult(clearSummary(res));
      useNoticeStore
        .getState()
        .push(res.removed > 0 ? '下载缓存已清理' : '下载缓存无需清理', 'info');
      setConfirming(false);
    } catch (e) {
      const msg = e instanceof Error ? e.message : String(e);
      useNoticeStore.getState().push(`清理下载缓存失败: ${msg}`, 'error');
    } finally {
      setClearing(false);
    }
  };

  const rows = sourceRows(usage);
  const busy = loading || clearing;

  return (
    <div className="space-y-2 max-w-lg">
      <div className="flex items-center justify-between gap-2">
        <Label className="text-xs font-semibold flex items-center gap-1.5">
          <HardDrive className="w-3.5 h-3.5 text-muted-foreground" />
          下载缓存
        </Label>
        <div className="flex items-center gap-2">
          <Button
            variant="ghost"
            size="icon-sm"
            onClick={() => {
              setLoading(true);
              void getAudioCache().then(apply, fail);
            }}
            disabled={busy}
            title="刷新缓存用量"
          >
            <RefreshCw className={`w-3.5 h-3.5 ${loading ? 'animate-spin' : ''}`} />
          </Button>
          <Button
            variant="outline"
            size="sm"
            className="h-8 px-3 text-xs"
            disabled={busy || !usage?.files}
            onClick={() => setConfirming(true)}
            title="删除缓存里的音频文件（不影响曲库）"
          >
            {clearing ? <Loader2 className="w-3.5 h-3.5 animate-spin" /> : <Trash2 className="w-3.5 h-3.5 text-muted-foreground" />}
            清理缓存
          </Button>
        </div>
      </div>

      <p className="text-[11px] text-muted-foreground font-mono">{usageLine(usage)}</p>

      {rows.length > 0 && (
        <ul className="text-[11px] text-muted-foreground space-y-0.5">
          {rows.map((r) => (
            <li key={r.source} className="flex justify-between gap-2">
              <span>{r.source}</span>
              <span className="font-mono">
                {r.bytesHuman} · {r.files} 个
              </span>
            </li>
          ))}
        </ul>
      )}

      <p className="text-[11px] text-muted-foreground">
        试听和「加库」时下载的音频缓存在曲库之外，不会影响曲库里的文件。
        {usage?.auto_prune.enabled
          ? `超过 ${usage.auto_prune.max_human} 后，worker 会每 30 分钟自动清理一次，从最旧的文件开始删。`
          : '当前未设置自动清理上限（worker 的 AUDIO_CACHE_MAX_MB=0），缓存只会增长。'}
      </p>

      {result && <p className="text-[11px] text-foreground">{result}</p>}

      <Dialog open={confirming} onOpenChange={setConfirming}>
        <DialogContent className="sm:max-w-md">
          <DialogHeader>
            <DialogTitle className="text-sm">清理下载缓存</DialogTitle>
            <DialogDescription className="text-xs space-y-2">
              <span className="block">
                将删除曲库之外缓存的 {usage?.files ?? 0} 个音频文件（当前 {usage?.bytes_human ?? '—'}）。
                曲库里的文件不受影响；之后再次试听同一首会重新下载。
              </span>
              <span className="block">{guardNote(usage)}</span>
            </DialogDescription>
          </DialogHeader>
          <DialogFooter className="flex-col gap-2 sm:flex-row sm:justify-between">
            <Button
              variant="ghost"
              size="sm"
              className="h-8 px-3 text-xs text-muted-foreground"
              disabled={clearing}
              onClick={() => void runClear(true)}
              title="连保留窗口内的文件一起删除，可能打断正在播放的音频"
            >
              全部删除（含保留窗口）
            </Button>
            <div className="flex gap-2">
              <Button variant="outline" size="sm" className="h-8 px-3 text-xs" onClick={() => setConfirming(false)}>
                取消
              </Button>
              <Button
                size="sm"
                className="h-8 px-3 text-xs"
                disabled={clearing}
                onClick={() => void runClear(false)}
              >
                {clearing && <Loader2 className="w-3.5 h-3.5 animate-spin" />}
                清理
              </Button>
            </div>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}
