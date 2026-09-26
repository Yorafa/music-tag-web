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
  activeFields,
  buildOverrides,
  changedCellCount,
  changedRowCount,
  draftFor,
  hasUnreadable,
  seedDrafts,
  type OverrideDrafts,
  type ParseTagField,
} from './parseOverride';
import {
  PARSE_TAG_FIELDS,
  PARSE_TAG_LABELS,
  PATTERN_PRESETS,
  buildPattern,
  describeRule,
  fieldsFromPattern,
  firstParsedExample,
  patternHelp,
  tryPattern,
} from './parseAssist';
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
  // Bumped to force a fresh preview. It exists because clearing the token
  // is not enough on its own: when the token is ALREADY null — a preview
  // that failed, or a selection that arrived empty — `setToken(null)` is
  // a no-op, React does not re-render, and the effect never re-runs. The
  // "重新解析" button was therefore dead in exactly the case where the
  // user most needs it: they fixed a broken pattern and clicked again.
  const [previewNonce, setPreviewNonce] = useState(0);
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
      // Drop state on close so a re-open forces a fresh preview. The
      // pattern is re-seeded from the prop too: it used to be left at
      // whatever was last typed locally, so if the caller ever changed it
      // from outside (or the user abandoned a half-typed rule), the box
      // and the caller's idea of the pattern silently diverged.
      setToken(null);
      setResults([]);
      setOverrides({});
      setError(null);
      setLoading(null);
      setPattern(initialPattern);
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
    // button is the explicit trigger, and it moves the nonce.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, token, selectedPaths, previewNonce]);

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
  const help = useMemo(() => patternHelp(pattern), [pattern]);
  const patternErr = help.problem;
  // The chip row and the box are two views of one value, so the box is
  // the single source of truth: clicking a chip GENERATES the pattern
  // rather than editing it, so there is no way for the two to drift.
  const chosenFields = useMemo(() => fieldsFromPattern(pattern), [pattern]);
  const [tryName, setTryName] = useState('');
  const tryResult = useMemo(() => tryPattern(tryName, pattern), [tryName, pattern]);

  // Picking a preset also seeds the try-it box with that preset's own
  // example, so the shape proves itself instead of being asserted.
  const applyPreset = (fields: ParseTagField[], example: string) => {
    const p = buildPattern(fields);
    setPattern(p);
    onPatternChange?.(p);
    setTryName(example);
  };

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

  // Both halves matter. Clearing the token discards the table the old
  // pattern produced; bumping the nonce is what actually guarantees the
  // effect re-runs, which clearing alone does not when the token was
  // already null.
  const handleReparse = () => {
    onPatternChange?.(pattern);
    setToken(null);
    setResults([]);
    setOverrides({});
    setError(null);
    setPreviewNonce((n) => n + 1);
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
          {/* ---- Rule picker. Chips first, regex second: the person who
              does not know regex should never have to meet one to finish
              the job, and the person who does can still type over the
              generated text. ---- */}
          <div className="space-y-2 rounded border border-border p-3">
            <div className="text-xs font-medium text-muted-foreground">
              文件名里依次是什么？（点选，自动生成规则）
            </div>
            <div className="flex flex-wrap gap-1.5">
              {PARSE_TAG_FIELDS.map((f) => {
                const on = chosenFields.includes(f);
                return (
                  <button
                    key={f}
                    type="button"
                    onClick={() => {
                      const next = on
                        ? chosenFields.filter((x) => x !== f)
                        : [...chosenFields, f];
                      const p = next.length === 0 ? '' : buildPattern(next);
                      setPattern(p);
                      onPatternChange?.(p);
                    }}
                    className={cn(
                      'rounded-md border px-2 py-0.5 text-xs transition-colors',
                      on
                        ? 'border-primary bg-primary/10 text-primary'
                        : 'border-border text-muted-foreground hover:bg-accent',
                    )}
                    aria-pressed={on}
                    data-testid={`parse-field-chip-${f}`}
                  >
                    {PARSE_TAG_LABELS[f]}
                  </button>
                );
              })}
            </div>
            {chosenFields.length > 0 && (
              <div className="text-xs text-muted-foreground">
                当前顺序：{chosenFields.map((f) => PARSE_TAG_LABELS[f]).join(' → ')}
                （再点一次可移除；从左到右即文件名里的先后）
              </div>
            )}

            <div className="text-xs font-medium text-muted-foreground pt-1">
              常见命名（不确定就用这个）
            </div>
            <div className="flex flex-wrap gap-1.5">
              {PATTERN_PRESETS.map((pre) => (
                <button
                  key={pre.id}
                  type="button"
                  title={pre.hint}
                  onClick={() => applyPreset(pre.fields, pre.example)}
                  className="rounded-md border border-border px-2 py-0.5 text-xs text-muted-foreground hover:bg-accent"
                  data-testid={`parse-preset-${pre.id}`}
                >
                  {pre.label}
                </button>
              ))}
            </div>
          </div>

          <div className="flex items-start gap-2">
            <Input
              className="h-8 text-sm font-mono"
              value={pattern}
              placeholder="留空 = 默认规则"
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
          {/* The rule is always spelled out, default included. An empty
              box used to mean "something is happening that I cannot see",
              which is the same feeling as a broken feature. */}
          <div className="text-xs text-muted-foreground" data-testid="parse-rule-text">
            当前规则：{describeRule(pattern)}
          </div>
          {patternErr && (
            <div className="text-xs text-destructive" data-testid="parse-filenames-pattern-error">
              {patternErr}
            </div>
          )}
          {pattern.trim() === '' && (
            <div className="text-xs text-muted-foreground">
              默认规则已能处理 <code className="font-mono">{'艺术家 - 标题'}</code>{' '}
              这类两段式；只有当文件名里还带着专辑或音轨时，才需要上面的规则。
            </div>
          )}

          {/* Try-it box: run the rule against one filename without
              spending a preview round trip. Seeded from the real
              selection so it starts as the user's own data. */}
          <div className="space-y-1.5 rounded border border-border p-3">
            <div className="text-xs font-medium text-muted-foreground">
              试一下（不会写入任何文件）
            </div>
            <div className="flex items-center gap-2">
              <Input
                className="h-8 text-sm"
                value={tryName}
                placeholder="粘一个文件名，例如 周杰倫 - 晴天.flac"
                onChange={(e) => setTryName(e.target.value)}
                aria-label="试算文件名"
                spellCheck={false}
                data-testid="parse-try-name"
              />
              {tryName === '' && results.length > 0 && (
                <Button
                  variant="ghost"
                  size="sm"
                  className="shrink-0 h-8 text-xs"
                  onClick={() => setTryName(firstParsedExample(results))}
                >
                  用选中项
                </Button>
              )}
            </div>
            {tryName.trim() !== '' && (
              <TryItResult result={tryResult} />
            )}
          </div>

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

/** The try-it box's verdict. Shows the status word the server would
 *  use, so "unparsable" here and in the table below mean the same thing —
 *  a local preview that invents friendlier words would be teaching the
 *  wrong vocabulary. */
function TryItResult({
  result,
}: {
  result: ReturnType<typeof tryPattern>;
}) {
  if (result.status === 'unparsable') {
    return (
      <div className="text-xs text-amber-600 dark:text-amber-400" data-testid="parse-try-result">
        这条规则匹配不上这个文件名。可以换一个预设，或在上面的方框里调整字段顺序。
      </div>
    );
  }
  return (
    <div className="text-xs" data-testid="parse-try-result">
      <span className="text-muted-foreground">将得到：</span>{' '}
      {result.results.map((r) => (
        <span key={r.field} className="mr-2 inline-flex items-baseline gap-1">
          <span className="text-muted-foreground">{PARSE_TAG_LABELS[r.field]}</span>
          <code className="font-mono">{r.value}</code>
        </span>
      ))}
      {result.status === 'ambiguous' && (
        <span className="ml-1 text-amber-600 dark:text-amber-400">
          （多段，标题为合并结果）
        </span>
      )}
    </div>
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
