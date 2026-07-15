import { useState } from 'react';
import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogFooter } from '@/components/ui/dialog';
import { Button } from '@/components/ui/button';
import { Checkbox } from '@/components/ui/checkbox';
import { Label } from '@/components/ui/label';
import type { SourceInfo } from '@/types';

interface Props {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  /** Available sources, sorted by parent. Each entry is one checkbox row. */
  sources: SourceInfo[];
  /** Currently-enabled source names. Compared against sources[].name. */
  selected: string[];
  /** Apply — emits the full ordered list of selected names. */
  onConfirm: (names: string[]) => void;
}

export function SourcePickerModal({ open, onOpenChange, sources, selected, onConfirm }: Props) {
  const [draft, setDraft] = useState<string[]>(selected);

  // Sync draft when modal opens with new selection — guards against stale
  // draft state if the parent re-opens with a freshly-loaded enabled set.
  const handleOpenChange = (nextOpen: boolean) => {
    if (nextOpen) {
      setDraft([...selected]);
    }
    onOpenChange(nextOpen);
  };

  const toggle = (name: string) => {
    setDraft((prev) =>
      prev.includes(name) ? prev.filter((s) => s !== name) : [...prev, name]
    );
  };

  const handleConfirm = () => {
    onConfirm(draft);
    onOpenChange(false);
  };

  // Group by kind so the UI shows "音乐源" before "下载源" with a separator.
  const groups: { kind: 'tag' | 'download'; items: SourceInfo[] }[] = [
    { kind: 'tag', items: sources.filter((s) => s.kind === 'tag') },
    { kind: 'download', items: sources.filter((s) => s.kind === 'download') },
  ];

  return (
    <Dialog open={open} onOpenChange={handleOpenChange}>
      <DialogContent className="sm:max-w-xs">
        <DialogHeader>
          <DialogTitle className="text-sm">选择搜索源</DialogTitle>
        </DialogHeader>

        <div className="space-y-3 py-2 max-h-[60vh] overflow-y-auto">
          {groups.map((g, idx) => g.items.length === 0 ? null : (
            <div key={g.kind} className={idx > 0 ? 'border-t border-border pt-3' : ''}>
              <div className="text-[11px] text-muted-foreground mb-1.5 px-1">
                {g.kind === 'tag' ? '音乐标签源' : '下载源'}
              </div>
              <div className="space-y-1.5">
                {g.items.map((src) => (
                  <label
                    key={src.name}
                    className="flex items-center gap-3 cursor-pointer px-1 py-0.5 rounded hover:bg-muted/50 transition-colors"
                  >
                    <Checkbox
                      checked={draft.includes(src.name)}
                      onCheckedChange={() => toggle(src.name)}
                    />
                    <Label className="text-sm cursor-pointer">{src.display_name}</Label>
                    {!src.searchable && (
                      <span className="text-[10px] text-muted-foreground ml-auto">仅 ID3</span>
                    )}
                  </label>
                ))}
              </div>
            </div>
          ))}
        </div>

        <DialogFooter>
          <Button size="sm" onClick={handleConfirm} disabled={draft.length === 0}>
            确认
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
