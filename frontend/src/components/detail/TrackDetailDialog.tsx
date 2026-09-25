// The song-detail dialog — the app's single "what is in this file"
// surface, opened from a row tap in 音乐库 or 智能刮削 alike.
//
// One dialog, not two. The phone used to get a TagEditor form and the
// desktop got the TrackInspector column; that split is the bug. The
// column's content is the richer one (标签 / 候选 / 歌词 / 封面 / 指纹
// plus 智能刮削), so it is the one that survives, and the phone now
// opens the identical surface at the identical widths.
//
// Mounted at AppShell root so it is not unmounted by a section switch:
// opening a detail, navigating to 刮削 and coming back should not have
// silently discarded the half-edited lyrics.

import { Dialog, DialogContent, DialogHeader, DialogTitle } from '@/components/ui/dialog';
import { TrackInspector } from '@/components/detail/TrackInspector';
import { useDetailStore } from '@/store/useDetailStore';

export function TrackDetailDialog() {
  const target = useDetailStore((s) => s.target);
  const closeDetail = useDetailStore((s) => s.closeDetail);

  return (
    // `open` is derived from the target rather than stored separately:
    // "open with nothing to show" is not a state this UI can render.
    <Dialog
      open={target !== null}
      onOpenChange={(v) => {
        // Only react to a close. base-ui fires onOpenChange(true) for
        // open, and there is no trigger element — treating that as a
        // signal to open would clear the target we just set.
        if (!v) closeDetail();
      }}
    >
      <DialogContent
        className="sm:max-w-[min(56rem,calc(100%-2rem))] max-h-[92vh] overflow-hidden p-0"
        // The inline maxWidth mirrors the class on purpose. DialogContent
        // ships a `sm:max-w-sm` default, and this repo does not take
        // twMerge's conflict resolution on trust for a value that
        // decides whether the layout works at all.
        style={{ maxWidth: 'min(56rem, calc(100% - 2rem))' }}
      >
        <div className="flex flex-col h-[92vh] overflow-hidden">
          {/* TrackInspector's hero header already shows the title, artist
              and cover. This title exists for assistive tech (base-ui
              wires aria-labelledby to it) and is hidden to avoid saying
              it twice on screen. */}
          <DialogHeader className="sr-only">
            <DialogTitle>歌曲详情</DialogTitle>
          </DialogHeader>
          <div className="flex-1 min-h-0">
            <TrackInspector row={target} />
          </div>
        </div>
      </DialogContent>
    </Dialog>
  );
}
