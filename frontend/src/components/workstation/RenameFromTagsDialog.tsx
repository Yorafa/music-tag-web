// 从标签改名 — the inverse of 解析文件名, and the same shape on purpose:
// pick a rule, read the plan, apply.
//
// # The plan is derived here, not asked for
//
// The table is a useMemo over the current template and the rows. The
// server's preview used to be asked for on a timer and a 重新预览 click,
// which meant a table that could describe a template the operator had
// already edited, and an apply button gated on re-reading it. Rendering
// the plan from the rows' cached tags removes the whole class of problem:
// the table always describes the template in the box, and it updates the
// instant the template changes.
//
// # What the local plan deliberately does NOT say
//
// Whether the target name is already taken on disk needs a stat, and the
// client has no data about the disk. So that is not checked, and it is not
// implied to be fine. Two of the visible rows landing on the same name IS
// checked, because that comparison is free and it is the collision people
// hit most. The rest is the operator's to look at — the dialog says so.

import { useMemo, useState } from 'react';
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
import { applyRenameFromTags } from '@/api/client';
import { useWorklistStore } from '@/store/useWorklistStore';
import {
  RENAME_FIELDS,
  RENAME_FIELD_LABELS,
  RENAME_PRESETS,
  buildTemplate,
  canPlan,
  describeTemplate,
  fieldsFromTemplate,
  tallyRename,
  templateProblem,
} from './renameFromTags';
import { localRenamePlan, type PlanRow } from './localPreview';
import { cn } from '@/lib/utils';
import { previewTruncationNote } from '@/lib/previewLimit';
import type { WorklistRow } from '@/types';

interface RenameFromTagsDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  /** The selected worklist rows. The plan renders from these, and the
   *  apply covers all of them — not just the ten the table shows. */
  rows: WorklistRow[];
}

export function RenameFromTagsDialog({ open, onOpenChange, rows }: RenameFromTagsDialogProps) {
  const [template, setTemplate] = useState('');
  const [applying, setApplying] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const renameWorklistRow = useWorklistStore((s) => s.renameRow);

  const chosen = useMemo(() => fieldsFromTemplate(template), [template]);
  const problem = useMemo(() => templateProblem(template), [template]);
  const plan = useMemo(
    () => (template.trim() !== '' && problem === null ? localRenamePlan(rows, template) : []),
    [rows, template, problem],
  );

  // The plan is capped; the apply is not. Say which is which, or a ten-row
  // table reads as the whole job.
  const previewNote = previewTruncationNote(plan.length, rows.length);
  const wouldRename = plan.filter((p) => !p.unchanged).length;
  const submitPaths = rows.map((r) => r.fullPath);

  const handleApply = async () => {
    if (template.trim() === '') return;
    setApplying(true);
    try {
      const res = await applyRenameFromTags(submitPaths, template);
      // The response is the truth, not a receipt: the server re-planned
      // against the disk, so it may have refused rows this preview cleared.
      const t = tallyRename(res.rows ?? []);
      useNoticeStore.getState().push(
        `已改名 ${t.ok} 个，未变 ${t.noChange} 个，跳过 ${t.taken + t.blocked + t.failed} 个`,
        t.failed > 0 || t.blocked > 0 ? 'warn' : 'info',
      );
      // Only rows that actually moved need a table update; a no_change
      // row's new_name is its old name and writing it would churn the
      // store for nothing.
      for (const r of res.rows ?? []) {
        if (r.status !== 'ok') continue;
        renameWorklistRow(r.path, joinDir(r.path, r.new_name), r.new_name);
      }
      onOpenChange(false);
    } catch (e: unknown) {
      const msg = e instanceof Error ? e.message : String(e);
      setError(msg);
      useNoticeStore.getState().push(`改名失败: ${msg}`, 'error');
    } finally {
      setApplying(false);
    }
  };

  const canApply = canPlan(template, problem, rows.length) && !applying;

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-4xl">
        <DialogHeader>
          <DialogTitle>
            <span className="inline-flex items-center gap-2">
              <FileEdit className="w-4 h-4" />
              从标签改名
            </span>
          </DialogTitle>
          <DialogDescription>
            按文件里已有的标签重新生成文件名。下表在本地按当前模板实时算出来，
            已有标签不受影响。
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
                      setTemplate(
                        buildTemplate(on ? chosen.filter((x) => x !== f) : [...chosen, f]),
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
                  onClick={() => setTemplate(buildTemplate(p.fields))}
                  className="rounded-md border border-border px-2 py-0.5 text-xs text-muted-foreground hover:bg-accent"
                  data-testid={`rename-preset-${p.id}`}
                >
                  {p.label}
                </button>
              ))}
            </div>
          </div>

          <Input
            className="h-8 text-sm font-mono"
            value={template}
            placeholder="${artist} - ${title}"
            onChange={(e) => setTemplate(e.target.value)}
            aria-label="文件名模板"
            spellCheck={false}
            data-testid="rename-template-input"
          />
          <div className="text-xs text-muted-foreground" data-testid="rename-rule-text">
            {describeTemplate(template)}
          </div>
          {problem && (
            <div className="text-xs text-destructive" data-testid="rename-template-error">
              {problem}
            </div>
          )}
          {error && <div className="text-sm text-destructive">{error}</div>}

          {plan.length > 0 && <PlanView rows={plan} />}
          {previewNote && (
            <div className="text-[11px] text-muted-foreground" data-testid="rename-preview-note">
              {previewNote}
            </div>
          )}
          <div className="text-[11px] text-muted-foreground">
            本地预览只判断模板长什么样，以及这 {plan.length} 个文件彼此之间会不会撞名；
            目标名字是否已被占用需要看磁盘，这里不查，冲突请自行核对。
          </div>
        </div>

        <DialogFooter showCloseButton>
          <span className="mr-auto text-[11px] text-muted-foreground self-center">
            {rows.length === 0
              ? '请至少选择一个文件'
              : template.trim() === ''
                ? '先选一条规则（点上面的预设，或点字段自己拼）'
                : problem !== null
                  ? '模板有问题，改好后再提交'
                  : plan.length > 0
                    ? `将改名选中的 ${rows.length} 个文件；上表 ${plan.length} 个里有 ${wouldRename} 个会变`
                    : ''}
          </span>
          <Button onClick={handleApply} disabled={!canApply} data-testid="rename-apply">
            {applying ? (
              <>
                <Loader2 className="w-4 h-4 mr-2 animate-spin" />
                改名中…
              </>
            ) : (
              // The apply covers the WHOLE selection; the plan it is shown
              // above is capped to ten rows. Labelling the button with the
              // plan's tally would promise "改名 10 个文件" and then rename
              // two thousand, so the number here is always the number that
              // will actually be submitted.
              <>改名 {submitPaths.length} 个文件</>
            )}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

/** The plan: every file's old name and new name, both always shown, because
 *  "why did this one not change" is the question the plan exists to answer
 *  and a blank destination is a worse answer than a reason. */
function PlanView({ rows }: { rows: PlanRow[] }) {
  const withGaps = rows.filter((r) => r.missing.length > 0).length;
  return (
    <>
      <div
        className="rounded border border-border px-3 py-2 text-xs space-y-1"
        data-testid="rename-summary"
      >
        <div>共 {rows.length} 个：其中 {rows.filter((r) => !r.unchanged).length} 个会改名</div>
        {withGaps > 0 && (
          <div className="text-amber-600 dark:text-amber-400">
            有 {withGaps} 个文件缺少模板里的某个标签，空位会留在新名字里。
          </div>
        )}
      </div>

      <div
        className="max-h-[45vh] overflow-y-auto rounded border border-border"
        data-testid="rename-plan"
      >
        {rows.map((r) => (
          <div
            key={r.id}
            className="flex items-baseline gap-2 px-3 py-1.5 text-xs border-b border-border last:border-b-0"
          >
            <span className="font-mono text-muted-foreground truncate shrink-0 max-w-[38%]">
              {r.fileName}
            </span>
            <span className="text-muted-foreground shrink-0">→</span>
            <span
              className={cn(
                'font-mono truncate',
                r.clash ? 'text-rose-600 dark:text-rose-400' : '',
                r.unchanged ? 'text-muted-foreground' : '',
              )}
            >
              {r.result || '—'}
            </span>
            {r.clash && (
              <span className="ml-auto flex items-center gap-1 text-rose-600 dark:text-rose-400 shrink-0">
                <AlertTriangle className="w-3 h-3" />
                撞名
              </span>
            )}
            {r.unchanged && <span className="ml-auto text-muted-foreground shrink-0">无需改动</span>}
            {!r.clash && !r.unchanged && r.missing.length > 0 && (
              <span className="ml-auto text-amber-600 dark:text-amber-400 shrink-0">
                缺 {r.missing.map((m) => RENAME_FIELD_LABELS[m] ?? m).join('、')}
              </span>
            )}
          </div>
        ))}
      </div>
    </>
  );
}

/** The new path for a renamed row, from the server's own absolute `path`. */
function joinDir(dir: string, name: string): string {
  return dir === '' ? name : `${dir}/${name}`;
}
