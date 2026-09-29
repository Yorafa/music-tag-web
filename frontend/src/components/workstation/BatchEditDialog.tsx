// Batch tag editing: one set of fields, applied to every selected file.
//
// This is the entry that did not exist. There were three ways to write tags
// and every one of them either had a human looking at exactly one file
// (TrackInspector) or had a machine supplying the values (批量智能刮削, 查重,
// 解析文件名). "I have selected forty files and I want to set the album on
// all of them" had no path at all.
//
// It is deliberately NOT inline per-row editing. Two reasons, both learned
// from what this app already does:
//
//   - Every row's identity is its path. A table where cells are editable has
//     to reconcile "what the user typed" with "where the row now lives" for
//     each row, and a rename moves it out from under them.
//   - A save is not a local edit. It writes files, moves sidecars, and can
//     rename. The inspector's explicit 保存 button is a deliberate speed
//     bump between intention and thirty-nine file writes; taking it away is
//     not a simplification.
//
// What the per-field 不修改 checkbox is for: the form is shared across rows,
// so "this box is empty" cannot mean "these forty files have no album" —
// most of them do. The checkbox is how the user says which fields they mean,
// and the default is 不修改, because the alternative default is a button
// that wipes forty genres.

import { useMemo, useState } from 'react';
import { Loader2, Tags, FileEdit } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Textarea } from '@/components/ui/textarea';
import { Checkbox } from '@/components/ui/checkbox';
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogFooter,
  DialogDescription,
} from '@/components/ui/dialog';
import { useNoticeStore } from '@/store/useNoticeStore';
import { useWorklistStore } from '@/store/useWorklistStore';
import { batchUpdateId3 } from '@/api/client';
import { isDedupeEnabled } from '@/utils/dedupe';
import type { WorklistRow } from '@/types';
import {
  baseNameOf,
  renamedPathsFromUpdate,
  sidecarWarningsFromUpdate,
  duplicateWarningsFromUpdate,
} from '@/components/detail/renameResult';
import {
  BATCH_TAG_FIELDS,
  initialBatchForm,
  buildBatchPayload,
  batchSelectData,
  type BatchFormState,
} from './batchEdit';

interface BatchEditDialogProps {
  /** The rows the edit applies to. */
  rows: WorklistRow[];
  /**
   * Unmounts the dialog.
   *
   *  There is deliberately no `open` prop: the toolbar renders this only
   *  while it is open, so the form state can be initialised once at mount
   *  and each opening starts clean. The alternative — an `open` prop plus a
   *  reset effect — is the shape that needs a lint exemption to explain
   *  why calling setState in an effect is fine here.
   */
  onClose: () => void;
}

export function BatchEditDialog({ rows, onClose }: BatchEditDialogProps) {
  const [form, setForm] = useState<BatchFormState>(() => initialBatchForm());
  const [renameTemplate, setRenameTemplate] = useState('');
  const [saving, setSaving] = useState(false);

  const setMusicInfo = useWorklistStore((s) => s.setMusicInfo);
  const setStatus = useWorklistStore((s) => s.setStatus);
  const renameWorklistRow = useWorklistStore((s) => s.renameRow);

  const payload = useMemo(
    () => buildBatchPayload(form, { renameTemplate, dedupeEnabled: isDedupeEnabled() }),
    [form, renameTemplate],
  );

  const setField = (key: string, patch: Partial<{ value: string; skip: boolean }>) => {
    setForm((prev) => ({ ...prev, [key]: { ...prev[key], ...patch } }));
  };

  const handleSave = async () => {
    if (!payload.hasChanges || saving) return;
    setSaving(true);
    // Captured before the request: a rename moves the files, so these paths
    // stop resolving the moment it lands.
    const targets = rows.map((r) => r.fullPath);

    try {
      const res = await batchUpdateId3({
        // Empty base path: the handler joins each row name onto it and
        // re-checks containment, so a selection spanning directories is one
        // request rather than one per parent.
        file_full_path: '',
        music_info: payload.music_info,
        select_data: batchSelectData(targets),
      });

      // What the user can act on. A batch that half-failed has to say
      // which half, or "成功 38 首" is indistinguishable from a save that
      // quietly ignored two files.
      for (const note of duplicateWarningsFromUpdate(res)) {
        useNoticeStore.getState().push(note, 'warn');
      }
      for (const note of sidecarWarningsFromUpdate(res)) {
        useNoticeStore.getState().push(note, 'warn');
      }

      const skipped = (res as { skipped?: unknown })?.skipped;
      const skippedCount = Array.isArray(skipped) ? skipped.length : 0;
      const done = (res as { done?: unknown })?.done;
      const doneCount = Array.isArray(done) ? done.length : 0;

      // The store update is NOT the wire payload. A cleared field is null
      // on the wire and '' in the store, because the row has to stop
      // showing a genre that is no longer in the file — writing null would
      // render as a broken value, and omitting the key would leave the old
      // one on screen, which is the one thing a successful clear must not
      // do. Fields nobody enabled are absent from both.
      const written: Record<string, string> = {};
      for (const field of payload.fields) {
        const value = payload.music_info[field.key];
        written[field.key] = typeof value === 'string' ? value : '';
      }

      const moved = renamedPathsFromUpdate(res);
      for (const row of rows) {
        if (payload.fields.length > 0) {
          setMusicInfo(row.fullPath, written);
        }
        setStatus(row.fullPath, 'scraped');

        const newPath = moved.get(row.fullPath);
        if (newPath) {
          const newFileName = baseNameOf(newPath);
          renameWorklistRow(row.fullPath, newPath, newFileName);
        }
      }

      if (moved.size > 0) {
        useNoticeStore
          .getState()
          .push(
            `已保存 ${doneCount} 首，其中 ${moved.size} 首按模板改名`,
            'info',
          );
      } else {
        useNoticeStore.getState().push(`已保存 ${doneCount} 首标签`, 'info');
      }
      if (skippedCount > 0) {
        useNoticeStore
          .getState()
          .push(
            `${skippedCount} 首被跳过：内容与库内文件完全一致。可在刮削设置中关闭「跳过重复文件」后重试`,
            'warn',
          );
      }

      onClose();
    } catch (e) {
      const msg = e instanceof Error ? e.message : String(e);
      useNoticeStore.getState().push(`批量保存失败: ${msg}`, 'error');
    } finally {
      setSaving(false);
    }
  };

  const fieldLabel = payload.fields.map((f) => f.label).join('、');

  return (
    <Dialog open onOpenChange={(next) => !next && onClose()}>
      <DialogContent className="sm:max-w-2xl max-h-[85vh] overflow-y-auto">
        <DialogHeader>
          <DialogTitle className="text-base flex items-center gap-2">
            <Tags className="w-4 h-4 text-primary" />
            批量编辑标签（{rows.length} 首）
          </DialogTitle>
          <DialogDescription className="text-xs">
            勾掉「不修改」的字段才会写入：留空并勾掉即为<strong>清空该标签</strong>。
            没有勾掉任何字段时不会发起请求。
          </DialogDescription>
        </DialogHeader>

        <div className="space-y-2 py-2">
          {BATCH_TAG_FIELDS.map((field) => {
            const edit = form[field.key];
            const enabled = !edit.skip;
            return (
              <div
                key={field.key}
                className="flex items-start gap-3 rounded-md border border-border/60 px-3 py-2"
              >
                <label className="flex items-center gap-2 pt-2 shrink-0 cursor-pointer">
                  <Checkbox
                    checked={edit.skip}
                    onCheckedChange={(v) => setField(field.key, { skip: v === true })}
                    aria-label={`不修改 ${field.label}`}
                  />
                  <span className="text-[11px] text-muted-foreground whitespace-nowrap">
                    不修改
                  </span>
                </label>

                <div className="flex-1 min-w-0 space-y-1">
                  <span className="text-xs text-muted-foreground">{field.label}</span>
                  {field.multiline ? (
                    <Textarea
                      value={edit.value}
                      disabled={edit.skip}
                      onChange={(e) => setField(field.key, { value: e.target.value })}
                      placeholder={enabled ? '留空 = 清空该标签' : '不修改'}
                      className="text-xs min-h-[60px]"
                    />
                  ) : (
                    <Input
                      value={edit.value}
                      disabled={edit.skip}
                      onChange={(e) => setField(field.key, { value: e.target.value })}
                      placeholder={enabled ? '留空 = 清空该标签' : '不修改'}
                      className="text-xs h-8"
                    />
                  )}
                </div>
              </div>
            );
          })}
        </div>

        <div className="rounded-md border border-border/60 px-3 py-2 space-y-1">
          <div className="flex items-center gap-2">
            <FileEdit className="w-3.5 h-3.5 text-muted-foreground" />
            <span className="text-xs text-muted-foreground">
              同时按模板改名（默认关闭）
            </span>
          </div>
          <Input
            value={renameTemplate}
            onChange={(e) => setRenameTemplate(e.target.value)}
            placeholder="${artist} - ${title}　（留空 = 不改名）"
            className="text-xs h-8 font-mono"
          />
          <p className="text-[10px] text-muted-foreground/80">
            模板对每首文件分别展开，改名由服务端执行并做重名检查。
          </p>
        </div>

        <DialogFooter>
          <span className="mr-auto text-[11px] text-muted-foreground self-center">
            {payload.hasChanges
              ? `将修改：${renameTemplate.trim() ? '文件名 + ' : ''}${fieldLabel}`
              : '未勾选任何字段'}
          </span>
          <Button
            variant="ghost"
            size="sm"
            onClick={onClose}
            disabled={saving}
          >
            取消
          </Button>
          <Button size="sm" onClick={handleSave} disabled={!payload.hasChanges || saving}>
            {saving ? <Loader2 className="w-3.5 h-3.5 animate-spin" /> : null}
            <span>{saving ? '正在保存…' : '保存标签'}</span>
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
