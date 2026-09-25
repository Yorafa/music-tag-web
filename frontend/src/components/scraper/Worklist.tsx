// The Worklist table itself. Each row shows: cover (resolved via the
// shared cover helper) + filename + status badge + kebab menu.
//
// Row click: open the mode-agnostic detail Dialog (TagEditor in the
// dual-panel layout). Hydrates the editor's musicInfo from the row's
// lazy cache (`musicInfo` field) if present, OR fetches /api/music_id3/
// inline so the editor doesn't open empty.
//
// Kebab menu: Play (build a `{kind:'local', fileName, filePath:fullPath}`
// PlayerTrack per usePlayerStore's discriminated union — pitfall #12),
// Detail (same as row click), Remove (drop the row from useWorklistStore).
//
// Performance consideration: every visible row subscribes to the same
// useWorklistStore selector. Each row's selector returns a primitive
// (boolean / one status field), so the store's zustand middleware
// emits re-renders per row ONLY when THAT row's relevant field
// changes — flipping a row's status re-renders just that row's status
// badge.
//
// ───── Plan C.3 grouping integration ──────────────────────────────────
//
// When `grouping !== 'none'`, we derive `GroupedRow[]` from the visible
// (post-filter) rows:
//   - each group key is `<axis>:<label>` so a label that exists in
//     both `album` and `artist` planes can't collide (a real CJK case:
//     a row with `album: "Compilation"` AND `artist: "Compilation"` no
//     longer merges under one header when switching axes).
//   - rows with no musicInfo for the active axis fall into a single
//     "unknown album" / "unknown artist" group. NOT silently dropped
//     (Plan C.3 § step 2 invariant: 未 scrape 行 → 'unknown *' group).
//   - empty groups (filter excludes every row in the group) are NOT
//     rendered at all (Plan C.3 § step 3: 空 group 不渲染).
//   - selection state is keyed by row.id (== fullPath), so a row's
//     checkbox stays in sync across group collapses / grouping-axis
//     switches.

import { useMemo, useState, type CSSProperties } from 'react';
import {
  MoreHorizontal,
  Play,
  PencilLine,
  Trash2,
  Music,
} from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Badge } from '@/components/ui/badge';
import { ScrollArea } from '@/components/ui/scroll-area';
import {
  DropdownMenu,
  DropdownMenuTrigger,
  DropdownMenuContent,
  DropdownMenuItem,
} from '@/components/ui/dropdown-menu';
import { useWorklistStore, type WorklistGrouping } from '@/store/useWorklistStore';
import { editorActions, useEditorStore } from '@/store/useEditorStore';
import { useNoticeStore } from '@/store/useNoticeStore';
import { usePlayerStore } from '@/store/usePlayerStore';
import { readTagsFromPath } from '@/lib/id3Reader';
import {
  COVER_PLACEHOLDER_GRADIENTS,
  inspectCoverSrc,
  resolveCoverSrc,
} from '@/utils/cover';
import { needsMusicInfoRefetch } from '@/utils/persistMusicInfo';
import { toInitialChar, toTrimmedString } from '@/utils/string';
import { cn } from '@/lib/utils';
import { buildMediaUrl as sharedBuildMediaUrl } from '@/lib/mediaUrl';
import { GroupHeaderRow } from './GroupHeaderRow';
import type { MusicTagInfo, WorklistRow } from '@/types';

const STATUS_BADGE: Record<'pending' | 'scraped' | 'failed', string> = {
  pending: 'bg-zinc-500/10 text-zinc-400 border-zinc-500/30',
  scraped: 'bg-emerald-500/10 text-emerald-400 border-emerald-500/30',
  failed: 'bg-red-500/10 text-red-400 border-red-500/30',
};
const STATUS_LABEL: Record<'pending' | 'scraped' | 'failed', string> = {
  pending: '待刮',
  scraped: '已刮',
  failed: '失败',
};

/** Build the media URL for a /media/<encoded-rel-path>/<encoded-name>
 *  request. Delegated to the shared src/lib/mediaUrl.ts util — see the
 *  file header for the URL shape rationale. */
function buildMediaUrl(fullPath: string): string {
  return sharedBuildMediaUrl(fullPath);
}

/** Resolve a row to its human-readable label on the active grouping axis.
 *  Rows with no musicInfo fall into a single "unknown *" group per Plan
 *  C.3 § step 2: NOT silently dropped. */
function rowToGroupLabel(
  row: WorklistRow,
  axis: 'album' | 'artist',
): string {
  const cell = row.musicInfo?.[axis];
  const trimmed = typeof cell === 'string' ? cell.trim() : '';
  return trimmed.length > 0 ? trimmed : `unknown ${axis}`;
}

/** Discriminated union of items in the flattened (group-aware) Worklist
 *  render. `kind: 'header'` precedes all data rows of its group; the
 *  `groupKey` matches what `useWorklistStore.collapsedGroups` carries. */
type GroupedRow =
  | { kind: 'header'; groupKey: string; label: string; count: number; expanded: boolean }
  | { kind: 'data'; row: WorklistRow };

/** Pure derivation: visible-after-filter rows → GroupedRow[] for the
 *  current grouping axis (or flat-equivalent when grouping === 'none').
 *
 *  Side note on choice of useMemo: with `rows.length` typically in the
 *  10²–10⁴ range and a single O(n) walk per change, memoizing on
 *  (rows, filter, grouping) is plenty. We re-run on group-collapse
 *  toggles too because `expanded` lives on the derived item — the
 *  collapse state is used inside the render to filter out collapsed
 *  rows downstream. */
function deriveGrouped(
  rows: WorklistRow[],
  filter: 'all' | 'pending' | 'scraped' | 'failed',
  grouping: WorklistGrouping,
  collapsedGroups: Set<string>,
): GroupedRow[] {
  const visible = rows.filter((r) =>
    filter === 'all' ? true : r.status === filter,
  );
  if (visible.length === 0) return [];
  if (grouping === 'none') {
    return visible.map((row) => ({ kind: 'data', row }));
  }
  const axis: 'album' | 'artist' = grouping;
  // Build a stable Map<groupKey, {label, rows[]}> insertion order =
  // first-row-seen order. Matters for "albums A, B, C" where adding a
  // row matching album B later should not yeet A-C order around.
  const map = new Map<string, { label: string; rows: WorklistRow[] }>();
  for (const row of visible) {
    const label = rowToGroupLabel(row, axis);
    const groupKey = `${axis}:${label}`;
    const bucket = map.get(groupKey);
    if (bucket) bucket.rows.push(row);
    else map.set(groupKey, { label, rows: [row] });
  }
  const out: GroupedRow[] = [];
  for (const [groupKey, { label, rows: groupRows }] of map) {
    const expanded = !collapsedGroups.has(groupKey);
    out.push({ kind: 'header', groupKey, label, count: groupRows.length, expanded });
    if (expanded) {
      for (const row of groupRows) out.push({ kind: 'data', row });
    }
  }
  return out;
}

/** Cover thumbnail matching SearchResults' CoverThumb semantics:
 *  same gradient palette, same array-indexed seed (via the AudioRow's
 *  fullPath-hash), same empty-payload + oversized short-circuit, same
 *  `imgFailed` fallback, same first-letter placeholder (pitfall #8:
 *  resolveCoverSrc is mandatory before reading album_img). */
function RowCover({
  src,
  id,
  title,
}: {
  src?: string;
  id: number;
  title: string;
}) {
  const [imgFailed, setImgFailed] = useState(false);
  const { isEmptyPayload, isOversized, tooltip } = inspectCoverSrc(src);
  const showImg = !isEmptyPayload && !isOversized && !imgFailed;
  const initial = toInitialChar(title);
  const bg: CSSProperties = {
    background: COVER_PLACEHOLDER_GRADIENTS[id % COVER_PLACEHOLDER_GRADIENTS.length],
  };

  return (
    <div className="w-10 h-10 rounded overflow-hidden bg-muted shrink-0">
      {showImg ? (
        <img
          src={src}
          alt="封面"
          className="w-full h-full object-cover"
          onError={() => setImgFailed(true)}
        />
      ) : (
        <div
          className="w-full h-full flex items-center justify-center text-white font-semibold text-sm"
          style={bg}
          title={tooltip}
        >
          {initial}
        </div>
      )}
    </div>
  );
}

/** Stable integer derived from a Worklist row id (= fullPath). Same
 *  hashing trick used by SearchResults so the same file's cover
 *  gradient slot stays consistent across Worklist + TagEditor. */
function stableIdFromRow(rowId: string): number {
  let h = 0;
  for (let i = 0; i < rowId.length; i++) h = (h * 31 + rowId.charCodeAt(i)) | 0;
  return Math.abs(h);
}

interface RowProps {
  fullPath: string;
  fileName: string;
  status: 'pending' | 'scraped' | 'failed';
  musicInfo: Partial<MusicTagInfo> | undefined;
  selected: boolean;
}

function WorklistRowView({
  fullPath,
  fileName,
  status,
  musicInfo,
  selected,
}: RowProps) {
  const playTrack = usePlayerStore((s) => s.playTrack);
  const setSelected = useWorklistStore((s) => s.toggleSelected);
  const removeIds = useWorklistStore((s) => s.remove);
  const coverSrc = resolveCoverSrc(musicInfo ?? null);
  const coverId = stableIdFromRow(fullPath);
  const artist = musicInfo?.artist ? toTrimmedString(musicInfo.artist) : '';

  /** Open the detail Dialog with this row's id3 hydrated.
   *  - If the row has a cached `musicInfo` snapshot, hydrate from it.
   *  - Otherwise fire an inline /api/music_id3/ to load whatever the
   *    file currently has on disk, then seed the row's cache so the
   *    next click on the same row is instant. Mirrors the pre-refactor
   *    FileBrowser cell-click path. */
  const openEditor = async () => {
    editorActions.setSelectedFile(fileName);
    editorActions.setFullPath(fullPath);
    editorActions.setFadeShowDetail(false);
    editorActions.setSongList([]);
    // Optimistic hydration so the editor mounts without a blank-field
    // flash; the inline fetch below overrides it once it resolves.
    editorActions.setMusicInfo(musicInfo ?? {});
    editorActions.setEditorOpen(true);

    // `needsMusicInfoRefetch` rather than "any non-null field" — lightweight
    // caches from localStorage keep title/artist but strip data-URI covers,
    // so we still re-fetch to restore album art (and any other dropped
    // heavy fields) on open.
    if (needsMusicInfoRefetch(musicInfo)) {
      try {
        const info = await readTagsFromPath(fullPath);
        // Race guard: a newer click may have already switched the
        // editor's fullPath/selectedFile to a different row. Skip
        // the apply in that case so an in-flight slow response for
        // Row A can't overwrite the freshly-mounted Row B.
        const stillCurrent =
          useEditorStore.getState().fullPath === fullPath &&
          useEditorStore.getState().selectedFile === fileName;
        if (!stillCurrent) return;
        if (Object.values(info).some(v => v != null)) {
          editorActions.setMusicInfo(info);
          // Seed the row's lazy cache so subsequent clicks skip the
          // network round-trip.
          useWorklistStore.getState().setMusicInfo(fullPath, info);
        }
      } catch (err) {
        // Network/transport/parse error: surface so the user can
        // tell apart "fetch failed" from "tags actually empty".
        const msg = err instanceof Error ? err.message : String(err);
        useNoticeStore.getState().push(`读取标签失败: ${msg}`, 'warn');
      }
    }
  };

  const play = (e: React.MouseEvent) => {
    e.stopPropagation();
    playTrack({
      id: `local-${fullPath}`,
      url: buildMediaUrl(fullPath),
      title: fileName,
      artist,
      cover: coverSrc,
      source: { kind: 'local', fileName, filePath: fullPath },
    });
  };

  const detail = (e: React.MouseEvent) => {
    e.stopPropagation();
    void openEditor();
  };

  const remove = (e: React.MouseEvent) => {
    e.stopPropagation();
    removeIds([fullPath]);
  };

  return (
    <div
      onClick={openEditor}
      className={cn(
        // 5-column grid: [checkbox][48-cover][info][badge][kebab]
        'group grid grid-cols-[auto_48px_minmax(0,1fr)_auto_auto] gap-2 px-4 py-2 items-center',
        'hover:bg-accent/40 transition-colors text-xs border-b border-border/50 cursor-pointer',
        selected && 'bg-primary/10',
      )}
    >
      {/* Visible per-row selector. `e.stopPropagation()` prevents the
          row's onClick (open detail Dialog) from firing when the user
          just wanted to toggle the checkbox. The 全选 header button on
          WorklistHeaderBar selects ALl visible-after-filter rows via
          useWorklistStore.selectAll() — same store action, same UX
          parity as before. */}
      <input
        type="checkbox"
        aria-label={`选中 ${fileName}`}
        title={`选中 ${fileName}`}
        checked={selected}
        onChange={() => setSelected(fullPath)}
        onClick={(e) => e.stopPropagation()}
        className="w-3.5 h-3.5 cursor-pointer accent-primary z-10"
      />
      <RowCover
        src={coverSrc}
        id={coverId}
        title={musicInfo?.album ? toTrimmedString(musicInfo.album) : fileName}
      />
      <div className="min-w-0">
        <div className="truncate font-medium" title={fullPath}>
          {fileName}
        </div>
        {artist && (
          <div
            className="truncate text-[11px] text-muted-foreground"
            title={artist}
          >
            {artist}
          </div>
        )}
      </div>
      <Badge
        variant="outline"
        className={cn('text-[10px] px-1.5 py-0 h-4', STATUS_BADGE[status])}
        title={STATUS_LABEL[status]}
      >
        {status === 'scraped' ? '✓ 已刮' : status === 'failed' ? '✗ 失败' : '· 待刮'}
      </Badge>
      <DropdownMenu>
        <DropdownMenuTrigger
          render={
            <Button
              variant="ghost"
              size="icon-sm"
              aria-label="行操作"
              title="行操作"
              onClick={(e) => e.stopPropagation()}
            />
          }
        >
          <MoreHorizontal className="w-3.5 h-3.5" />
        </DropdownMenuTrigger>
        <DropdownMenuContent align="end">
          <DropdownMenuItem onClick={play}>
            <Play className="w-3.5 h-3.5 mr-2" /> 播放
          </DropdownMenuItem>
          <DropdownMenuItem onClick={detail}>
            <PencilLine className="w-3.5 h-3.5 mr-2" /> 详情
          </DropdownMenuItem>
          <DropdownMenuItem
            onClick={remove}
            className="text-destructive focus:text-destructive"
          >
            <Trash2 className="w-3.5 h-3.5 mr-2" /> 移除
          </DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>
    </div>
  );
}

/** Outer container. Selects from the store with a stable identity-sort
 *  (rows are appended in enqueueDirs() order), then renders either a
 *  flat per-row list (legacy behaviour when `grouping === 'none'`) OR
 *  a flat per-group+per-row list (Plan C.3 grouping). Empty
 *  placeholder if no rows. */
export function Worklist() {
  const rows = useWorklistStore((s) => s.rows);
  const selectedIds = useWorklistStore((s) => s.selectedIds);
  const filter = useWorklistStore((s) => s.filter);
  const grouping = useWorklistStore((s) => s.grouping);
  const collapsedGroups = useWorklistStore((s) => s.collapsedGroups);
  const toggleGroupCollapsed = useWorklistStore((s) => s.toggleGroupCollapsed);

  const grouped = useMemo(
    () => deriveGrouped(rows, filter, grouping, collapsedGroups),
    [rows, filter, grouping, collapsedGroups],
  );

  if (grouped.length === 0) {
    return (
      <div className="flex-1 flex items-center justify-center p-4 text-muted-foreground">
        {rows.length === 0 ? (
          <div className="text-center space-y-2">
            <Music className="w-10 h-10 mx-auto opacity-30" />
            <p className="text-sm">Worklist 为空</p>
            <p className="text-xs opacity-70">
              点击顶栏「添加音乐」选择目录以开始刮削
            </p>
          </div>
        ) : (
          <p className="text-sm">当前过滤条件下没有结果</p>
        )}
      </div>
    );
  }

  return (
    <ScrollArea className="flex-1 min-h-0">
      {grouped.map((item) =>
        item.kind === 'header' ? (
          <GroupHeaderRow
            key={`h:${item.groupKey}`}
            groupKey={item.groupKey}
            label={item.label}
            count={item.count}
            collapsed={!item.expanded}
            onToggle={() => toggleGroupCollapsed(item.groupKey)}
          />
        ) : (
          <WorklistRowView
            key={item.row.id}
            fullPath={item.row.fullPath}
            fileName={item.row.fileName}
            status={item.row.status}
            musicInfo={item.row.musicInfo}
            selected={selectedIds.includes(item.row.id)}
          />
        ),
      )}
    </ScrollArea>
  );
}
