// Auto-dismissing toast host. Mounted once at AppShell root so anywhere in
// the tree can broadcast a message via useNoticeStore.push(text, variant).
//
// Per-message TTL via cleanup effect keyed on the latest message id's
// presence \u2014 we deliberately don't re-run the timer on every render
// (which would reset earlier toasts). Each newly-appended toast gets its
// own 4-second window without clobbering older dismissals.

import { useEffect } from 'react';
import { useNoticeStore } from '@/store/useNoticeStore';

const TOAST_TTL_MS = 4000;

const variantPalette: Record<'info' | 'warn' | 'error', string> = {
  info: 'bg-card border-border text-foreground',
  warn: 'bg-amber-500/15 border-amber-500/40 text-amber-700 dark:text-amber-300',
  error: 'bg-red-500/15 border-red-500/40 text-red-700 dark:text-red-300',
};

export function ToastHost() {
  const messages = useNoticeStore((s) => s.messages);
  const dismiss = useNoticeStore((s) => s.dismiss);

  useEffect(() => {
    if (messages.length === 0) return;
    const lastId = messages[messages.length - 1].id;
    const timer = setTimeout(() => dismiss(lastId), TOAST_TTL_MS);
    return () => clearTimeout(timer);
  }, [messages, dismiss]);

  return (
    <div
      aria-live="polite"
      aria-atomic="true"
      className="fixed bottom-20 right-4 z-50 flex flex-col gap-2 items-end pointer-events-none"
    >
      {messages.map((m) => (
        <div
          key={m.id}
          className={`pointer-events-auto px-3 py-2 rounded-md border text-sm shadow-sm ${variantPalette[m.variant]}`}
        >
          {m.text}
        </div>
      ))}
    </div>
  );
}
