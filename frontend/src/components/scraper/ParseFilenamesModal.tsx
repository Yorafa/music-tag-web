/* eslint-disable react-hooks/set-state-in-effect -- This whole modal
 * uses the canonical fetch-on-mount + reload-on-close pattern. The
 * rule's recommended fix (React Query / Suspense) introduces runtime
 * deps a single modal component can't justify; when we migrate to a
 * fetcher library the `useEffect` block (and its associated state
 * initialisation) collapses cleanly.
 */

// 解析文件名 round-trip modal. Triggered from the worklist's
// 「解析文件名」 button. The flow:
//
//   1. Mount → POST /api/tag/preview_parse_filenames/ for every
//      selected row's path → token + per-row ParsedPreviewRow[].
//   2. Render scrollable table: file basename + one editable cell per
//      field the parser read something into + status badge.
//   3. User types into override cells → override state updates.
//   4. Click "Apply" → POST /api/tag/apply_parsed_filenames/ with the
//      token + per-row overrides. Backend enqueues the async worker,
//      returns the task_id which we surface as a toast.
//
// The modal is mounted with an optional starting pattern and reports
// one back up so the toolbar can re-open it with whatever the user
// last typed (see WorkstationToolbar's `parsePattern` state) — a
// downloader's naming convention does not change between batches, and
// re-typing a regex per batch is the kind of friction that makes
// people stop using the feature.
//
// Override semantics live in parseOverride.ts and are tested there. The
// short version: an empty cell inherits the parsed value, a filled one
// replaces it, and there is no way to clear a tag from here — the batch
// editor owns deleting.
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
} from '@/api/client';
import {
  PARSE_TAG_LABELS,
  PATTERN_PLACEHOLDER,
  activeFields,
  buildOverrides,
  changedCellCount,
  changedRowCount,
  draftFor,
  hasUnreadable,
  patternProblem,
  seedDrafts,
  type OverrideDrafts,
  type ParseTagField,
} from './parseOverride';
import { cn } from '@/lib/utils';

interface ParseFilenamesModalProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  /** Selected row IDs (== fullPath under MUSIC_DIR). */
  selectedPaths: string[];
  /** Starting pattern. Kept by the caller so a re-open keeps it. */
  initialPattern?: string;
  /** Called whenever the pattern changes, so the caller can re-open with it. */
  onPatternChange?: (pattern: string) => void;
}

export function ParseFilenamesModal({
  open,
  onOpenChange,
  selectedPaths,
  initialPattern = '',
  onPatternChange,
}: ParseFilenamesModalProps) {
  const [token, setToken] = useState<string | null>(null);
  const [results, setResults] = useState<ParsedPreviewRow[]>([]);
  const [overrides, setOverrides] = useState<OverrideDrafts>({});
  const [pattern, setPattern] = useState(initialPattern);
  const [loading, setLoading] = useState<'preview' | 'apply' | null>(null);
  const [error, setError] = useState<string | null>(null);

  // Preview mount: when the modal opens AND we don't yet have a token
  // for this batch, POST the preview path. Re-running on every open
  // would re-consume 10-min TTL slots on the server; we keep a
  // sticky token until the modal closes or the user clicks Apply.
  //
  // The preview is keyed on the pattern too, so editing it and hitting
  // "重新解析" invalidates the token rather than applying overrides that
  // were typed against a table the user can no longer see.
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
          { pattern: pattern.trim() || undefined },
        );
        if (cancelled) return;
        setToken(t);
        setResults(rs);
        setOverrides(seedDrafts(rs));
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
    // `pattern` is intentionally absent: including it would re-preview
    // on every keystroke, burning a TTL slot per character. The re-parse
    // button is the explicit trigger (it clears the token first).
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, token, selectedPaths]);

  const overridesForApply = useMemo(
    () => buildOverrides(results, overrides),
    [overrides, results],
  );
  const changedRows = useMemo(
    () => changedRowCount(results, overrides),
    [overrides, results],
  );
  const changedCells = useMemo(() => changedCellCount(overrides), [overrides]);
  const fields = useMemo(() => activeFields(results), [results]);
  const unreadable = useMemo(() => hasUnreadable(results), [results]);
  const patternErr = useMemo(() => patternProblem(pattern), [pattern]);

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
    field: ParseTagField,
    value: string,
  ) => {
    setOverrides((prev) => ({
      ...prev,
      [path]: { ...draftFor(prev, path), [field]: value },
    }));
  };

  // Dropping the token is what makes the effect above re-run, so this is
  // also what discards the table the old pattern produced.
  const handleReparse = () => {
    onPatternChange?.(pattern);
    setToken(null);
    setResults([]);
    setOverrides({});
    setError(null);
  };

  const handlePatternEdit = (v: string) => {
    setPattern(v);
    onPatternChange?.(v);
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-5xl">
        <DialogHeader>
          <DialogTitle>
            <span className="inline-flex items-center gap-2">
              <Music2 className="w-4 h-4" />
              解析文件名
            </span>
          </DialogTitle>
          <DialogDescription>
            下载来的文件常常还没有标签，刮削也就无从比对。点「预览」先看解析结果，
            任何一格都能手动改；确认后点「应用」写入，刮削就有东西可匹配了。
            解析只填，不删——要清空标签请用「批量编辑标签」。
          </DialogDescription>
        </DialogHeader>

        <div className="space-y-3 py-2">
          <div className="flex items-start gap-2">
            <Input
              className="h-8 text-sm font-mono"
              value={pattern}
              placeholder={PATTERN_PLACEHOLDER}
              onChange={(e) => handlePatternEdit(e.target.value)}
              aria-label="命名模板"
              spellCheck={false}
            />
            <Button
              variant="outline"
              size="sm"
              className="shrink-0 h-8"
              onClick={handleReparse}
              disabled={loading !== null || selectedPaths.length === 0 || patternErr !== null}
              data-testid="parse-filenames-reparse"
            >
              重新解析
            </Button>
          </div>
          {patternErr && (
            <div className="text-xs text-destructive" data-testid="parse-filenames-pattern-error">
              {patternErr}
            </div>
          )}
          {pattern.trim() === '' && (
            <div className="text-xs text-muted-foreground">
              留空则按 <code className="font-mono">艺术家 - 标题</code> 拆两段。
              命名更规整时可以填一条正则，用 <code className="font-mono">{'(?P<album>...)'}</code>{' '}
              这样的命名分组指定每段对应哪个字段。
            </div>
          )}

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
            <>
              <div
                className="max-h-[55vh] overflow-y-auto rounded border border-border"
                data-testid="parse-filenames-table"
              >
                <div
                  className="grid gap-2 px-3 py-2 text-xs font-medium text-muted-foreground border-b border-border sticky top-0 bg-surface-1"
                  style={{ gridTemplateColumns: gridTemplate(fields.length) }}
                >
                  <div>文件名</div>
                  {fields.map((f) => (
                    <div key={f}>{fieldLabel(f)}</div>
                  ))}
                  <div>状态</div>
                </div>
                {results.map((r) => (
                  <div
                    key={r.path}
                    className="grid gap-2 px-3 py-2 items-center text-sm border-b border-border last:border-b-0"
                    style={{ gridTemplateColumns: gridTemplate(fields.length) }}
                  >
                    <div className="truncate font-mono text-xs" title={r.path}>
                      {basename(r.path)}
                    </div>
                    {fields.map((f) => (
                      <Input
                        key={f}
                        className="h-8 text-sm"
                        value={draftFor(overrides, r.path)[f]}
                        placeholder={r[f] ?? ''}
                        onChange={(e) => setOverrideField(r.path, f, e.target.value)}
                        aria-label={`${fieldLabel(f)} 覆盖 for ${basename(r.path)}`}
                      />
                    ))}
                    <StatusBadge status={r.status} />
                  </div>
                ))}
              </div>
              {unreadable && (
                <div className="text-xs text-muted-foreground">
                  标为 ambiguous / unparsable 的行解析不出结果，直接在上表里手填即可照常写入。
                </div>
              )}
              {changedCells > 0 && (
                <div className="text-xs text-muted-foreground">
                  已手动改动 {changedCells} 格（{changedRows} 行），留空的格子沿用解析值。
                </div>
              )}
            </>
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
                应用 ({changedRows} 个覆盖 / {results.length} 行)
              </>
            )}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

/** One flexible column per active field, plus the file name, the status
 *  badge, and their gaps. Kept as a template string so the header and
 *  every row are guaranteed to line up. */
function gridTemplate(fieldCount: number): string {
  return ['minmax(0,1.4fr)', ...Array(fieldCount).fill('minmax(0,1fr)'), 'auto'].join(
    ' ',
  );
}

function fieldLabel(f: ParseTagField): string {
  return PARSE_TAG_LABELS[f];
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
