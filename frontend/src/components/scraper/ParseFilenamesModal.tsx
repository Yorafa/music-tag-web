/* eslint-disable react-hooks/set-state-in-effect -- This whole modal
 * uses the canonical fetch-on-mount + reload-on-close pattern. The
 * rule's recommended fix (React Query / Suspense) introduces runtime
 * deps a single modal component can't justify; when we migrate to a
 * fetcher library the `useEffect` block (and its associated state
 * initialisation) collapses cleanly.
 */

// C.2 Filename Parse round-trip modal. Triggered from ScrapeTopBar's
// "解析文件名" button. The flow:
//
//   1. Mount → POST /api/tag/preview_parse_filenames/ for every
//      selected row's path → token + per-row ParsedPreviewRow[].
//   2. Render scrollable table: file basename + parsed Artist (editable
//      override) + parsed Title (editable override) + status badge.
//   3. User types into override inputs → override state updates.
//   4. Click "Apply" → POST /api/tag/apply_parsed_filenames/ with the
//      token + per-row overrides. Backend enqueues the async worker,
//      returns the task_id which we surface as a toast.
//
// Override semantics: an empty override field inherits the parsed value
// (so the user only types rows they want to change). A non-empty
// override REPLACES the parsed value on the backend. Overriding an
// "unparsable" row's artist OR title flips status to "ok" server-side.
//
// Token expiry (401 "preview_expired") is caught here and re-prompts
// the user with a fresh preview by re-calling the parent's onReapply
// callback. Negative-pressure case: large input batch with a slow
// modal mount can exceed the 10min TTL if the user walks away; we
// surface the re-preview prompt rather than 4xx silently.

import { useEffect, useMemo, useState } from 'react';
import { Loader2, Music2, CheckCircle2, AlertTriangle, XCircle } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogFooter,
  DialogDescription,
} from '@/components/ui/dialog';
import { useNoticeStore } from '@/store/useNoticeStore';
import {
  previewParseFilenames,
  applyParsedFilenames,
  type ParsedPreviewRow,
  type ParseApplyOverride,
} from '@/api/client';
import { cn } from '@/lib/utils';

interface ParseFilenamesModalProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  /** Selected row IDs (== fullPath under MUSIC_DIR). */
  selectedPaths: string[];
}

interface RowOverrideDraft {
  artist: string;
  title: string;
}

export function ParseFilenamesModal({
  open,
  onOpenChange,
  selectedPaths,
}: ParseFilenamesModalProps) {
  const [token, setToken] = useState<string | null>(null);
  const [results, setResults] = useState<ParsedPreviewRow[]>([]);
  const [overrides, setOverrides] = useState<Record<string, RowOverrideDraft>>({});
  const [loading, setLoading] = useState<'preview' | 'apply' | null>(null);
  const [error, setError] = useState<string | null>(null);

  // Preview mount: when the modal opens AND we don't yet have a token
  // for this batch, POST the preview path. Re-running on every open
  // would re-consume 10-min TTL slots on the server; we keep a
  // sticky token until the modal closes or the user clicks Apply.
  useEffect(() => {
    if (!open) {
      // Drop state on close so a re-open forces a fresh preview.
      setToken(null);
      setResults([]);
      setOverrides({});
      setError(null);
      setLoading(null);
      return;
    }
    if (token || selectedPaths.length === 0) return;
    let cancelled = false;
    (async () => {
      setLoading('preview');
      setError(null);
      try {
        const { token: t, results: rs } = await previewParseFilenames(
          selectedPaths,
        );
        if (cancelled) return;
        setToken(t);
        setResults(rs);
        // Pre-seed overrides with empty values so React keys stay stable
        // even before the user types anything.
        const seed: Record<string, RowOverrideDraft> = {};
        for (const r of rs) {
          seed[r.path] = { artist: '', title: '' };
        }
        setOverrides(seed);
      } catch (e: unknown) {
        if (cancelled) return;
        const msg = e instanceof Error ? e.message : String(e);
        setError(msg);
        useNoticeStore.getState().push(`解析预览失败: ${msg}`, 'error');
      } finally {
        if (!cancelled) setLoading(null);
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [open, token, selectedPaths]);

  const overridesForApply = useMemo<ParseApplyOverride[]>(() => {
    const list: ParseApplyOverride[] = [];
    for (const r of results) {
      const draft = overrides[r.path];
      if (!draft) continue;
      const trimmedArtist = draft.artist.trim();
      const trimmedTitle = draft.title.trim();
      if (trimmedArtist === '' && trimmedTitle === '') continue;
      list.push({
        path: r.path,
        ...(trimmedArtist ? { artist: trimmedArtist } : {}),
        ...(trimmedTitle ? { title: trimmedTitle } : {}),
      });
    }
    return list;
  }, [overrides, results]);

  const handleApply = async () => {
    if (!token) return;
    setLoading('apply');
    try {
      const res = await applyParsedFilenames(token, overridesForApply);
      useNoticeStore.getState().push(
        `已提交解析写入任务 (${res.row_count} 行, task=${res.task_id.slice(0, 8)}…)`,
        'info',
      );
      onOpenChange(false);
    } catch (e: unknown) {
      const msg = e instanceof Error ? e.message : String(e);
      // 401 "preview_expired" → re-preview path. We surface a toast and
      // let the user close + reopen the modal to fetch a fresh token.
      if (msg.includes('preview_expired') || msg.includes('401')) {
        setToken(null);
        useNoticeStore.getState().push('预览已过期,请重新打开对话框刷新预览', 'warn');
      } else {
        useNoticeStore.getState().push(`应用解析失败: ${msg}`, 'error');
      }
    } finally {
      setLoading(null);
    }
  };

  const setOverrideField = (
    path: string,
    field: 'artist' | 'title',
    value: string,
  ) => {
    setOverrides((prev) => ({
      ...prev,
      [path]: {
        ...(prev[path] ?? { artist: '', title: '' }),
        [field]: value,
      },
    }));
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-3xl">
        <DialogHeader>
          <DialogTitle>
            <span className="inline-flex items-center gap-2">
              <Music2 className="w-4 h-4" />
              解析文件名
            </span>
          </DialogTitle>
          <DialogDescription>
            对当前选中的 {selectedPaths.length} 个文件执行 C.2 解析预览;任何字段都可手动覆盖,再点击「应用」写入 Artist + Title 标签。
          </DialogDescription>
        </DialogHeader>

        <div className="space-y-3 py-2">
          {loading === 'preview' && (
            <div className="flex items-center gap-2 text-sm text-muted-foreground">
              <Loader2 className="w-4 h-4 animate-spin" />
              正在请求预览…
            </div>
          )}

          {error && (
            <div className="text-sm text-destructive">预览失败: {error}</div>
          )}

          {!loading && results.length === 0 && !error && (
            <div className="text-sm text-muted-foreground">
              （无内容 — 请至少选择一行 Worklist）
            </div>
          )}

          {results.length > 0 && (
            <div
              className="max-h-[60vh] overflow-y-auto rounded border border-border"
              data-testid="parse-filenames-table"
            >
              <div
                className="grid grid-cols-[minmax(0,1.4fr)_minmax(0,1fr)_minmax(0,1.4fr)_auto] gap-2 px-3 py-2 text-xs font-medium text-muted-foreground border-b border-border sticky top-0 bg-surface-1"
              >
                <div>文件名</div>
                <div>Artist (覆盖)</div>
                <div>Title (覆盖)</div>
                <div>状态</div>
              </div>
              {results.map((r) => (
                <div
                  key={r.path}
                  className="grid grid-cols-[minmax(0,1.4fr)_minmax(0,1fr)_minmax(0,1.4fr)_auto] gap-2 px-3 py-2 items-center text-sm border-b border-border last:border-b-0"
                >
                  <div
                    className="truncate font-mono text-xs"
                    title={r.path}
                  >
                    {basename(r.path)}
                  </div>
                  <Input
                    className="h-8 text-sm"
                    value={overrides[r.path]?.artist ?? ''}
                    placeholder={r.artist ?? ''}
                    onChange={(e) =>
                      setOverrideField(r.path, 'artist', e.target.value)
                    }
                    aria-label={`Artist 覆盖 for ${basename(r.path)}`}
                  />
                  <Input
                    className="h-8 text-sm"
                    value={overrides[r.path]?.title ?? ''}
                    placeholder={r.title ?? ''}
                    onChange={(e) =>
                      setOverrideField(r.path, 'title', e.target.value)
                    }
                    aria-label={`Title 覆盖 for ${basename(r.path)}`}
                  />
                  <StatusBadge status={r.status} />
                </div>
              ))}
            </div>
          )}
        </div>

        <DialogFooter showCloseButton>
          <Button
            onClick={handleApply}
            disabled={loading !== null || !token || results.length === 0}
            data-testid="parse-filenames-apply"
          >
            {loading === 'apply' ? (
              <>
                <Loader2 className="w-4 h-4 mr-2 animate-spin" />
                提交中…
              </>
            ) : (
              <>
                应用 ({overridesForApply.length} 个覆盖 / {results.length} 行)
              </>
            )}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function basename(p: string): string {
  const i = p.lastIndexOf('/');
  return i >= 0 ? p.substring(i + 1) : p;
}

function StatusBadge({ status }: { status: ParsedPreviewRow['status'] }) {
  const cls = cn(
    'inline-flex items-center gap-1 px-2 py-0.5 rounded-md text-xs whitespace-nowrap',
    status === 'ok' &&
      'bg-emerald-500/10 text-emerald-700 dark:text-emerald-300',
    status === 'ambiguous' &&
      'bg-amber-500/10 text-amber-700 dark:text-amber-300',
    status === 'unparsable' &&
      'bg-rose-500/10 text-rose-700 dark:text-rose-300',
  );
  return (
    <span className={cls} aria-label={`status ${status}`}>
      {status === 'ok' && <CheckCircle2 className="w-3 h-3" />}
      {status === 'ambiguous' && <AlertTriangle className="w-3 h-3" />}
      {status === 'unparsable' && <XCircle className="w-3 h-3" />}
      {status}
    </span>
  );
}
