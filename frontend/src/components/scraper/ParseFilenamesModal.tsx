// 解析文件名 round-trip modal. Triggered from the worklist's
// 「解析文件名」 button. The flow:
//
//   1. Pick a rule — chips or a preset; both GENERATE the pattern text,
//      so the box and the chips can never disagree.
//   2. Read the per-file plan, computed HERE from the selected rows'
//      own filenames. Nothing leaves the browser until 写入.
//   3. Click 写入 → POST /api/tag/apply_parsed_filenames/ with the paths
//      and the rule. The backend parses them again — the same
//      utils.PortParseFilename the local mirror follows — enqueues the
//      async worker, and returns the task_id which we surface as a toast.
//
// # Why the plan is local
//
// It used to be a request, and that cost a round trip on every rule the
// dialog invites you to change, over a selection that can be two thousand
// files. The answer being bought was ten rows of it. The write still goes
// to the server, and the server still decides every file's fate — this
// only moved the part that can be decided here.
//
// # There is no token, and no TTL
//
// The apply used to be bound to the preview's one-shot token, so an
// expired preview (10 min) turned into a 401 and a re-preview loop for a
// dialog the user might have left open over lunch. With the plan local,
// the apply is self-contained: paths in, task out.
//
// The rule logic (generation, the default rule, the RE2 limits, and the
// local mirror of the parser) lives in parseAssist.ts and is tested
// there, including against the Go parser's own fixture.
//
// This dialog deliberately does NOT offer per-file editing. It used to:
// there was a table with an editable cell per field. Removing it splits
// the job cleanly — 解析文件名 answers "does my rule work?", and
// 批量编辑标签 answers "what should this actually say?". The batch editor
// has the per-field 「不修改」 control and can DELETE a tag; this dialog
// could do neither, so its edit cells were a weaker version of a
// control that already exists elsewhere in the same toolbar.
//
// The pattern is REMEMBERED across reloads (see common/batchRuleCache) —
// a downloader's naming convention does not change between batches. It
// used to be lifted into WorkstationToolbar and passed down as
// `initialPattern` / `onPatternChange` instead, which meant it survived a
// re-open but not a refresh, and gave the rule two owners: the toolbar's
// copy and the modal's own useState, which reads its prop exactly once and
// then ignores it. The modal now owns the one copy.

import { useCallback, useMemo, useState } from 'react';
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
import { applyParsedFilenames } from '@/api/client';
import {
  PARSE_TAG_FIELDS,
  PARSE_TAG_LABELS,
  PATTERN_PRESETS,
  buildPattern,
  describeRule,
  fieldsFromPattern,
  localParsePlan,
  patternHelp,
  tallyPlan,
  type ParsePlanRow,
} from './parseAssist';
import { cn } from '@/lib/utils';
import { loadParsePattern, rememberParsePattern } from '@/components/common/batchRuleCache';
import { previewTruncationNoteBound } from '@/lib/previewLimit';
import type { WorklistRow } from '@/types';

interface ParseFilenamesModalProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  /** The selected worklist rows. The plan renders from these, and the
   *  write covers all of them — not just the ten the table shows. */
  rows: WorklistRow[];
}

export function ParseFilenamesModal({ open, onOpenChange, rows }: ParseFilenamesModalProps) {
  // Restored on mount, so a reload leaves the rule the way the user left
  // it. `useState(load)` rather than useEffect: a lazy initialiser runs
  // once, whereas an effect would read storage and then setState, which
  // renders the default rule for a frame before replacing it.
  const [pattern, setPattern] = useState<string>(loadParsePattern);
  const [applying, setApplying] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const help = useMemo(() => patternHelp(pattern), [pattern]);
  const patternErr = help.problem;
  // The chip row and the box are two views of one value, so the box is
  // the single source of truth: clicking a chip GENERATES the pattern
  // rather than editing it, so there is no way for the two to drift.
  const chosenFields = useMemo(() => fieldsFromPattern(pattern), [pattern]);

  // The plan, recomputed from the current rule. A useMemo and not a
  // fetch, which is what removes the second button and the "your plan is
  // out of date" state: a table derived from the rule in the box cannot
  // describe a different rule.
  const plan = useMemo(
    () => (patternErr === null ? localParsePlan(rows, pattern) : []),
    [rows, pattern, patternErr],
  );

  // The list is capped; the WRITE is not — it covers every selected row,
  // and the server parses all of them again when it runs. Say which is
  // which, or a ten-row table reads as the whole job.
  const planNote = previewTruncationNoteBound(plan.length, rows.length);
  const submitPaths = rows.map((r) => r.fullPath);

  const handleApply = async () => {
    if (submitPaths.length === 0) return;
    setApplying(true);
    try {
      const res = await applyParsedFilenames(submitPaths, {
        pattern: pattern.trim() || undefined,
      });
      useNoticeStore.getState().push(
        `已提交解析写入任务 (${res.row_count} 行, task=${res.task_id.slice(0, 8)}…)`,
        'info',
      );
      onOpenChange(false);
    } catch (e: unknown) {
      const msg = e instanceof Error ? e.message : String(e);
      setError(msg);
      useNoticeStore.getState().push(`应用解析失败: ${msg}`, 'error');
    } finally {
      setApplying(false);
    }
  };

  // Wrapped rather than calling both at each of the three places the rule
  // changes (a chip, a preset, the box): a rule the user set and then lost
  // is a worse bug than a missing write, and the call site that gets
  // forgotten is always whichever one somebody added later.
  const handlePatternEdit = useCallback((v: string) => {
    setPattern(v);
    rememberParsePattern(v);
  }, []);

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
            从文件名解析提取元数据并写入进文件。下表按当前规则在本地实时算出，
            看清了再写入；只填不删，已有的标签不会被清掉。
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
                      handlePatternEdit(next.length === 0 ? '' : buildPattern(next));
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
                  onClick={() => handlePatternEdit(buildPattern(pre.fields))}
                  className="rounded-md border border-border px-2 py-0.5 text-xs text-muted-foreground hover:bg-accent"
                  data-testid={`parse-preset-${pre.id}`}
                >
                  {pre.label}
                </button>
              ))}
            </div>
          </div>

          <Input
            className="h-8 text-sm font-mono"
            value={pattern}
            placeholder="留空 = 默认规则"
            onChange={(e) => handlePatternEdit(e.target.value)}
            aria-label="命名模板"
            spellCheck={false}
            data-testid="parse-pattern-input"
          />
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

          {error && <div className="text-sm text-destructive">提交失败: {error}</div>}

          {rows.length === 0 && (
            <div className="text-sm text-muted-foreground">
              （无内容 — 请至少选择一行 Worklist）
            </div>
          )}

          {plan.length > 0 && <PlanSummary rows={plan} />}
          {plan.length > 0 && <PlanList rows={plan} />}
          {planNote && (
            <div className="text-[11px] text-muted-foreground" data-testid="parse-preview-note">
              {planNote}
            </div>
          )}
        </div>

        <DialogFooter showCloseButton>
          <span className="mr-auto text-[11px] text-muted-foreground self-center">
            {rows.length === 0
              ? '请至少选择一个文件'
              : patternErr !== null
                ? '规则有问题，改好后再写入'
                : ''}
          </span>
          <Button
            onClick={handleApply}
            disabled={applying || submitPaths.length === 0 || patternErr !== null}
            data-testid="parse-filenames-apply"
          >
            {applying ? (
              <>
                <Loader2 className="w-4 h-4 mr-2 animate-spin" />
                提交中…
              </>
            ) : (
              // The whole selection, always: the plan above is capped to
              // ten rows, and labelling the button with its count would
              // promise "写入 10 个文件" and then write two thousand.
              <>写入 {submitPaths.length} 个文件</>
            )}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

/** What the plan found, in one line.
 *
 *  The counts come first because "7 个文件" alone would let someone click
 *  写入 and discover afterwards that five of them matched nothing.
 *
 *  `ambiguous` is counted and labelled separately from `unparsable` on
 *  purpose: an ambiguous row DID parse (it just guessed at the split),
 *  so it will be written, and lumping it in with unparsable would
 *  overstate the number of files being skipped. */
function PlanSummary({ rows }: { rows: ParsePlanRow[] }) {
  const { total, matched, guessed, skipped } = tallyPlan(rows);

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

/** The per-file plan: this filename becomes these tags. Read-only. */
const PARSE_STATUS_TEXT: Record<ParsePlanRow['status'], string> = {
  ok: '匹配',
  ambiguous: '猜测',
  unparsable: '不匹配',
};

const PARSE_STATUS_TONE: Record<ParsePlanRow['status'], string> = {
  ok: 'text-emerald-600 dark:text-emerald-400',
  ambiguous: 'text-amber-600 dark:text-amber-400',
  unparsable: 'text-rose-600 dark:text-rose-400',
};

function PlanList({ rows }: { rows: ParsePlanRow[] }) {
  return (
    <div
      className="max-h-[40vh] overflow-y-auto rounded border border-border"
      data-testid="parse-filenames-plan"
    >
      {rows.map((r) => (
        <div
          key={r.id}
          className="flex items-start gap-2 px-2.5 py-1.5 border-b border-border/50 last:border-b-0 text-[11px]"
          data-testid={`parse-plan-row-${r.status}`}
        >
          <span
            className="font-mono text-muted-foreground truncate max-w-[38%] shrink-0"
            title={r.fileName}
          >
            {r.fileName}
          </span>
          <span className="text-muted-foreground shrink-0">→</span>
          <span className="flex-1 flex flex-wrap gap-x-2 min-w-0">
            {r.tags.length === 0 ? (
              <span className={PARSE_STATUS_TONE[r.status]}>
                {r.status === 'unparsable' ? '不写入任何标签' : '没有解析出字段'}
              </span>
            ) : (
              r.tags.map(([f, v]) => (
                <span key={f} className="inline-flex items-baseline gap-1">
                  <span className="text-muted-foreground">{PARSE_TAG_LABELS[f]}</span>
                  <code className="font-mono">{v}</code>
                </span>
              ))
            )}
          </span>
          <span className={cn('shrink-0', PARSE_STATUS_TONE[r.status])}>
            {PARSE_STATUS_TEXT[r.status]}
          </span>
        </div>
      ))}
    </div>
  );
}
