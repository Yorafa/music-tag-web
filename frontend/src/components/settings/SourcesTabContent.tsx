import { useEffect } from 'react';
import { Badge } from '@/components/ui/badge';
import { Card, CardContent } from '@/components/ui/card';
import { Loader2, Music4, Download } from 'lucide-react';
import { useSourceStore } from '@/store/useSourceStore';
import { cn } from '@/lib/utils';
import type { SourceInfo } from '@/types';

/** The capability flags worth surfacing, in the order a user cares about
 *  when asking "can I search this one, and can I preview it?".
 *
 *  These mirror `plugin.TagSource`'s capability methods, so a plugin that
 *  gains a capability starts showing it here with no frontend change —
 *  the labels are the only thing this file hard-codes. */
const CAPABILITIES: ReadonlyArray<{
  key: keyof SourceInfo;
  label: string;
}> = [
  { key: 'searchable', label: '搜索' },
  { key: 'lyric', label: '歌词' },
  { key: 'supports_id3', label: '刮削' },
  { key: 'supports_audio_url', label: '试听' },
];

/**
 * 音源 — a read-only view of the plugin registry.
 *
 * This panel does not offer runtime `api_base` / secret overrides, and
 * there is no 「重载配置」 button, because the two endpoints behind them
 * answer 501: the plugins run in their own
 * containers and the gRPC contract has no RPC able to carry an override
 * across that boundary, so the feature could never have applied one. A
 * button that always returns red
 * "GET /api/sources/override/ 失败: … 501" banner above an empty list.
 *
 * What replaces it is the part that does work: which plugins the gateway
 * has registered and what each one can do. Overrides still belong in
 * `data/sources/*.yaml` on disk, applied by restarting the plugin
 * container — the note below says so rather than offering a button that
 * cannot deliver.
 */
export function SourcesTabContent() {
  const sources = useSourceStore((s) => s.sources);
  const loaded = useSourceStore((s) => s.loaded);
  const loadSources = useSourceStore((s) => s.loadSources);

  useEffect(() => {
    // loadSources swallows its own errors and flips `loaded` either way,
    // so an unreachable gateway shows the empty-state below rather than
    // an error banner — this tab is informational and must never look
    // broken. Hence no `.catch` here: it could never fire.
    void loadSources();
  }, [loadSources]);

  const loading = !loaded;

  return (
    <div className="space-y-3">
      <p className="text-xs text-muted-foreground">
        网关已注册的音源与其能力。运行时 override 不可用：插件运行在独立容器中，
        修改{' '}
        <code className="px-1 py-0.5 rounded bg-muted/40 text-[10px] font-mono">
          data/sources/&lt;name&gt;.yaml
        </code>{' '}
        后需重启对应插件容器才会生效。
      </p>

      {loading ? (
        <div className="text-xs text-muted-foreground flex items-center gap-2 py-4 justify-center">
          <Loader2 className="w-3.5 h-3.5 animate-spin" />
          加载中…
        </div>
      ) : sources.length === 0 ? (
        <div className="text-xs text-muted-foreground bg-muted/40 rounded px-3 py-4 text-center">
          {/* The store reports an unreachable gateway and a registry with
           * zero plugins identically (sources: [], loaded: true), so this
           * one message has to cover both without claiming to know which. */}
          未获取到音源列表：网关未注册任何音源，或无法连接网关。
        </div>
      ) : (
        <div className="space-y-1.5 max-h-[52vh] overflow-y-auto">
          {sources.map((s) => (
            <Card key={s.name} className="bg-muted/30">
              <CardContent className="py-2.5 px-3 space-y-1.5">
                <div className="flex items-center gap-2 flex-wrap">
                  <span className="font-mono text-sm font-medium">
                    {s.display_name || s.name}
                  </span>
                  <span className="font-mono text-[10px] text-muted-foreground">
                    {s.name}
                  </span>
                  <Badge
                    variant="outline"
                    className="text-[10px] px-1.5 py-0 h-4 gap-0.5"
                    title={
                      s.kind === 'download'
                        ? '下载源：提供音频下载，不参与标签刮削'
                        : '标签源：提供搜索 / 歌词 / ID3 刮削'
                    }
                  >
                    {s.kind === 'download' ? (
                      <Download className="w-2.5 h-2.5 mr-0.5" />
                    ) : (
                      <Music4 className="w-2.5 h-2.5 mr-0.5" />
                    )}
                    {s.kind === 'download' ? '下载源' : '标签源'}
                  </Badge>
                  {s.default_on && (
                    <Badge
                      variant="outline"
                      className="text-[10px] px-1.5 py-0 h-4 border-emerald-500/30 bg-emerald-500/10 text-emerald-400"
                      title="默认参与云端检索"
                    >
                      默认开启
                    </Badge>
                  )}
                </div>
                <div className="flex flex-wrap items-center gap-1">
                  {CAPABILITIES.map(({ key, label }) => {
                    const on = Boolean(s[key]);
                    return (
                      <Badge
                        key={String(key)}
                        variant="outline"
                        className={cn(
                          'text-[10px] px-1.5 py-0 h-4',
                          on
                            ? 'bg-zinc-500/10 text-zinc-300 border-zinc-500/30'
                            : 'bg-transparent text-muted-foreground/50 border-border/50',
                        )}
                      >
                        {label}
                      </Badge>
                    );
                  })}
                </div>
              </CardContent>
            </Card>
          ))}
        </div>
      )}
    </div>
  );
}
