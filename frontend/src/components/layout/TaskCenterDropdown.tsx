import { useEffect, useRef, useState, useCallback } from 'react';
import {
  Activity,
  Trash2,
  CheckCircle2,
  Clock,
  RefreshCw,
  Cpu,
} from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Badge } from '@/components/ui/badge';
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from '@/components/ui/popover';
import { ScrollArea } from '@/components/ui/scroll-area';
import { useNoticeStore } from '@/store/useNoticeStore';
import { getActiveQueue, clearAsyncTasks } from '@/api/client';

interface TaskQueueData {
  servers?: string[];
  queues?: Record<string, number>;
  pending?: Array<{ id: string; type: string; payload?: string }>;
}

export function TaskCenterDropdown() {
  const [open, setOpen] = useState(false);
  const [data, setData] = useState<TaskQueueData | null>(null);
  const [loading, setLoading] = useState(false);
  const [clearing, setClearing] = useState(false);

  // 上一次请求还没回来就跳过，5s 的间隔慢于 Redis 往返，但慢网络下会叠。
  const inFlight = useRef(false);

  const load = useCallback(async () => {
    if (inFlight.current) return;
    inFlight.current = true;
    setLoading(true);
    try {
      const res = await getActiveQueue();
      if (res?.result) {
        setData(res.data);
      }
    } catch {
      // 拉取失败保留上一次的快照，不打断使用；下次打开会重试
    } finally {
      inFlight.current = false;
      setLoading(false);
    }
  }, []);

  // 请求只由「点开任务中心」触发（见 handleOpenChange），effect 里只负责
  // 面板开着期间每 5s 跟一次，关闭即停。挂载时一次都不发。
  useEffect(() => {
    if (!open) return;
    const interval = setInterval(load, 5000);
    return () => clearInterval(interval);
  }, [open, load]);

  // 关着的时候角标上的数字是「上次查看时」的值，不是实时的
  const handleOpenChange = (next: boolean) => {
    setOpen(next);
    if (next) void load();
  };

  const handleClear = async () => {
    setClearing(true);
    try {
      const res = await clearAsyncTasks();
      if (res?.result) {
        useNoticeStore.getState().push('已清除所有待处理异步任务', 'info');
        await load();
      } else {
        useNoticeStore.getState().push('清除任务失败', 'warn');
      }
    } catch (e) {
      const msg = e instanceof Error ? e.message : String(e);
      useNoticeStore.getState().push(`清除任务失败: ${msg}`, 'error');
    } finally {
      setClearing(false);
    }
  };

  const pendingCount = data?.pending?.length ?? 0;
  const isBusy = pendingCount > 0;
  const stale = !open && isBusy;

  return (
    <Popover open={open} onOpenChange={handleOpenChange}>
      <PopoverTrigger
        render={
          <Button
            variant="ghost"
            size="sm"
            className="h-8 px-2.5 gap-1.5 text-xs text-muted-foreground hover:text-foreground relative"
            title={
              stale
                ? `异步任务队列与系统状态（上次查看时 ${pendingCount} 个任务，打开刷新）`
                : '异步任务队列与系统状态'
            }
          >
            <Activity
              className={`w-3.5 h-3.5 ${
                isBusy && open ? 'text-primary animate-pulse' : 'text-muted-foreground'
              }`}
            />
            <span className="hidden sm:inline">任务中心</span>
            {pendingCount > 0 && (
              <Badge
                variant="default"
                className={`h-4 px-1 text-[10px] min-w-[16px] justify-center font-mono ${
                  stale
                    ? 'bg-muted text-muted-foreground border border-border'
                    : 'bg-primary text-primary-foreground'
                }`}
              >
                {pendingCount}
              </Badge>
            )}
          </Button>
        }
      />
      <PopoverContent align="end" className="w-80 p-0 shadow-xl border-border">
        <div className="flex items-center justify-between p-3 border-b border-border bg-muted/30">
          <div className="flex items-center gap-2">
            <Cpu className="w-4 h-4 text-primary" />
            <span className="text-sm font-semibold">任务队列与状态</span>
          </div>
          <div className="flex items-center gap-1">
            <Button
              variant="ghost"
              size="icon-sm"
              onClick={() => void load()}
              disabled={loading}
              title="刷新状态"
            >
              <RefreshCw className={`w-3.5 h-3.5 ${loading ? 'animate-spin' : ''}`} />
            </Button>
            {pendingCount > 0 && (
              <Button
                variant="ghost"
                size="icon-sm"
                onClick={handleClear}
                disabled={clearing}
                className="text-destructive hover:text-destructive"
                title="清空待处理任务队列"
              >
                <Trash2 className="w-3.5 h-3.5" />
              </Button>
            )}
          </div>
        </div>

        <div className="p-3 border-b border-border/60 bg-surface-1 text-xs space-y-1.5">
          <div className="flex justify-between items-center text-muted-foreground">
            <span>活跃 Worker:</span>
            <span className="font-mono text-foreground font-medium">
              {data?.servers?.length ?? 1} 个实例在线
            </span>
          </div>
          <div className="flex justify-between items-center text-muted-foreground">
            <span>排队任务数:</span>
            <Badge variant={pendingCount > 0 ? 'default' : 'outline'} className="text-[10px] h-4">
              {pendingCount} 个进行中 / 排队
            </Badge>
          </div>
        </div>

        <ScrollArea className="max-h-60 p-2">
          {pendingCount === 0 ? (
            <div className="py-6 text-center text-xs text-muted-foreground space-y-1.5">
              <CheckCircle2 className="w-6 h-6 text-emerald-500/80 mx-auto" />
              <p>当前任务队列空闲</p>
              <p className="text-[10px] text-muted-foreground/60">所有异步刮削与整理任务均已完成</p>
            </div>
          ) : (
            <div className="space-y-1.5">
              {data?.pending?.map((t) => (
                <div
                  key={t.id}
                  className="p-2 rounded border border-border/60 bg-surface-2 text-xs space-y-1"
                >
                  <div className="flex items-center justify-between font-mono">
                    <span className="font-medium text-foreground truncate max-w-[180px]">
                      {t.type}
                    </span>
                    <Badge variant="outline" className="text-[9px] h-3.5 px-1 text-primary">
                      <Clock className="w-2.5 h-2.5 mr-0.5" />
                      运行中
                    </Badge>
                  </div>
                  <div className="text-[10px] text-muted-foreground truncate font-mono">
                    ID: {t.id}
                  </div>
                </div>
              ))}
            </div>
          )}
        </ScrollArea>
      </PopoverContent>
    </Popover>
  );
}
