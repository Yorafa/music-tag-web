// The "将写入的标签" panel that sits beside the 候选 card list.
//
// This is Direction B of the scrape-UI rework: the candidate cards stay the
// per-source layout they always were, but the tags they write are no longer
// invisible until you switch to the 标签 tab. This panel mirrors formData
// live, so every 应用 lands somewhere the user can see without navigating —
// the "where did my apply go" problem that made picking fields feel like
// death by a thousand clicks.
//
// It reads formData (the single source of truth the 标签 tab also edits) and
// a provenance map that records which source each field was last taken from,
// so a value can show a "网易云" chip and the user can tell a composed record
// apart from the file's original tags.

import { cn } from '@/lib/utils';
import { Badge } from '@/components/ui/badge';
import { ImageIcon } from 'lucide-react';
import type { MusicTagInfo } from '@/types';
import { sourceLabel } from '@/components/workstation/scrapeReport';
import {
  APPLYABLE_FIELDS,
  fieldLabel,
  type ApplyableField,
} from '@/components/workstation/scrapedInfo';

// The plain-text fields render as a label/value/provenance row; cover and
// lyrics get their own richer treatment below, so they are split out here.
const TEXT_FIELDS = APPLYABLE_FIELDS.filter(
  (f) => f !== 'album_img' && f !== 'lyrics',
);

export interface ScrapeTargetPanelProps {
  formData: Partial<MusicTagInfo>;
  /** Which source each field was last applied from. Absent = the file's own
   *  original value, so no provenance chip is shown. */
  fieldSources: Partial<Record<ApplyableField, string>>;
  /** The field to briefly highlight after an apply, so the eye is drawn to
   *  where the value landed. Cleared by the parent on a timer. */
  flashField: ApplyableField | null;
  /** Source id → display name, threaded through from the sources list so the
   *  chip reads 「网易云」 not 「netease」. */
  sources: { id: string; name: string }[];
  className?: string;
}

export function ScrapeTargetPanel({
  formData,
  fieldSources,
  flashField,
  sources,
  className,
}: ScrapeTargetPanelProps) {
  const label = (id?: string) => (id ? sourceLabel(id, sources) : '');

  return (
    <div
      className={cn(
        'rounded-xl border border-primary/30 bg-surface-2/70 p-4 space-y-2.5',
        className,
      )}
    >
      <div className="flex items-center justify-between">
        <span className="text-sm font-semibold text-foreground">将写入的标签</span>
        <Badge variant="outline" className="text-[10px]">
          实时预览
        </Badge>
      </div>

      {TEXT_FIELDS.map((field) => (
        <TargetRow
          key={field}
          label={fieldLabel(field)}
          value={(formData[field] as string) || ''}
          source={label(fieldSources[field])}
          flash={flashField === field}
        />
      ))}

      {/* Cover: a thumbnail rather than a URL string, since a cover is judged
          by eye. Empty falls back to the placeholder icon. */}
      <div className="flex items-center gap-2 pt-1">
        <span className="text-muted-foreground text-xs w-14 shrink-0">
          {fieldLabel('album_img')}
        </span>
        <div
          className={cn(
            'w-11 h-11 rounded-lg overflow-hidden bg-muted ring-1 ring-border flex items-center justify-center transition-shadow',
            flashField === 'album_img' && 'ring-2 ring-primary',
          )}
        >
          {formData.album_img ? (
            <img src={formData.album_img} alt="" className="w-full h-full object-cover" />
          ) : (
            <ImageIcon className="w-5 h-5 text-muted-foreground/50" />
          )}
        </div>
        {fieldSources.album_img && (
          <Badge variant="secondary" className="text-[10px]">
            {label(fieldSources.album_img)}
          </Badge>
        )}
      </div>

      {/* Lyric: a short scrollable preview, so the user can confirm they took
          the right one (translated vs original, right song) before saving. */}
      <div className="pt-1">
        <div className="flex items-center justify-between">
          <span className="text-muted-foreground text-xs">{fieldLabel('lyrics')}</span>
          {fieldSources.lyrics && (
            <Badge variant="secondary" className="text-[10px]">
              {label(fieldSources.lyrics)}
            </Badge>
          )}
        </div>
        <pre
          className={cn(
            'mt-1 max-h-56 overflow-y-auto whitespace-pre-wrap break-words rounded bg-muted/40 p-2 font-mono text-[10px] leading-relaxed text-foreground',
            flashField === 'lyrics' && 'ring-2 ring-primary',
          )}
        >
          {formData.lyrics || '—'}
        </pre>
      </div>
    </div>
  );
}

function TargetRow({
  label,
  value,
  source,
  flash,
}: {
  label: string;
  value: string;
  source: string;
  flash: boolean;
}) {
  return (
    <div
      className={cn(
        'flex items-center gap-2 text-xs rounded px-1 -mx-1 transition-colors',
        flash && 'bg-primary/10',
      )}
    >
      <span className="text-muted-foreground w-14 shrink-0">{label}</span>
      <span
        className={cn('flex-1 min-w-0 truncate', !value && 'text-muted-foreground/50')}
        title={value}
      >
        {value || '—'}
      </span>
      {source && (
        <Badge variant="secondary" className="text-[10px] shrink-0">
          {source}
        </Badge>
      )}
    </div>
  );
}
