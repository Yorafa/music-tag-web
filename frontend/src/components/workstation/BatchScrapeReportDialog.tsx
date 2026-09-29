// What the batch scrape did, row by row.
//
// The toolbar used to report "成功 N 首，未匹配 M 首" in a toast and drop
// the rest. Three different things hide behind 未匹配 — nobody has this
// track, the writer refused the row, a source was down — and the user
// cannot tell them apart, so the only way to find out was to run the scrape
// again with different sources and see whether the number moved.
//
// One row per track, with the source that answered, because "which source
// should I tick next time" is the question a batch scrape raises.

import { CheckCircle2, XCircle, AlertTriangle, MinusCircle, Sparkles } from 'lucide-react';

import { Button } from '@/components/ui/button';
import { Badge } from '@/components/ui/badge';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import { ScrollArea } from '@/components/ui/scroll-area';
import { SOURCES } from '@/components/common/tagSources';
import {
  sourceLabel,
  summarizeScrape,
  type ScrapeRowOutcome,
} from '@/components/workstation/scrapeReport';
import { cn } from '@/lib/utils';

interface Props {
  open: boolean;
  onOpenChange: (v: boolean) => void;
  rows: ScrapeRowOutcome[];
}

const STATUS_VIEW = {
  written: { icon: CheckCircle2, cls: 'text-emerald-500', label: '已写入' },
  no_match: { icon: MinusCircle, cls: 'text-muted-foreground', label: '未匹配' },
  refused: { icon: AlertTriangle, cls: 'text-amber-500', label: '写入被拒' },
  error: { icon: XCircle, cls: 'text-destructive', label: '失败' },
} as const;

export function BatchScrapeReportDialog({ open, onOpenChange, rows }: Props) {
  const summary = summarizeScrape(rows);

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-2xl max-h-[80vh] flex flex-col">
        <DialogHeader>
          <DialogTitle className="text-base flex items-center gap-2">
            <Sparkles className="w-4 h-4 text-primary" />
            批量刮削结果
          </DialogTitle>
          <DialogDescription className="text-xs">
            共 {summary.total} 首：已写入 {summary.written}
            {summary.noMatch > 0 && `，未匹配 ${summary.noMatch}`}
            {summary.refused > 0 && `，写入被拒 ${summary.refused}`}
            {summary.errored > 0 && `，失败 ${summary.errored}`}
            {summary.sourcesUsed.length > 0 && (
              <>
                {' '}· 命中音源：
                {summary.sourcesUsed.map((id) => sourceLabel(id, SOURCES)).join('、')}
              </>
            )}
          </DialogDescription>
        </DialogHeader>

        <ScrollArea className="flex-1 min-h-0 max-h-[52vh]">
          <div className="divide-y divide-border/40 pr-2">
            {rows.map((row) => {
              const view = STATUS_VIEW[row.status];
              const Icon = view.icon;
              return (
                <div key={row.fullPath} className="flex items-start gap-2 py-2 text-xs">
                  <Icon className={cn('w-3.5 h-3.5 mt-0.5 shrink-0', view.cls)} />
                  <div className="flex-1 min-w-0">
                    <div className="flex items-center gap-1.5 min-w-0">
                      <span className="truncate text-foreground" title={row.fullPath}>
                        {row.fileName}
                      </span>
                      <Badge variant="secondary" className="h-4 px-1 text-[10px] font-normal shrink-0">
                        {view.label}
                      </Badge>
                      {row.source && (
                        <Badge
                          variant="outline"
                          className="h-4 px-1 text-[10px] font-normal shrink-0"
                        >
                          {sourceLabel(row.source, SOURCES)}
                        </Badge>
                      )}
                    </div>
                    {row.applied?.title && (
                      <p className="text-[11px] text-muted-foreground truncate mt-0.5">
                        {row.applied.title}
                        {row.applied.artist ? ` · ${row.applied.artist}` : ''}
                        {row.applied.album ? ` · ${row.applied.album}` : ''}
                      </p>
                    )}
                    {row.reason && (
                      <p className="text-[11px] text-muted-foreground/90 mt-0.5 break-words">
                        {row.reason}
                      </p>
                    )}
                  </div>
                </div>
              );
            })}
          </div>
        </ScrollArea>

        <DialogFooter showCloseButton>
          <Button variant="outline" onClick={() => onOpenChange(false)} className="text-xs h-8">
            关闭
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
