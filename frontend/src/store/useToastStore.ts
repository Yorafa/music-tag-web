// DEPRECATED back-compat shim — re-exports useToastStore from
// useNoticeStore.ts. Plan B owns PlayButton.tsx, PlayerBar.tsx, and
// common/ToastHost.tsx (no edits allowed in PR2); they continue to
// `import { useToastStore } from '@/store/useToastStore'`. Forwarding
// the binding here keeps the tree green until Plan B migrates its
// components to useNoticeStore directly.
//
// Removal plan: Plan B's PR3 (local play mode rewrite) deletes this
// file alongside the legacy alias rename in those three components.

import { useNoticeStore, type NoticeVariant, type Notice } from './useNoticeStore';

export type ToastVariant = NoticeVariant;
/** @deprecated use {@link Notice} from useNoticeStore. */
export type Toast = Notice;
export const useToastStore = useNoticeStore;
