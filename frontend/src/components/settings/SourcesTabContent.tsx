/* eslint-disable react-hooks/set-state-in-effect -- Fetch-on-mount in settings tab */
import { useEffect, useState } from 'react';
import { Button } from '@/components/ui/button';
import { Badge } from '@/components/ui/badge';
import { Card, CardContent } from '@/components/ui/card';
import {
  RefreshCw,
  Loader2,
  Lock,
  Check,
  X,
} from 'lucide-react';
import {
  refreshSources,
  getSourceOverrides,
} from '@/api/client';
import { useNoticeStore } from '@/store/useNoticeStore';
import { cn } from '@/lib/utils';

export function SourcesTabContent() {
  type OverrideRow = {
    name: string;
    hasOverride: boolean;
    apiBase?: string;
    hasSecret: boolean;
  };
  const [overrides, setOverrides] = useState<OverrideRow[] | null>(null);
  const [loading, setLoading] = useState(true);
  const [refreshing, setRefreshing] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const refresh = async () => {
    setLoading(true);
    setError(null);
    try {
      const res = await getSourceOverrides();
      setOverrides(res.overrides);
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    void refresh();
  }, []);

  const onReload = async () => {
    setRefreshing(true);
    try {
      const res = await refreshSources();
      const notice = useNoticeStore.getState();
      notice.push(
        `已重载 ${res.loaded} 个 override，触发 ${res.refreshed} 个 setter 调用`,
        'info',
      );
      await refresh();
    } catch (e) {
      const msg = e instanceof Error ? e.message : String(e);
      useNoticeStore.getState().push(`重载失败: ${msg}`, 'error');
    } finally {
      setRefreshing(false);
    }
  };

  return (
    <div className="space-y-3">
      <div className="flex items-center justify-between gap-2">
        <p className="text-xs text-muted-foreground">
          当前生效的 plugin override（来自服务器{' '}
          <code className="px-1 py-0.5 rounded bg-muted/40 text-[10px] font-mono">
            data/sources/*.yaml
          </code>
          ）。secret 栏只显示「已配置」状态，不暴露明文。
        </p>
        <Button
          variant="outline"
          size="sm"
          onClick={onReload}
          disabled={refreshing || loading}
          aria-label="重载 source overrides"
          title="POST /api/sources/refresh/ — 不重启 gateway 即热重载 YAML"
        >
          {refreshing ? (
            <Loader2 className="w-3.5 h-3.5 mr-1.5 animate-spin" />
          ) : (
            <RefreshCw className="w-3.5 h-3.5 mr-1.5" />
          )}
          重载配置
        </Button>
      </div>

      {error && (
        <div
          role="alert"
          className="text-xs text-red-400 bg-red-500/10 border border-red-500/30 rounded px-2 py-1.5"
        >
          GET /api/sources/override/ 失败: {error}
        </div>
      )}

      {loading && !error ? (
        <div className="text-xs text-muted-foreground flex items-center gap-2 py-4 justify-center">
          <Loader2 className="w-3.5 h-3.5 animate-spin" />
          加载中…
        </div>
      ) : overrides && overrides.length === 0 ? (
        <div className="text-xs text-muted-foreground bg-muted/40 rounded px-3 py-4 text-center space-y-1">
          <p>暂无 override。</p>
          <p className="opacity-70">
            编辑服务器{' '}
            <code className="font-mono text-[10px] bg-muted/60 px-1 py-0.5 rounded">
              data/sources/&lt;name&gt;.yaml
            </code>{' '}
            后点击「重载配置」。
          </p>
        </div>
      ) : (
        <div className="space-y-1.5 max-h-[40vh] overflow-y-auto">
          {(overrides ?? []).map((ov) => (
            <Card key={ov.name} className="bg-muted/30">
              <CardContent className="py-2.5 px-3 space-y-1.5">
                <div className="flex items-center gap-2">
                  <span className="font-mono text-sm font-medium">{ov.name}</span>
                  <Badge
                    variant="outline"
                    className={cn(
                      'text-[10px] px-1.5 py-0 h-4',
                      ov.hasOverride
                        ? 'bg-emerald-500/10 text-emerald-400 border-emerald-500/30'
                        : 'bg-zinc-500/10 text-zinc-400 border-zinc-500/30',
                    )}
                  >
                    {ov.hasOverride ? (
                      <>
                        <Check className="w-2.5 h-2.5 mr-0.5" /> 已 override
                      </>
                    ) : (
                      <>
                        <X className="w-2.5 h-2.5 mr-0.5" /> 默认
                      </>
                    )}
                  </Badge>
                  {ov.hasSecret && (
                    <Badge
                      variant="outline"
                      className="text-[10px] px-1.5 py-0 h-4 bg-amber-500/10 text-amber-400 border-amber-500/30"
                      title="secret 已配置 (明文不在 UI 暴露)"
                    >
                      <Lock className="w-2.5 h-2.5 mr-0.5" /> secret 已设
                    </Badge>
                  )}
                </div>
                {ov.apiBase && (
                  <div
                    className="text-[11px] font-mono text-muted-foreground truncate"
                    title={ov.apiBase}
                  >
                    api_base: {ov.apiBase}
                  </div>
                )}
              </CardContent>
            </Card>
          ))}
        </div>
      )}
    </div>
  );
}
