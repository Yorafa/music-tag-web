import { useCallback, useEffect, useMemo, useState } from 'react';
import {
  ArchiveRestore,
  FileText,
  Flame,
  Image as ImageIcon,
  Loader2,
  Music,
  RotateCcw,
} from 'lucide-react';

import { Button } from '@/components/ui/button';
import { Badge } from '@/components/ui/badge';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import {
  listTrash,
  purgeTrash,
  restoreFromTrash,
  type TrashBatch,
  type TrashFile,
  type TrashListing,
} from '@/api/client';
import { formatBytes } from '@/utils/formatBytes';
import { useNoticeStore } from '@/store/useNoticeStore';

function formatWhen(iso: string): string {
  if (!iso) return '';
  const d = new Date(iso);
  // A batch directory the app named parses; one a human made does not, and
  // that file is still perfectly recoverable — so fall back rather than hide.
  if (Number.isNaN(d.getTime())) return iso;
  return d.toLocaleString('zh-CN', { hour12: false });
}

/** A purge waiting for its second click. `batch` destroys one whole batch
 *  directory — the only way an emptied batch ever stops being listed;
 *  `picked` destroys exactly the ticked files, which may span batches. */
type PurgeRequest = { kind: 'batch'; batchId: string } | { kind: 'picked' };

function FileIcon({ f }: { f: TrashFile }) {
  if (f.is_audio) return <Music className="w-3.5 h-3.5 shrink-0 text-primary" />;
  if (f.content_type.startsWith('image/')) {
    return <ImageIcon className="w-3.5 h-3.5 shrink-0 text-amber-400" />;
  }
  return <FileText className="w-3.5 h-3.5 shrink-0 text-muted-foreground" />;
}

export function TrashDialog({
  open,
  onOpenChange,
}: {
  open: boolean;
  onOpenChange: (v: boolean) => void;
}) {
  const [batches, setBatches] = useState<TrashBatch[]>([]);
  const [totalFiles, setTotalFiles] = useState(0);
  const [truncated, setTruncated] = useState(false);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [picked, setPicked] = useState<Set<string>>(new Set());
  // WHICH operation is running, not just that one is. A single boolean
  // made the restore button say 「恢复中…」 while a purge was in flight —
  // the user watches the button for the action they just chose report the
  // other one. The two are opposites (one brings a file back, one
  // destroys it), so the label has to name the right one.
  const [busy, setBusy] = useState<'restore' | 'purge' | null>(null);
  // What the user has asked to destroy and has NOT yet confirmed. Held as
  // state rather than fired straight from the button: purge is the one
  // action in this app that cannot be undone, so it gets the same two-step
  // every other destructive action gets, and the confirm copy can name what
  // is about to go.
  const [pendingPurge, setPendingPurge] = useState<PurgeRequest | null>(null);

  const applyListing = useCallback((listing: TrashListing) => {
    setBatches(listing.batches);
    setTotalFiles(listing.total_files);
    setTruncated(listing.truncated);
  }, []);

  const load = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      applyListing(await listTrash());
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setLoading(false);
    }
  }, [applyListing]);

  // Fetched on open, not on mount: the trash changes when something is
  // deleted, and a dialog that showed a stale list would be worse than one
  // that takes a moment to appear. The `active` guard is the same shape
  // DirPickerDrawer uses — without it a fast close-then-reopen can land the
  // first response after the second request and show the older listing.
  useEffect(() => {
    if (!open) return;
    let active = true;
    void (async () => {
      setLoading(true);
      setError(null);
      try {
        const listing = await listTrash();
        if (!active) return;
        applyListing(listing);
      } catch (e) {
        if (!active) return;
        setError((e as Error).message);
      } finally {
        if (active) setLoading(false);
      }
    })();
    return () => {
      active = false;
    };
  }, [open, applyListing]);

  const handleOpenChange = (next: boolean) => {
    if (!next) {
      setPicked(new Set());
      // A confirmation left open across a close would reappear over the
      // next listing, describing files that are no longer on screen.
      setPendingPurge(null);
    }
    onOpenChange(next);
  };

  const toggle = (key: string) =>
    setPicked((prev) => {
      const next = new Set(prev);
      if (next.has(key)) next.delete(key);
      else next.add(key);
      return next;
    });

  // A file is addressed by (batch, rel_path): two batches can hold the same
  // library path, since deleting and re-adding produces exactly that.
  const restoreTargets = useMemo(
    () =>
      [...picked].map((k) => {
        const i = k.indexOf('\u0000');
        return { batchId: k.slice(0, i), relPath: k.slice(i + 1) };
      }),
    [picked],
  );

  /** The picked files grouped by batch — the purge endpoint takes one
   *  batch_id per call, the same constraint restore has. */
  const pickedByBatch = useMemo(() => {
    const byBatch = new Map<string, string[]>();
    for (const t of restoreTargets) {
      const list = byBatch.get(t.batchId) ?? [];
      list.push(t.relPath);
      byBatch.set(t.batchId, list);
    }
    return byBatch;
  }, [restoreTargets]);

  const handleRestore = async () => {
    if (restoreTargets.length === 0) return;
    setBusy('restore');
    // Group by batch: the endpoint takes one batch_id per call, and firing
    // one request per file would both hammer the API and lose the per-batch
    // result shape.
    const byBatch = new Map<string, string[]>();
    for (const t of restoreTargets) {
      const list = byBatch.get(t.batchId) ?? [];
      list.push(t.relPath);
      byBatch.set(t.batchId, list);
    }
    let restored = 0;
    const problems: string[] = [];
    try {
      for (const [batchId, paths] of byBatch) {
        const report = await restoreFromTrash(batchId, paths);
        restored += report.restored;
        for (const row of report.results) {
          if (row.status !== 'restored') {
            problems.push(`${row.rel_path}：${row.reason ?? row.status}`);
          }
        }
      }
      useNoticeStore
        .getState()
        .push(
          `已恢复 ${restored} 个文件${problems.length ? `，${problems.length} 个未恢复` : ''}`,
          problems.length ? 'warn' : 'info',
        );
      if (problems.length) {
        // The reasons are the whole value of this dialog — "目标位置已有同名
        // 文件" tells the user what to do, a bare count does not.
        useNoticeStore.getState().push(problems[0], 'warn');
      }
      setPicked(new Set());
      await load();
    } catch (e) {
      useNoticeStore.getState().push(`恢复失败：${(e as Error).message}`, 'error');
    } finally {
      setBusy(null);
    }
  };

  const handlePurge = async () => {
    if (!pendingPurge) return;
    const req = pendingPurge;
    // One call per batch, because batch_id is a single field. A throw
    // aborts the rest: reporting a partial purge as a whole one is the
    // kind of quiet lie this dialog is trying not to tell.
    const calls: Array<[string, string[]]> =
      req.kind === 'batch'
        ? [[req.batchId, []]]
        : [...pickedByBatch.entries()];
    setBusy('purge');
    try {
      let purged = 0;
      const problems: string[] = [];
      for (const [batchId, paths] of calls) {
        const report = await purgeTrash(batchId, paths);
        purged += report.purged;
        for (const row of report.results) {
          if (row.status !== 'purged') {
            problems.push(`${row.rel_path}：${row.reason ?? row.status}`);
          }
        }
      }
      useNoticeStore
        .getState()
        .push(
          `已彻底删除 ${purged} 个文件${problems.length ? `，${problems.length} 个未删除` : ''}`,
          problems.length ? 'warn' : 'info',
        );
      if (problems.length) useNoticeStore.getState().push(problems[0], 'warn');
      setPendingPurge(null);
      // The listing is refetched rather than patched locally: purging a
      // whole batch removes its directory, so a local splice would leave a
      // batch on screen that no longer exists.
      setPicked(new Set());
      await load();
    } catch (e) {
      useNoticeStore.getState().push(`彻底删除失败：${(e as Error).message}`, 'error');
    } finally {
      setBusy(null);
    }
  };

  return (
    <Dialog open={open} onOpenChange={handleOpenChange}>
      <DialogContent className="max-w-2xl max-h-[80vh] flex flex-col">
        <DialogHeader>
          <DialogTitle className="text-base flex items-center gap-2">
            <ArchiveRestore className="w-4 h-4" /> 回收站
          </DialogTitle>
          <DialogDescription className="text-xs">
            删除的文件没有真正消失，暂存在数据目录下的{' '}
            <code className="px-1 rounded bg-muted/50 font-mono">.trash/</code>
            ，可以放回原来的位置。
          </DialogDescription>
        </DialogHeader>

        <div className="flex-1 min-h-0 overflow-y-auto space-y-3 pr-1 text-xs">
          {loading ? (
            <div className="flex items-center justify-center gap-2 py-8 text-muted-foreground">
              <Loader2 className="w-4 h-4 animate-spin" /> 正在读取回收站…
            </div>
          ) : error ? (
            <p className="text-destructive py-6 text-center">{error}</p>
          ) : batches.length === 0 ? (
            <p className="text-muted-foreground py-8 text-center">
              回收站是空的。删除文件后它们会出现在这里，可以随时放回。
            </p>
          ) : (
            <>
              {truncated && (
                <p className="text-amber-500">
                  共 {totalFiles} 个文件，这里只列出最近的一部分。
                </p>
              )}
              {batches.map((b) => (
                <div
                  key={b.id}
                  className="rounded-lg border border-border/60 bg-surface-1 overflow-hidden"
                >
                  <div className="flex items-center justify-between gap-2 px-3 py-2 bg-muted/40 border-b border-border/50">
                    <span className="font-mono text-[11px] truncate">
                      {formatWhen(b.deleted_at)}
                    </span>
                    <span className="flex items-center gap-1.5 shrink-0">
                      <button
                        type="button"
                        onClick={() =>
                          setPendingPurge({ kind: 'batch', batchId: b.id })
                        }
                        disabled={busy !== null}
                        title="彻底删除这个批次的全部文件（不可撤销）"
                        className="inline-flex items-center gap-1 px-1.5 h-5 rounded text-[10px] text-destructive/90 hover:bg-destructive/10 hover:text-destructive transition-colors disabled:opacity-40 shrink-0"
                      >
                        <Flame className="w-3 h-3" />
                        彻底删除
                      </button>
                      <Badge variant="secondary" className="h-4 px-1 text-[10px] font-mono">
                        {b.files.length} 个
                      </Badge>
                      <span className="text-[10px] text-muted-foreground font-mono">
                        {formatBytes(b.total_size)}
                      </span>
                    </span>
                  </div>
                  {/* No empty-batch branch: the server omits a batch that
                      holds nothing, so a row here always has a file in it.
                      The empty directory is still on disk, and a whole-batch
                      purge from the header above is what removes it. */}
                  <div className="divide-y divide-border/40 max-h-56 overflow-y-auto">
                    {b.files.map((f) => {
                      const key = `${b.id}\u0000${f.rel_path}`;
                      return (
                        <label
                          key={key}
                          className="flex items-center gap-2 px-3 py-1.5 cursor-pointer hover:bg-accent/40"
                        >
                          <input
                            type="checkbox"
                            checked={picked.has(key)}
                            onChange={() => toggle(key)}
                            className="w-3.5 h-3.5 rounded border-border accent-primary shrink-0 cursor-pointer"
                          />
                          <FileIcon f={f} />
                          <span className="truncate flex-1" title={f.rel_path}>
                            {f.rel_path}
                          </span>
                          <span className="text-[10px] text-muted-foreground font-mono shrink-0">
                            {formatBytes(f.size)}
                          </span>
                        </label>
                      );
                    })}
                  </div>
                </div>
              ))}
            </>
          )}
        </div>

        <DialogFooter showCloseButton>
          <Button
            variant="outline"
            onClick={() => void load()}
            disabled={loading || busy !== null}
            className="text-xs h-8"
          >
            刷新
          </Button>
          <Button
            onClick={handleRestore}
            disabled={restoreTargets.length === 0 || busy !== null || loading}
            className="text-xs h-8"
          >
            <RotateCcw className={`w-3.5 h-3.5 ${busy === 'restore' ? 'animate-spin' : ''}`} />
            {busy === 'restore' ? '恢复中…' : `恢复选中 (${restoreTargets.length})`}
          </Button>
          <Button
            variant="ghost"
            onClick={() => setPendingPurge({ kind: 'picked' })}
            disabled={pickedByBatch.size === 0 || busy !== null || loading}
            className="text-xs h-8 text-destructive hover:bg-destructive/10 hover:text-destructive"
            title="删除选中的文件，无法再放回"
          >
            <Flame className={`w-3.5 h-3.5 ${busy === 'purge' ? 'animate-spin' : ''}`} />
            {busy === 'purge' ? '删除中…' : `彻底删除选中 (${restoreTargets.length})`}
          </Button>
        </DialogFooter>
      </DialogContent>

      {/* The second click. A purge that fires from the first one is a purge
          nobody can cancel once they realise they meant 恢复. */}
      <Dialog open={pendingPurge !== null} onOpenChange={(v) => !v && setPendingPurge(null)}>
        <DialogContent className="max-w-sm">
          <DialogHeader>
            <DialogTitle className="text-base flex items-center gap-2">
              <Flame className="w-4 h-4 text-destructive" />
              确认彻底删除
            </DialogTitle>
            <DialogDescription className="text-xs">
              {pendingPurge?.kind === 'batch'
                ? `将彻底删除整个批次（${pendingPurge.batchId}）及其中的全部文件。此操作不可撤销。`
                : `将彻底删除选中的 ${restoreTargets.length} 个文件，无法再放回。此操作不可撤销。`}
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button
              variant="outline"
              onClick={() => setPendingPurge(null)}
              disabled={busy !== null}
              className="text-xs h-8"
            >
              取消
            </Button>
            <Button
              onClick={handlePurge}
              disabled={busy !== null || !pendingPurge}
              className="text-xs h-8 bg-destructive text-destructive-foreground hover:bg-destructive/90 font-semibold"
            >
              <Flame className="w-3.5 h-3.5" />
              {busy === 'purge' ? '删除中…' : '确认删除'}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </Dialog>
  );
}
