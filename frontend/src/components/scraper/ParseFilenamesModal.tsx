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
//   1. Pick a rule — chips or a preset; both GENERATE the pattern text,
//      so the box and the chips can never disagree.
//   2. POST /api/tag/preview_parse_filenames/ for every selected path →
//      token + per-row ParsedPreviewRow[]. A one-line summary reports
//      how many matched, how many were guessed at, how many will be
//      skipped.
//   3. Click 写入 → POST /api/tag/apply_parsed_filenames/ with the
//      token. Backend enqueues the async worker, returns the task_id
//      which we surface as a toast.
//
// The rule logic (generation, the default rule, the try-it box, the
// RE2 limits) lives in parseAssist.ts and is tested there.
//
// This dialog deliberately does NOT offer per-file editing. It used to:
// there was a table with an editable cell per field. Removing it splits
// the job cleanly — 解析文件名 answers "does my rule work?", and
// 批量编辑标签 answers "what should this actually say?". The batch editor
// has the per-field 「不修改」 control and can DELETE a tag; this dialog
// could do neither, so its edit cells were a weaker version of a
// control that already exists elsewhere in the same toolbar.
//
// The modal is mounted with an optional starting pattern and reports
// one back up so the toolbar can re-open it with whatever the user
// last typed (see WorkstationToolbar's `parsePattern` state) — a
// downloader's naming convention does not change between batches.
//
// Token expiry (401 "preview_expired") is caught here: the token is
// cleared so the effect re-previews, and a toast explains why. A large
// batch left open past the 10min TTL hits this.

import { useEffect, useMemo, useState } from 'react';
import { Loader2, Music2 } from 'lucide-react';
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
  PARSE_TAG_FIELDS,
  PARSE_TAG_LABELS,
  PATTERN_PRESETS,
  buildPattern,
  describeRule,
  fieldsFromPattern,
  firstParsedExample,
  patternHelp,
  tryPattern,
  tallyPreview,
  type ParseTagField,
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
      const res = await applyParsedFilenames(token, []);
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

  // Both halves matter. Clearing the token discards the table the old
  // pattern produced; bumping the nonce is what actually guarantees the
  // effect re-runs, which clearing alone does not when the token was
  // already null.
  const handleReparse = () => {
    onPatternChange?.(pattern);
    setToken(null);
    setResults([]);
    setError(null);
    setPreviewNonce((n) => n + 1);
  };

  const handlePatternEdit = (v: string) => {
    setPattern(v);
    onPatternChange?.(v);
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-5xl">
        <DialogHeader>
          <DialogTitle>
            <span className="inline-flex items-center gap-2">
              <Music2 className="w-4 h-4" />
              解析文件名
            </span>
          </DialogTitle>
          <DialogDescription>
            从文件名解析提取元数据并写入进文件。写入前可以先试算、再看匹配情况；
            只填不删，已有的标签不会被清掉。
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

          {results.length > 0 && <PreviewSummary results={results} />}
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
                写入 {results.length} 个文件
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
        没能从这个文件名里取出标签。确认文件名里有分隔符（默认按 “- _ / \ | ·” 切分），
        若是多段命名则改用上面的规则。
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

/** What the preview found, in one line.
 *
 *  This used to be a full per-file table with an editable cell per
 *  field. Removing it is a deliberate trade: this dialog now answers
 *  "does my rule work?" and nothing more, and hand-editing a value
 *  belongs to 批量编辑标签 — which has the per-field 「不修改」 control
 *  and can also DELETE a tag, neither of which this dialog could do.
 *
 *  The counts stay because "7 个文件" alone would let someone click
 *  写入 and discover afterwards that five of them matched nothing. A
 *  single line answers that before the click, which is the part of the
 *  table that was actually load-bearing.
 *
 *  `ambiguous` is counted separately from `unparsable` on purpose: an
 *  ambiguous row DID parse (it just guessed at the split), so it will
 *  be written, and lumping it in with unparsable would overstate the
 *  number of files being skipped. */
function PreviewSummary({ results }: { results: ParsedPreviewRow[] }) {
  const { total, matched, guessed, skipped } = tallyPreview(results);

  return (
    <div
      className="rounded border border-border px-3 py-2 text-xs space-y-1"
      data-testid="parse-filenames-summary"
    >
      <div>
        共 {total} 个文件：
        <span className="text-emerald-600 dark:text-emerald-400">匹配 {matched}</span>
        {guessed > 0 && (
          <span className="text-amber-600 dark:text-amber-400"> · 猜测 {guessed}</span>
        )}
        {skipped > 0 && (
          <span className="text-rose-600 dark:text-rose-400"> · 不匹配 {skipped}</span>
        )}
      </div>
      {skipped > 0 && (
        <div className="text-muted-foreground">
          不匹配的文件不会被写入，也不会删除已有标签。要给它们补标签，用「批量编辑标签」。
        </div>
      )}
      {guessed > 0 && (
        <div className="text-muted-foreground">
          「猜测」表示文件名被切成了多段、按「首段=艺术家、其余=标题」处理——写入的是这个猜测的结果。
        </div>
      )}
    </div>
  );
}
