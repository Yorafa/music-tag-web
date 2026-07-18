// Header notification button — shows a bell icon + an unread badge fed
// by useNoticeStore.backlog. Clicking opens a small dropdown pinned to
// the button that lists the backlog (most-recent first) with timestamp +
// variant color + unread marker, plus a 「全部已读」 / 「清空」 action
// row at the bottom.
//
// The dropdown collapses on outside-click or Escape. We deliberately
// DO NOT use the shadcn <Dialog> here — this is an anchored popover, not
// a centered modal. A click-away overlay + a relative-positioned panel
// is the lightest portable implementation; the rest of the app already
// relies on raw pointer-down handlers (see DirPickerDrawer's breadcrumb)
// so this pattern is consistent. Implementation notes:
//
//   open state is local to this component — there's no global notion of
//   "the notifications panel is open"; the drawer just snapshots the
//   backlog at render time.
//
//   markAllRead is called on OPEN (not on close) so the badge updates
//   the instant the user acknowledges they're looking at the list. This
//   matches Slack / GitHub behavior and avoids the "open-close-open"
//   pattern where stale unread state confuses the user.

import { useEffect, useRef, useState, useCallback } from 'react';
import { Bell, CheckCheck, Trash2 } from 'lucide-react';
import { useNoticeStore, type NoticeRecord } from '@/store/useNoticeStore';
import { cn } from '@/lib/utils';

const VARIANT_DOT: Record<NoticeRecord['variant'], string> = {
  info: 'bg-blue-500',
  warn: 'bg-amber-500',
  error: 'bg-red-500',
};

const VARIANT_TEXT: Record<NoticeRecord['variant'], string> = {
  info: 'text-foreground',
  warn: 'text-amber-700 dark:text-amber-300',
  error: 'text-red-700 dark:text-red-300',
};

/** Compact relative timestamp for the dropdown rows. Stale-er than
 *  '60m ago' we just show HH:MM since the user almost certainly wants
 *  the wall clock at that horizon. */
function relTime(epoch: number, now: number): string {
  const s = Math.max(0, Math.floor((now - epoch) / 1000));
  if (s < 10) return '刚刚';
  if (s < 60) return `${s}s 前`;
  const m = Math.floor(s / 60);
  if (m < 60) return `${m}m 前`;
  const h = Math.floor(m / 60);
  if (h < 24) return `${h}h 前`;
  const d = new Date(epoch);
  const pad = (n: number) => n.toString().padStart(2, '0');
  return `${pad(d.getHours())}:${pad(d.getMinutes())}`;
}

export function NoticeCenterButton() {
  const [open, setOpen] = useState(false);
  const containerRef = useRef<HTMLDivElement>(null);
  // `nowTick` is a render-friendly stamp bumped by an interval while
  // the dropdown is open. Avoids calling Date.now()/new Date() inline
  // in the row render — the react-hooks/purity rule disallows that
  // because renders should be free of observable side effects.
  const [nowTick, setNowTick] = useState(() => Date.now());
  const backlog = useNoticeStore((s) => s.backlog);
  const markAllRead = useNoticeStore((s) => s.markAllRead);
  const clearBacklog = useNoticeStore((s) => s.clearBacklog);

  const unread = backlog.filter((r) => r.readAt === null).length;
  const newestFirst = [...backlog].reverse();

  // Click-away + Escape close. Registered only when open.
  useEffect(() => {
    if (!open) return;
    const onPointer = (e: PointerEvent) => {
      if (!containerRef.current?.contains(e.target as Node)) {
        setOpen(false);
      }
    };
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') setOpen(false);
    };
    document.addEventListener('pointerdown', onPointer);
    document.addEventListener('keydown', onKey);
    return () => {
      document.removeEventListener('pointerdown', onPointer);
      document.removeEventListener('keydown', onKey);
    };
  }, [open]);

  // Relative-timestamp ticker. Kicks in only while the dropdown is
  // open — runs every 15s to refresh '刚刚' → '12s 前' → '1m 前'.
  // We DON'T immediately refresh on open: we set `nowTick` in the
  // onToggle handler (a user-gesture handler, not an effect) so the
  // initial render of the open panel already reflects the current
  // epoch without triggering the react-hooks/set-state-in-effect rule.
  useEffect(() => {
    if (!open) return;
    const tick = setInterval(() => setNowTick(Date.now()), 15000);
    return () => clearInterval(tick);
  }, [open]);

  const onToggle = useCallback(() => {
    setOpen((prev) => {
      const next = !prev;
      // Mark-read on OPEN so the badge counts down to zero as the user
      // acknowledges the panel. If the panel was already open, we
      // don't touch state — close is just unmount.
      if (next) {
        setNowTick(Date.now());
        if (unread > 0) markAllRead();
      }
      return next;
    });
  }, [unread, markAllRead]);

  return (
    <div className="relative" ref={containerRef}>
      <button
        type="button"
        onClick={onToggle}
        title="通知中心"
        aria-label="通知中心"
        aria-expanded={open}
        className="relative inline-flex items-center justify-center w-9 h-9 rounded-md hover:bg-accent text-muted-foreground hover:text-foreground transition-colors"
      >
        <Bell className="w-4 h-4" />
        {unread > 0 && (
          <span
            className="absolute top-1 right-1.5 inline-flex items-center justify-center min-w-[16px] h-4 px-1 rounded-full bg-red-500 text-white text-[10px] font-semibold leading-none"
            aria-label={`未读 ${unread} 条`}
          >
            {unread > 99 ? '99+' : unread}
          </span>
        )}
      </button>
      {open && (
        <div
          role="dialog"
          aria-label="通知中心"
          className="absolute right-0 top-full mt-1 w-80 max-w-[calc(100vw-1rem)] rounded-lg border border-border bg-popover text-popover-foreground shadow-lg z-50 flex flex-col"
        >
          <div className="px-3 py-2 border-b border-border flex items-center justify-between">
            <span className="text-sm font-medium">通知</span>
            <span className="text-xs text-muted-foreground">
              {backlog.length === 0 ? '暂无' : `${backlog.length} 条历史`}
            </span>
          </div>
          <div className="max-h-80 overflow-y-auto">
            {newestFirst.length === 0 ? (
              <div className="px-4 py-8 text-center text-xs text-muted-foreground">
                暂无通知
              </div>
            ) : (
              <ul className="divide-y divide-border">
                {newestFirst.map((r) => (
                  <li
                    key={r.id}
                    className={cn(
                      'px-3 py-2 flex items-start gap-2',
                      r.readAt === null && 'bg-primary/5',
                    )}
                  >
                    <span
                      className={cn(
                        'mt-1.5 w-1.5 h-1.5 rounded-full shrink-0',
                        VARIANT_DOT[r.variant],
                      )}
                      aria-hidden
                    />
                    <div className="flex-1 min-w-0">
                      <p
                        className={cn(
                          'text-xs leading-snug break-words',
                          VARIANT_TEXT[r.variant],
                        )}
                      >
                        {r.text}
                      </p>
                      <p className="text-[10px] text-muted-foreground mt-0.5">
                        {relTime(r.createdAt, nowTick)}
                      </p>
                    </div>
                  </li>
                ))}
              </ul>
            )}
          </div>
          {backlog.length > 0 && (
            <div className="flex items-center justify-end gap-2 px-3 py-2 border-t border-border">
              <button
                type="button"
                onClick={clearBacklog}
                title="清空全部历史"
                className="inline-flex items-center gap-1 px-2 py-1 rounded text-xs text-muted-foreground hover:text-foreground hover:bg-accent transition-colors"
              >
                <Trash2 className="w-3 h-3" />
                清空
              </button>
              <button
                type="button"
                onClick={markAllRead}
                title="标记全部已读"
                className="inline-flex items-center gap-1 px-2 py-1 rounded text-xs text-muted-foreground hover:text-foreground hover:bg-accent transition-colors"
              >
                <CheckCheck className="w-3 h-3" />
                全部已读
              </button>
            </div>
          )}
        </div>
      )}
    </div>
  );
}
