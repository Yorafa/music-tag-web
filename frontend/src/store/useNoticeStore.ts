// Lightweight notification queue — semantics widened from "toast" to
// "notice" since the host could grow into non-toast surfaces (inline
// banner, status-bar entry) without breaking call sites that only read
// `.push(text, variant)` / `.dismiss(id)`.
//
// Sized to fit one info / warn / error broadcast per interactive
// moment; we don't model per-message action callbacks or stacking
// limits. If a future UX needs those, swap this file for sonner / react-
// hot-toast without changing call sites.
//
// Two-tier storage:
//   - `messages`  → active toasts shown by <ToastHost>; auto-dismissed
//                   after 4s by the host's effect. Same surface the
//                   regression suite expected.
//   - `backlog`   → persistent history of every notice ever pushed —
//                   survives toast dismissal and feeds the header's
//                   NoticeCenter dropdown so a dismissed toast (or a
//                   background enqueue-result callers never saw) can be
//                   reviewed later. Capped at MAX_BACKLOG entries; old
//                   records fall off FIFO to avoid unbounded growth.
//
// Step 2 of Plan A. The old useToastStore.ts file is left in place as a
// back-compat shim: it re-exports useToastStore from THIS file so Plan
// B's owned components (PlayButton.tsx, PlayerBar.tsx,
// common/ToastHost.tsx — listed under "Plan B touches, don't edit")
// don't need touching in this PR. Plan B will scrub the legacy alias
// when it migrates its surface to <NoticeHost>.

import { create } from 'zustand';

export type NoticeVariant = 'info' | 'warn' | 'error';

export interface Notice {
  id: string;
  text: string;
  variant: NoticeVariant;
  /** Epoch ms when the notice was pushed. Used by the NoticeCenter to
   *  render relative timestamps ('刚刚' / '12s 前' / '3m 前'). The
   *  toast renderer doesn't need this — toasts are fire-and-forget. */
  createdAt: number;
}

/** A backlog record is just a Notice pinning whether the user has
 *  "seen" it via the NoticeCenter dropdown yet. `readAt === null`
 *  drives the unread badge counter. */
export interface NoticeRecord extends Notice {
  readAt: number | null;
}

/** Internal name for the variant union. Old `ToastVariant` is kept
 *  exported as an alias so any straggler import still compiles (see
 *  useToastStore.ts). */
export type ToastVariant = NoticeVariant;

/** Cap on backlog length. Old records fall off FIFO. Picked larger
 *  than any realistic session-long burst so the user can scroll back
 *  to "what happened when I clicked 添加音乐 two minutes ago" without
 *  us ballooning memory if a runaway loop keeps pushing notices. */
const MAX_BACKLOG = 200;

interface NoticeState {
  /** Active toast queue — drained by ToastHost's auto-dismiss. */
  messages: Notice[];
  /** Persistent history — feeds the header NoticeCenter dropdown. */
  backlog: NoticeRecord[];
  push: (text: string, variant?: NoticeVariant) => void;
  dismiss: (id: string) => void;
  /** Mark every backlog record as read (readAt = now). Called when
   *  the user opens the NoticeCenter dropdown. No-op if everything
   *  is already read — keeps the selector cheap. */
  markAllRead: () => void;
  /** Wipe the backlog entirely. The toast queue is left alone so an
   *  in-flight toast still finishes its 4s window. */
  clearBacklog: () => void;
  /** Select unread backlog count. Plain getter — no subscription. */
  unreadCount: () => number;
}

export const useNoticeStore = create<NoticeState>((set, get) => ({
  messages: [],
  backlog: [],
  push: (text, variant = 'info') => {
    const id = crypto.randomUUID();
    const createdAt = Date.now();
    const notice: Notice = { id, text, variant, createdAt };
    const record: NoticeRecord = { ...notice, readAt: null };
    set((s) => ({
      messages: [...s.messages, notice],
      backlog: [...s.backlog, record].slice(-MAX_BACKLOG),
    }));
  },
  dismiss: (id) =>
    set((s) => ({ messages: s.messages.filter((m) => m.id !== id) })),
  markAllRead: () => {
    const now = Date.now();
    set((s) => ({
      backlog: s.backlog.map((r) =>
        r.readAt === null ? { ...r, readAt: now } : r,
      ),
    }));
  },
  clearBacklog: () => set({ backlog: [] }),
  unreadCount: () => get().backlog.filter((r) => r.readAt === null).length,
}));
