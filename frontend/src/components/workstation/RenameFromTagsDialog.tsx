/* eslint-disable react-hooks/set-state-in-effect -- Same reason as
 * ParseFilenamesModal: fetch-on-mount plus reload-on-close, and the
 * rule's suggested fix (React Query / Suspense) adds runtime deps a
 * single dialog cannot justify.
 */

// 从标签改名 — the inverse of 解析文件名, and the same shape on purpose:
// pick a rule, try it, read the plan, apply. The second dialog to use
// this shape needs no explanation.
//
// The one thing it has that the parse dialog does not is a dry run that
// returns real per-file names. A parse is reversible in the sense that a
// bad row writes nothing; a rename moves files, and 500 moved files are
// not something to apply unread. So:
//
//   1. POST /api/tag/preview_rename_from_tags/ → rows of {old, new, status}.
//      Nothing is written. The plan is the product here; the apply button
//      is the boring part.
//   2. The operator reads it. Gaps (a field that was empty) and blocked
//      rows are called out, because "A -  - T.mp3" is a name the operator
//      chose to allow and also might not have meant.
//   3. Click 应用 → the server RE-PLANS every row rather than trusting
//      this one, and returns the authoritative result. The table is then
//      updated from that result, not from the preview.
//
// A template edit invalidates the plan: the apply button disables until
// a fresh preview lands, so nobody applies a plan they did not read.

import { useEffect, useMemo, useState } from 'react';
import { Loader2, FileEdit, AlertTriangle } from 'lucide-react';
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
  previewRenameFromTags,
  applyRenameFromTags,
  type RenamePlanRow,
} from '@/api/client';
import { useWorklistStore } from '@/store/useWorklistStore';
import { useLibraryStore } from '@/store/useLibraryStore';
import {
  RENAME_FIELDS,
  RENAME_FIELD_LABELS,
  RENAME_PRESETS,
  TRY_EXAMPLE,
  buildTemplate,
  canRequestPreview,
  describeTemplate,
  fieldsFromTemplate,
  hasWorkToDo,
  rowReason,
  tallyRename,
  templateProblem,
  tryTemplate,
} from './renameFromTags';
import { cn } from '@/lib/utils';

interface RenameFromTagsDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  /** Selected row full paths, relative to MUSIC_DIR. */
  selectedPaths: string[];
}

export function RenameFromTagsDialog({
  open,
  onOpenChange,
  selectedPaths,
}: RenameFromTagsDialogProps) {
  const [template, setTemplate] = useState('');
  /** The template the current `plan` was computed for. A mismatch means
   *  the operator has typed since the preview, and applying would do
   *  something they have not read. */
  const [plannedTemplate, setPlannedTemplate] = useState('');
  const [plan, setPlan] = useState<RenamePlanRow[]>([]);
  const [loading, setLoading] = useState<'preview' | 'apply' | null>(null);
  const [error, setError] = useState<string | null>(null);
  // Bumped to force a fresh preview. Clearing state alone is not enough:
  // a second `setPlan([])` on an already-empty plan is a no-op, React
  // does not re-render, and the effect never re-runs — so "重新预览"
  // would be dead exactly when a previous preview had failed.
  const [previewNonce, setPreviewNonce] = useState(0);

  const renameWorklistRow = useWorklistStore((s) => s.renameRow);
  const renameLibraryRow = useLibraryStore((s) => s.renameRow);

  const chosen = useMemo(() => fieldsFromTemplate(template), [template]);
  const problem = useMemo(() => templateProblem(template), [template]);
  const tried = useMemo(
    () => tryTemplate(template, TRY_EXAMPLE),
    [template],
  );
  const tally = useMemo(() => tallyRename(plan), [plan]);
  const planIsStale = plannedTemplate !== template;

  useEffect(() => {
    if (!open) {
      setPlan([]);
      setError(null);
      setLoading(null);
      setTemplate('');
      setPlannedTemplate('');
      return;
    }
    if (!canRequestPreview(template, problem, selectedPaths.length)) return;
    let cancelled = false;
    (async () => {
      setLoading('preview');
      setError(null);
      try {
        const res = await previewRenameFromTags(selectedPaths, template);
        if (cancelled) return;
        setPlan(res.rows ?? []);
        setPlannedTemplate(template);
      } catch (e: unknown) {
        if (cancelled) return;
        const msg = e instanceof Error ? e.message : String(e);
        setError(msg);
        setPlan([]);
        useNoticeStore.getState().push(`改名预览失败: ${msg}`, 'error');
      } finally {
        if (!cancelled) setLoading(null);
      }
    })();
    return () => {
      cancelled = true;
    };
    // `template` is deliberately absent: re-previewing on every
    // keystroke would stat the filesystem per character. 重新预览 is the
    // explicit trigger, and it moves the nonce.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, selectedPaths, previewNonce]);

  const setTemplateAndPlan = (t: string) => {
    setTemplate(t);
    // Typing invalidates the plan the operator was reading. The apply
    // button goes dead until they re-preview, which is the point.
    setPlannedTemplate('');
    setPlan([]);
  };

  /** Choosing a rule is a discrete, deliberate act — unlike typing — so
   *  it previews on the spot. Otherwise the operator picks the obvious
   *  preset and then has to find a second button to see what it did. */
  const chooseRule = (t: string) => {
    setTemplateAndPlan(t);
    if (t.trim() !== '') setPreviewNonce((n) => n + 1);
  };

  const handleApply = async () => {
    if (template.trim() === '') return;
    setLoading('apply');
    try {
      const res = await applyRenameFromTags(selectedPaths, template);
      const t = tallyRename(res.rows ?? []);
      // The response is the truth, not a receipt: the server re-planned,
      // so it may have blocked rows this preview had cleared.
      setPlan(res.rows ?? []);
      setPlannedTemplate(template);
      useNoticeStore.getState().push(
        `已改名 ${t.ok} 个，未变 ${t.noChange} 个，跳过 ${t.taken + t.blocked + t.failed} 个`,
        t.failed > 0 || t.blocked > 0 ? 'warn' : 'info',
      );
      // Only rows that actually moved need a table update; a no_change
      // row's new_name is its old name and writing it would churn the
      // store for nothing.
      for (const r of res.rows ?? []) {
        if (r.status !== 'ok') continue;
        const newPath = joinDir(r.path, r.new_name);
        renameWorklistRow(r.path, newPath, r.new_name);
        renameLibraryRow(r.path, newPath, r.new_name);
      }
      onOpenChange(false);
    } catch (e: unknown) {
      const msg = e instanceof Error ? e.message : String(e);
      setError(msg);
      useNoticeStore.getState().push(`改名失败: ${msg}`, 'error');
    } finally {
      setLoading(null);
    }
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-4xl">
        <DialogHeader>
          <DialogTitle>
            <span className="inline-flex items-center gap-2">
              <FileEdit className="w-4 h-4" />
              从标签改名
            </span>
          </DialogTitle>
          <DialogDescription>
            按文件里已有的标签重新生成文件名。写入前会先列出每个文件的旧名与新名，
            确认后再执行；已有标签不受影响。
          </DialogDescription>
        </DialogHeader>

        <div className="space-y-3 py-2">
          <div className="space-y-2 rounded border border-border p-3">
            <div className="text-xs font-medium text-muted-foreground">
              文件名里依次放什么？（点选，自动生成模板）
            </div>
            <div className="flex flex-wrap gap-1.5">
              {RENAME_FIELDS.map((f) => {
                const on = chosen.includes(f);
                return (
                  <button
                    key={f}
                    type="button"
                    onClick={() =>
                      chooseRule(
                        buildTemplate(
                          on ? chosen.filter((x) => x !== f) : [...chosen, f],
                        ),
                      )
                    }
                    className={cn(
                      'rounded-md border px-2 py-0.5 text-xs transition-colors',
                      on
                        ? 'border-primary bg-primary/10 text-primary'
                        : 'border-border text-muted-foreground hover:bg-accent',
                    )}
                    aria-pressed={on}
                    data-testid={`rename-field-chip-${f}`}
                  >
                    {RENAME_FIELD_LABELS[f]}
                  </button>
                );
              })}
            </div>
            {chosen.length > 0 && (
              <div className="text-xs text-muted-foreground">
                当前顺序：{chosen.map((f) => RENAME_FIELD_LABELS[f]).join(' → ')}
                （从左到右即文件名里的先后；再点一次可移除）
              </div>
            )}

            <div className="text-xs font-medium text-muted-foreground pt-1">
              常见命名
            </div>
            <div className="flex flex-wrap gap-1.5">
              {RENAME_PRESETS.map((p) => (
                <button
                  key={p.id}
                  type="button"
                  title={p.hint}
                  onClick={() => chooseRule(buildTemplate(p.fields))}
                  className="rounded-md border border-border px-2 py-0.5 text-xs text-muted-foreground hover:bg-accent"
                  data-testid={`rename-preset-${p.id}`}
                >
                  {p.label}
                </button>
              ))}
            </div>
          </div>

          <div className="flex items-start gap-2">
            <Input
              className="h-8 text-sm font-mono"
              value={template}
              placeholder="${artist} - ${title}"
              onChange={(e) => setTemplateAndPlan(e.target.value)}
              aria-label="文件名模板"
              spellCheck={false}
              data-testid="rename-template-input"
            />
            <Button
              variant="outline"
              size="sm"
              className="shrink-0 h-8"
              onClick={() => setPreviewNonce((n) => n + 1)}
              disabled={loading !== null || !canRequestPreview(template, problem, selectedPaths.length)}
              data-testid="rename-repreview"
            >
              重新预览
            </Button>
          </div>
          <div className="text-xs text-muted-foreground" data-testid="rename-rule-text">
            {describeTemplate(template)}
          </div>
          {problem && (
            <div className="text-xs text-destructive" data-testid="rename-template-error">
              {problem}
            </div>
          )}
          {template.trim() !== '' && (
            <div className="text-xs text-muted-foreground" data-testid="rename-try">
              举例：<code className="font-mono">{tried.name || '（空）'}</code>
              {tried.missing.length > 0 && (
                <span className="ml-1 text-amber-600 dark:text-amber-400">
                  （示例里缺 {tried.missing.map((f) => RENAME_FIELD_LABELS[f]).join('、')}，
                  空位会留在名字里）
                </span>
              )}
            </div>
          )}

          {loading === 'preview' && (
            <div className="flex items-center gap-2 text-sm text-muted-foreground">
              <Loader2 className="w-4 h-4 animate-spin" />
              正在计算改名方案…
            </div>
          )}
          {error && <div className="text-sm text-destructive">{error}</div>}

          {!loading && plan.length > 0 && (
            <PlanView rows={plan} tally={tally} />
          )}
        </div>

        <DialogFooter showCloseButton>
          <span className="mr-auto text-[11px] text-muted-foreground self-center">
            {planIsStale && plan.length > 0
              ? '模板已改，点「重新预览」查看新的方案'
              : selectedPaths.length === 0
                ? '请至少选择一个文件'
                : template.trim() === ''
                  ? '先选一条规则（点上面的预设，或点字段自己拼）'
                  : problem !== null
                    ? '模板有问题，改好后再预览'
                    : ''}
          </span>
          <Button
            onClick={handleApply}
            disabled={
              loading !== null ||
              template.trim() === '' ||
              problem !== null ||
              planIsStale ||
              !hasWorkToDo(tally)
            }
            data-testid="rename-apply"
          >
            {loading === 'apply' ? (
              <>
                <Loader2 className="w-4 h-4 mr-2 animate-spin" />
                改名中…
              </>
            ) : (
              <>改名 {tally.ok} 个文件</>
            )}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

/** The dry-run result: every file's old name and new name, plus a
 *  summary. Rows that will not move say why inline, because "why did
 *  this one not rename" is the question the plan exists to answer. */
function PlanView({
  rows,
  tally,
}: {
  rows: RenamePlanRow[];
  tally: ReturnType<typeof tallyRename>;
}) {
  return (
    <>
      <div
        className="rounded border border-border px-3 py-2 text-xs space-y-1"
        data-testid="rename-summary"
      >
        <div>
          共 {tally.total} 个：将改名{' '}
          <span className="text-emerald-600 dark:text-emerald-400">{tally.ok}</span>
          {tally.noChange > 0 && (
            <span className="text-muted-foreground"> · 已是该名字 {tally.noChange}</span>
          )}
          {tally.taken > 0 && (
            <span className="text-rose-600 dark:text-rose-400"> · 重名跳过 {tally.taken}</span>
          )}
          {tally.blocked > 0 && (
            <span className="text-rose-600 dark:text-rose-400"> · 无法改名 {tally.blocked}</span>
          )}
          {tally.failed > 0 && (
            <span className="text-rose-600 dark:text-rose-400"> · 失败 {tally.failed}</span>
          )}
        </div>
        {tally.withGaps > 0 && (
          <div className="text-amber-600 dark:text-amber-400">
            有 {tally.withGaps} 个文件缺少模板里的某个标签，空位会留在新名字里。
          </div>
        )}
      </div>

      <div
        className="max-h-[45vh] overflow-y-auto rounded border border-border"
        data-testid="rename-plan"
      >
        {rows.map((r) => (
          <div
            key={r.path}
            className="flex items-baseline gap-2 px-3 py-1.5 text-xs border-b border-border last:border-b-0"
          >
            <span className="font-mono text-muted-foreground truncate shrink-0 max-w-[38%]">
              {r.old_name}
            </span>
            <span className="text-muted-foreground shrink-0">→</span>
            <span
              className={cn(
                'font-mono truncate',
                r.status === 'ok' ? '' : 'text-muted-foreground line-through',
              )}
            >
              {r.status === 'ok' || r.status === 'no_change'
                ? r.new_name || r.old_name
                : r.old_name}
            </span>
            {r.status !== 'ok' && r.status !== 'no_change' && (
              <span className="ml-auto flex items-center gap-1 text-rose-600 dark:text-rose-400 shrink-0">
                <AlertTriangle className="w-3 h-3" />
                {rowReason(r)}
              </span>
            )}
            {r.status === 'no_change' && (
              <span className="ml-auto text-muted-foreground shrink-0">无需改动</span>
            )}
          </div>
        ))}
      </div>
    </>
  );
}

/** The new absolute-ish path for a renamed row. `path` is relative to
 *  MUSIC_DIR, and so is the result, which is what the stores hold. */
function joinDir(dir: string, name: string): string {
  return dir === '' ? name : `${dir}/${name}`;
}
