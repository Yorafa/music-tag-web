// 整理目录 — the third dialog in the 解析文件名 / 从标签改名 family, and
// the same shape on purpose: build a rule, read the plan, apply.
//
// What it replaced was three text boxes called first_dir and second_dir,
// which meant the tidy could only ever build a two-level tree out of bare
// field names. There was no way to ask for `${year} - ${album}` as one
// level, no way to ask for a third level, and no way to see the result
// before committing — a tidy moves the file, its cover and its .lrc, and
// rebuilds the tree for the whole list in one action with no per-file undo.
//
// So the rule is an ORDERED LIST of levels, the list length is the depth,
// and each level is itself a template that may mix tag values with fixed
// text:
//
//   [ 流派 ] [ ${artist} ] [ ${year} - ${album} ] [ ${discnumber} ]
//
// # The plan is derived here, not asked for
//
// The table below is a useMemo over the current root, the current levels
// and the rows. That is the whole reason the three bugs this replaced are
// gone: the levels and the root used to feed a REQUEST, so editing either
// left the table showing the previous rule until someone pressed 预览方案,
// and there was no way to tell a stale table from a current one. A
// derivation cannot be stale — there is no separate "refresh" to forget.
//
// # What the local plan deliberately does NOT say
//
// Whether the destination is already occupied, and whether two files want
// the same one, need a stat of the target path. The client has no data
// about the disk, so those are not checked and not claimed to be fine. Two
// of the ten visible rows landing on the same path IS checked, because
// that comparison is free and it is the collision people hit most. The
// dialog says the rest is on the operator, because a tidy is a move, not a
// copy, and the write reports its own per-file outcomes in 操作审计.

import { useCallback, useMemo, useState } from 'react';
import { Loader2, FolderTree, ArrowUp, ArrowDown, Plus, X } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogFooter,
  DialogDescription,
} from '@/components/ui/dialog';
import { useNoticeStore } from '@/store/useNoticeStore';
import { tidyFolder } from '@/api/client';
import {
  MAX_TIDY_LEVELS,
  TIDY_FIELDS,
  TIDY_FIELD_LABELS,
  TIDY_PRESETS,
  addLevel,
  canTidy,
  describeLevel,
  fieldsFromLevel,
  moveLevel,
  removeLevel,
  segmentsProblem,
  setLevel,
  toggleField,
  type TidyField,
} from './tidyRule';
import { localTidyPlan, type PlanRow } from './localPreview';
import {
  loadTidyRoot,
  loadTidySegments,
  rememberTidyRoot,
  rememberTidySegments,
} from '@/components/common/batchRuleCache';
import { cn } from '@/lib/utils';
import { previewTruncationNote } from '@/lib/previewLimit';
import type { WorklistRow } from '@/types';

interface TidyFolderDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  /** The selected worklist rows. The plan is rendered from these, and the
   *  apply covers all of them — not just the ten the table shows. */
  rows: WorklistRow[];
}

export function TidyFolderDialog({ open, onOpenChange, rows }: TidyFolderDialogProps) {
  // Both parts of a tidy are remembered (see common/batchRuleCache). The
  // level list is the more important of the two: it is a structure the
  // user assembled one 「加一层」 at a time, and it used to start over at
  // artist/album on every open, so tidying one album and then the next
  // meant rebuilding the tree twice.
  const [rootPath, setRootPathRaw] = useState<string>(loadTidyRoot);
  const [segments, setSegmentsRaw] = useState<string[]>(loadTidySegments);
  // One setter each, wrapped, because the levels change from six
  // different places (add, remove, move, edit, toggle a field, a preset).
  // A remembered rule that misses one of them is indistinguishable from no
  // remembering at all, and the call site that gets forgotten is whichever
  // one somebody added later.
  const setRootPath = useCallback((next: string) => {
    setRootPathRaw(next);
    rememberTidyRoot(next);
  }, []);
  const setSegments = useCallback((next: string[]) => {
    setSegmentsRaw(next);
    rememberTidySegments(next);
  }, []);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const problem = useMemo(() => segmentsProblem(segments), [segments]);

  const plan = useMemo(
    () => (problem === null ? localTidyPlan(rows, rootPath, segments) : []),
    [rows, rootPath, segments, problem],
  );

  // The table shows at most PREVIEW_LIMIT rows; the submit covers every
  // selected row. Two different numbers, both shown, because a count that
  // silently changes shape between the table and the button is how "整理 0
  // 个文件" read as "this thing is broken".
  const previewNote = previewTruncationNote(plan.length, rows.length);
  const wouldMove = plan.filter((p) => !p.unchanged).length;
  const submitPaths = rows.map((r) => r.fullPath);

  const handleApply = async () => {
    if (submitPaths.length === 0) {
      setError('没有可整理的文件。');
      return;
    }
    setLoading(true);
    try {
      const res = await tidyFolder({
        music_paths: submitPaths,
        root_path: rootPath.trim(),
        segments,
      });
      if (res?.result) {
        // The task has not run yet, so there is nothing to refresh — the
        // files are still where they were. Saying what to do afterwards is
        // the useful part: this queue holds files to work on, and a tidy
        // finishes them. 刷新收录 drops the rows that left their old
        // directory, which after a tidy is exactly the ones that moved; a
        // file the worker refused (its destination was occupied) is still
        // where it was, so its row stays and the audit log says why.
        useNoticeStore
          .getState()
          .push(
            '已提交目录整理异步任务，结果见操作审计。跑完后点「刷新收录」把这批文件从队列里移除，目录树刷新后可以重新找到它们',
            'info',
          );
        onOpenChange(false);
      } else {
        // The gateway's reason, not a generic failure. It names the actual
        // fault — a root outside the library, an unknown field — and
        // throwing that away would leave the fix unreachable from the UI.
        useNoticeStore.getState().push(res?.message || '目录整理提交失败', 'warn');
      }
    } catch (e) {
      useNoticeStore.getState().push(`目录整理提交失败：${(e as Error).message}`, 'error');
    } finally {
      setLoading(false);
    }
  };

  // Not gated on "the plan has rows that will move". A library ALREADY
  // filed the way the chosen rule files it has nothing to move, and that
  // is an answer, not a reason to disable the only button in the dialog.
  const canApply = !loading && canTidy(segments, rows.length);

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-2xl max-h-[85vh] overflow-y-auto">
        <DialogHeader>
          <DialogTitle className="text-base flex items-center gap-2">
            <FolderTree className="w-4 h-4 text-primary" />
            整理音乐文件夹
          </DialogTitle>
          <DialogDescription className="text-xs">
            逐层指定目录名：第几层就是第几级子目录，层内可混用标签与固定文字。
            下表在本地按当前规则实时算出来，每个文件去哪一目了然。
          </DialogDescription>
        </DialogHeader>

        <div className="space-y-3 py-2">
          <div>
            <Label className="text-xs mb-1 block">
              整理到哪个目录（留空 = 曲库根目录）
            </Label>
            <Input
              value={rootPath}
              onChange={(e) => setRootPath(e.target.value)}
              // Deliberately NOT a hint at the server's absolute path.
              // Nothing in the API reports MUSIC_DIR, so suggesting
              // "/app/media/" was a guess that read as a fact and sent
              // anyone whose mount differs down a 400. An empty field is
              // the library root and is what the dialog leads with.
              placeholder="留空则整理到曲库根目录"
              className="h-8 text-xs font-mono"
              data-testid="tidy-root-input"
            />
            <div className="text-[11px] text-muted-foreground mt-1">
              默认就是整理到曲库根目录，不需要知道服务端的绝对路径。要
              收到某个子目录再填它（曲库内的绝对路径）。
            </div>
          </div>

          <div className="space-y-2 rounded border border-border p-3">
            <div className="text-xs font-medium text-muted-foreground">常见结构</div>
            <div className="flex flex-wrap gap-1.5">
              {TIDY_PRESETS.map((p) => (
                <button
                  key={p.id}
                  type="button"
                  title={p.hint}
                  onClick={() => setSegments([...p.segments])}
                  className="rounded-md border border-border px-2 py-0.5 text-xs text-muted-foreground hover:bg-accent"
                  data-testid={`tidy-preset-${p.id}`}
                >
                  {p.label}
                </button>
              ))}
            </div>
          </div>

          {/* The level list. Order IS the depth, so the list is the whole
              control — there is no separate "how many levels" field that
              could disagree with it. */}
          <div className="space-y-2 rounded border border-border p-3">
            <div className="flex items-center justify-between">
              <div className="text-xs font-medium text-muted-foreground">
                目录层级（自上而下）
              </div>
              <Button
                variant="outline"
                size="sm"
                className="h-6 px-2 text-[11px]"
                onClick={() => setSegments(addLevel(segments))}
                disabled={segments.length >= MAX_TIDY_LEVELS}
                data-testid="tidy-add-level"
              >
                <Plus className="w-3 h-3" />
                加一层
              </Button>
            </div>

            {segments.map((seg, i) => (
              <LevelRow
                key={i}
                index={i}
                total={segments.length}
                value={seg}
                canRemove={segments.length > 1}
                onChange={(v) => setSegments(setLevel(segments, i, v))}
                onMove={(d) => setSegments(moveLevel(segments, i, d))}
                onRemove={() => setSegments(removeLevel(segments, i))}
                onToggleField={(f) => setSegments(toggleField(segments, i, f))}
              />
            ))}

            {/* No worked example here any more, and no second table. It used
                to render each level against one hardcoded track
                ("艺术家 → 周杰伦"), which was the same information this
                table gives for ten actual files — only less true of them. A
                fixed sample next to a real plan also invites the reader to
                believe the sample IS the plan. */}
          </div>

          {problem && (
            <div className="text-xs text-destructive" data-testid="tidy-error">
              {problem}
            </div>
          )}

          {error && <div className="text-sm text-destructive">{error}</div>}

          {plan.length > 0 && <PlanView rows={plan} />}
          {previewNote && (
            <div className="text-[11px] text-muted-foreground">{previewNote}</div>
          )}
          <div className="text-[11px] text-muted-foreground">
            本地预览只判断规则长什么样，以及这 {plan.length} 个文件彼此之间有没有重名；
            目标位置是否已被占用需要看磁盘，这里不查，冲突请自行核对。
          </div>
        </div>

        <DialogFooter showCloseButton>
          <span className="mr-auto text-[11px] text-muted-foreground self-center">
            {rows.length === 0
              ? '请至少选择一个文件'
              : problem !== null
                ? '层级有问题，改好后再提交'
                : plan.length > 0
                  ? `将整理选中的 ${rows.length} 个文件；上表 ${plan.length} 个里有 ${wouldMove} 个会移动`
                  : `将整理选中的 ${rows.length} 个文件`}
          </span>
          <Button onClick={handleApply} disabled={!canApply} data-testid="tidy-apply">
            {loading ? (
              <>
                <Loader2 className="w-4 h-4 mr-2 animate-spin" />
                提交中…
              </>
            ) : (
              // The count is what will actually be submitted, not the
              // plan's move tally — a button promising 0 files while
              // sending the whole selection is how "整理 0 个文件" read
              // as "this thing is broken".
              <>整理 {submitPaths.length} 个文件</>
            )}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

/** One level: a position, a free-text template, the field chips, and the
 *  reorder/remove controls.
 *
 *  The chips toggle. A level is a template that may be half fixed text, so
 *  clicking 专辑 on "${year} - " must give "${year} - ${album}" and leave
 *  the separator alone — and clicking it again must take the field back
 *  out, which is what the chip's pressed state has always promised. */
function LevelRow({
  index,
  total,
  value,
  canRemove,
  onChange,
  onMove,
  onRemove,
  onToggleField,
}: {
  index: number;
  total: number;
  value: string;
  canRemove: boolean;
  onChange: (v: string) => void;
  onMove: (delta: number) => void;
  onRemove: () => void;
  onToggleField: (f: TidyField) => void;
}) {
  const chosen = fieldsFromLevel(value);
  return (
    <div className="space-y-1.5 rounded border border-border/70 p-2">
      <div className="flex items-center gap-2">
        <span className="text-[11px] text-muted-foreground w-8 shrink-0">
          第 {index + 1} 层
        </span>
        <Input
          value={value}
          onChange={(e) => onChange(e.target.value)}
          placeholder={`${'${artist}'} 或 合辑`}
          className="h-7 text-xs font-mono"
          spellCheck={false}
          aria-label={`第 ${index + 1} 层目录名`}
          data-testid={`tidy-level-${index}`}
        />
        <div className="flex items-center gap-0.5 shrink-0">
          <Button
            variant="ghost"
            size="icon-xs"
            onClick={() => onMove(-1)}
            disabled={index === 0}
            title="上移一层"
            className="h-6 w-6"
          >
            <ArrowUp className="w-3 h-3" />
          </Button>
          <Button
            variant="ghost"
            size="icon-xs"
            onClick={() => onMove(1)}
            disabled={index === total - 1}
            title="下移一层"
            className="h-6 w-6"
          >
            <ArrowDown className="w-3 h-3" />
          </Button>
          <Button
            variant="ghost"
            size="icon-xs"
            onClick={onRemove}
            disabled={!canRemove}
            title="删除这一层"
            className="h-6 w-6"
          >
            <X className="w-3 h-3" />
          </Button>
        </div>
      </div>

      <div className="flex flex-wrap gap-1 pl-10">
        {TIDY_FIELDS.map((f) => {
          const on = chosen.includes(f);
          return (
            <button
              key={f}
              type="button"
              // Toggle, so a second click on a lit chip removes the field
              // instead of appending it a second time.
              onClick={() => onToggleField(f)}
              className={cn(
                'rounded border px-1.5 py-0.5 text-[10px] transition-colors',
                on
                  ? 'border-primary bg-primary/10 text-primary'
                  : 'border-border text-muted-foreground hover:bg-accent',
              )}
              aria-pressed={on}
              data-testid={`tidy-field-chip-${f}`}
            >
              {TIDY_FIELD_LABELS[f]}
            </button>
          );
        })}
      </div>

      <div className="flex items-center gap-2 pl-10 text-[11px]">
        <span className="text-muted-foreground truncate">{describeLevel(value)}</span>
      </div>
    </div>
  );
}

/** The plan. Paths, not counts — the operator is checking this against a
 *  filesystem, and a count they can only nod at is not a confirmation.
 *
 *  Both sides of the arrow are shown even for a row that will not move,
 *  because "why did this one not move" is the question the plan exists to
 *  answer, and a blank destination is a worse answer than a reason. */
function PlanView({ rows }: { rows: PlanRow[] }) {
  return (
    <div
      className="max-h-[40vh] overflow-y-auto rounded border border-border"
      data-testid="tidy-plan"
    >
      {rows.map((r) => (
        <PlanRowView key={r.id} row={r} />
      ))}
    </div>
  );
}

function PlanRowView({ row }: { row: PlanRow }) {
  const tone = row.clash ? 'text-rose-600 dark:text-rose-400' : 'text-foreground';
  const label = row.unchanged ? '已在位' : row.clash ? '重名' : '将移动';
  const detail =
    row.missing.length > 0
      ? `缺少 ${row.missing.map((m) => TIDY_FIELD_LABELS[m] ?? m).join('、')}`
      : '';
  return (
    <div
      className="flex items-center gap-2 px-2.5 py-1.5 border-b border-border/50 last:border-b-0 text-[11px]"
      data-testid={`tidy-plan-row-${label}`}
    >
      <span className="font-mono text-muted-foreground truncate max-w-[38%] shrink-0" title={row.fileName}>
        {row.fileName}
      </span>
      <span className="text-muted-foreground shrink-0">→</span>
      <span
        className={cn('font-mono truncate flex-1', row.unchanged ? 'text-muted-foreground' : tone)}
        title={row.result}
      >
        {row.result || '—'}
      </span>
      <span className={cn('shrink-0', row.unchanged ? 'text-muted-foreground' : tone)}>{label}</span>
      {detail !== '' && (
        <span className="shrink-0 text-muted-foreground max-w-[30%] truncate" title={detail}>
          {detail}
        </span>
      )}
    </div>
  );
}
