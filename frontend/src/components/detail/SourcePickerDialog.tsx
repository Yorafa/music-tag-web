// The dialog 智能刮削 opens before it searches: which sources to ask.
//
// It exists because the choice was previously either absent (the detail
// dialog searched one hardcoded source, and the user only found out by
// getting no candidates) or decorative (the batch toolbar let you tick four
// sources and then searched the first one). Either way the request did not
// match the screen, which is the kind of thing a user stops trusting.
//
// Mounted only while open, so the selection starts from the last used
// sources every time rather than from whatever the previous search left
// behind.

import { useState } from 'react';
import { Sparkles } from 'lucide-react';

import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import { SOURCES, toggleSourceSelection } from '@/components/common/tagSources';
import { cn } from '@/lib/utils';
import type { MusicSource } from '@/types';

interface Props {
  open: boolean;
  onOpenChange: (v: boolean) => void;
  /** Sources to start from — the ones the last search used. */
  initialSources: MusicSource[];
  /** Prefilled with the track's current title, so the common case is one
   *  click; editable because a filename is often not the title. */
  initialQuery: string;
  loading: boolean;
  onSearch: (sources: MusicSource[], query: string) => void;
}

export function SourcePickerDialog({
  open,
  onOpenChange,
  initialSources,
  initialQuery,
  loading,
  onSearch,
}: Props) {
  const [sources, setSources] = useState<MusicSource[]>(initialSources);
  const [query, setQuery] = useState(initialQuery);

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-sm">
        <DialogHeader>
          <DialogTitle className="text-base flex items-center gap-2">
            <Sparkles className="w-4 h-4 text-primary" />
            选择刮削音源
          </DialogTitle>
          <DialogDescription className="text-xs">
            勾选这次要检索的音源，结果会合并去重后按匹配度交错排列。至少保留一个。
          </DialogDescription>
        </DialogHeader>

        <div className="space-y-3">
          <div>
            <Label htmlFor="scrape-query" className="text-xs mb-1.5 block">
              检索关键词
            </Label>
            <Input
              id="scrape-query"
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === 'Enter' && query.trim()) {
                  onSearch(sources, query.trim());
                }
              }}
              className="h-9 text-sm"
              placeholder="歌名或关键字"
            />
          </div>

          <div className="grid grid-cols-2 gap-1.5">
            {SOURCES.map((s) => {
              const active = sources.includes(s.id);
              return (
                <button
                  key={s.id}
                  type="button"
                  aria-pressed={active}
                  onClick={() => setSources((prev) => toggleSourceSelection(prev, s.id))}
                  className={cn(
                    'flex items-center justify-between px-2 py-1.5 rounded text-xs border transition-colors',
                    active
                      ? 'bg-primary/10 border-primary text-primary font-medium'
                      : 'bg-surface-2 border-border text-muted-foreground hover:text-foreground',
                  )}
                >
                  <span className="truncate">{s.name}</span>
                  {active && <span className="text-[10px]">✓</span>}
                </button>
              );
            })}
          </div>
        </div>

        <DialogFooter>
          <Button
            variant="outline"
            onClick={() => onOpenChange(false)}
            disabled={loading}
            className="text-xs h-8"
          >
            取消
          </Button>
          <Button
            onClick={() => onSearch(sources, query.trim())}
            // A blank query searches nothing, and the empty result reads as
            // "no matches" rather than "you typed nothing".
            disabled={loading || sources.length === 0 || query.trim() === ''}
            className="text-xs h-8 font-semibold"
          >
            <Sparkles className={`w-3.5 h-3.5 ${loading ? 'animate-spin' : ''}`} />
            {loading ? '检索中…' : `开始检索 (${sources.length} 个源)`}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
