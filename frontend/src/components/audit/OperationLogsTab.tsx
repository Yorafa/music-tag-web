/* eslint-disable react-hooks/set-state-in-effect -- Canonical fetch-on-mount + reload-on-click pattern in modal tab */
import { useState, useEffect, useCallback } from 'react';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Badge } from '@/components/ui/badge';
import { Card, CardContent } from '@/components/ui/card';
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogFooter,
} from '@/components/ui/dialog';
import {
  RefreshCw,
  Trash2,
  Loader2,
  FileText,
  Search,
  ChevronLeft,
  ChevronRight,
  Info,
  User,
  Clock,
  AlertTriangle,
} from 'lucide-react';
import {
  getOperationLogs,
  clearOperationLogs,
  type OperationLogItem,
} from '@/api/client';
import { useNoticeStore } from '@/store/useNoticeStore';
import { cn } from '@/lib/utils';
import { ACTION_CONFIG, STATUS_CONFIG } from './operationLogConfig';

function formatTimestamp(iso: string) {
  if (!iso) return '';
  const d = new Date(iso);
  if (isNaN(d.getTime())) return iso;
  return d.toLocaleString('zh-CN', {
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
    hour12: false,
  });
}

export function OperationLogsTab() {
  const [logs, setLogs] = useState<OperationLogItem[]>([]);
  const [total, setTotal] = useState(0);
  const [page, setPage] = useState(1);
  const pageSize = 15;

  const [actionFilter, setActionFilter] = useState<string>('all');
  const [statusFilter, setStatusFilter] = useState<string>('all');
  const [searchInput, setSearchInput] = useState<string>('');
  const [debouncedSearch, setDebouncedSearch] = useState<string>('');

  const [loading, setLoading] = useState(true);
  const [refreshing, setRefreshing] = useState(false);
  const [error, setError] = useState<string | null>(null);

  // Detail Modal state
  const [detailItem, setDetailItem] = useState<OperationLogItem | null>(null);
  // Clear Confirm Dialog state
  const [clearDialogOpen, setClearDialogOpen] = useState(false);
  const [clearing, setClearing] = useState(false);

  // Debounce search input
  useEffect(() => {
    const timer = setTimeout(() => {
      setDebouncedSearch(searchInput.trim());
      setPage(1);
    }, 300);
    return () => clearTimeout(timer);
  }, [searchInput]);

  const fetchLogs = useCallback(
    async (p: number) => {
      setLoading(true);
      setError(null);
      try {
        const res = await getOperationLogs({
          page: p,
          page_size: pageSize,
          action: actionFilter !== 'all' ? actionFilter : undefined,
          status: statusFilter !== 'all' ? statusFilter : undefined,
          search: debouncedSearch || undefined,
        });
        if (res.result) {
          setLogs(res.data.results || []);
          setTotal(res.data.count || 0);
        } else {
          setError(res.message || '获取日志失败');
        }
      } catch (e) {
        setError(e instanceof Error ? e.message : String(e));
      } finally {
        setLoading(false);
      }
    },
    [actionFilter, statusFilter, debouncedSearch, pageSize],
  );

  useEffect(() => {
    void fetchLogs(page);
  }, [fetchLogs, page]);

  const handleRefresh = async () => {
    setRefreshing(true);
    await fetchLogs(page);
    setRefreshing(false);
  };

  const handleClear = async () => {
    setClearing(true);
    try {
      const res = await clearOperationLogs(0);
      useNoticeStore.getState().push(`已清空 ${res.data?.cleared ?? 0} 条操作历史日志`, 'info');
      setClearDialogOpen(false);
      setPage(1);
      await fetchLogs(1);
    } catch (e) {
      const msg = e instanceof Error ? e.message : String(e);
      useNoticeStore.getState().push(`清空日志失败: ${msg}`, 'error');
    } finally {
      setClearing(false);
    }
  };

  const totalPages = Math.max(1, Math.ceil(total / pageSize));

  return (
    <div className="space-y-3">
      {/* Top Filter Bar */}
      <div className="flex flex-col sm:flex-row gap-2 items-stretch sm:items-center justify-between">
        <div className="flex flex-wrap gap-1.5 items-center flex-1">
          {/* Action Filter */}
          <select
            value={actionFilter}
            onChange={(e) => {
              setActionFilter(e.target.value);
              setPage(1);
            }}
            className="h-8 px-2 rounded-md border border-input bg-background text-xs"
            aria-label="操作类型过滤"
          >
            <option value="all">全部操作</option>
            <option value="update_id3">单曲标签</option>
            <option value="batch_update_id3">批量标签</option>
            <option value="auto_scrape">自动刮削</option>
            <option value="filename_parse">文件名解析</option>
            <option value="tidy_folder">目录整理</option>
            <option value="prune_empty_folders">清理残留</option>
            <option value="download">音乐下载</option>
            <option value="upload_cover">上传封面</option>
          </select>

          {/* Status Filter */}
          <select
            value={statusFilter}
            onChange={(e) => {
              setStatusFilter(e.target.value);
              setPage(1);
            }}
            className="h-8 px-2 rounded-md border border-input bg-background text-xs"
            aria-label="操作状态过滤"
          >
            <option value="all">全部状态</option>
            <option value="success">成功</option>
            <option value="failed">失败</option>
            <option value="partial">部分成功</option>
            <option value="skipped">已跳过</option>
          </select>

          {/* Search Input */}
          <div className="relative flex-1 min-w-[140px]">
            <Search className="w-3.5 h-3.5 absolute left-2.5 top-2.5 text-muted-foreground" />
            <Input
              value={searchInput}
              onChange={(e) => setSearchInput(e.target.value)}
              placeholder="搜索目标路径 / 详情..."
              className="h-8 pl-8 pr-2 text-xs"
            />
          </div>
        </div>

        {/* Action Buttons */}
        <div className="flex items-center gap-1.5 shrink-0 self-end sm:self-auto">
          <Button
            variant="outline"
            size="sm"
            onClick={handleRefresh}
            disabled={loading || refreshing}
            className="h-8 px-2.5 text-xs"
            title="刷新操作日志"
          >
            {refreshing ? (
              <Loader2 className="w-3.5 h-3.5 animate-spin" />
            ) : (
              <RefreshCw className="w-3.5 h-3.5" />
            )}
            <span className="ml-1 hidden sm:inline">刷新</span>
          </Button>
          <Button
            variant="outline"
            size="sm"
            onClick={() => setClearDialogOpen(true)}
            disabled={loading || total === 0}
            className="h-8 px-2.5 text-xs text-red-400 hover:text-red-300 hover:bg-red-500/10 border-red-500/20"
            title="清空历史日志"
          >
            <Trash2 className="w-3.5 h-3.5" />
            <span className="ml-1 hidden sm:inline">清空</span>
          </Button>
        </div>
      </div>

      {/* Error Alert */}
      {error && (
        <div
          role="alert"
          className="text-xs text-red-400 bg-red-500/10 border border-red-500/30 rounded px-2.5 py-1.5"
        >
          加载失败: {error}
        </div>
      )}

      {/* Log List View */}
      {loading && !refreshing ? (
        <div className="text-xs text-muted-foreground flex items-center justify-center gap-2 py-8">
          <Loader2 className="w-4 h-4 animate-spin" />
          正在加载操作日志…
        </div>
      ) : logs.length === 0 ? (
        <div className="text-xs text-muted-foreground bg-muted/30 rounded-lg px-4 py-8 text-center space-y-1">
          <Info className="w-5 h-5 mx-auto text-muted-foreground/60 mb-1" />
          <p className="font-medium">暂无操作历史审计日志</p>
          <p className="text-[11px] opacity-70">
            当对音乐标签进行单曲编辑、批量修改、自动刮削或下载时，会自动记录审计历史。
          </p>
        </div>
      ) : (
        <div className="space-y-1.5 max-h-[45vh] overflow-y-auto pr-0.5">
          {logs.map((item) => {
            const actionCfg = ACTION_CONFIG[item.action] || {
              label: item.action,
              icon: Info,
              color: 'bg-zinc-500/10 text-zinc-400 border-zinc-500/30',
            };
            const ActionIcon = actionCfg.icon;

            const statusCfg = STATUS_CONFIG[item.status] || {
              label: item.status,
              icon: Info,
              color: 'bg-zinc-500/10 text-zinc-400 border-zinc-500/30',
            };
            const StatusIcon = statusCfg.icon;

            return (
              <Card
                key={item.id}
                className="bg-muted/30 hover:bg-muted/50 transition-colors border-border/60"
              >
                <CardContent className="py-2.5 px-3 flex items-center justify-between gap-2.5">
                  <div className="flex-1 min-w-0 space-y-1">
                    <div className="flex items-center gap-2 flex-wrap">
                      {/* Action Badge */}
                      <Badge
                        variant="outline"
                        className={cn('text-[10px] px-1.5 py-0 h-4.5 gap-1', actionCfg.color)}
                      >
                        <ActionIcon className="w-2.5 h-2.5" />
                        {actionCfg.label}
                      </Badge>

                      {/* Status Badge */}
                      <Badge
                        variant="outline"
                        className={cn('text-[10px] px-1.5 py-0 h-4.5 gap-1', statusCfg.color)}
                      >
                        <StatusIcon className="w-2.5 h-2.5" />
                        {statusCfg.label}
                      </Badge>

                      {/* Item Count */}
                      {item.item_count > 1 && (
                        <span className="text-[10px] text-muted-foreground bg-muted px-1.5 py-0.2 rounded font-mono">
                          {item.item_count} 项
                        </span>
                      )}

                      {/* Operator */}
                      <span className="text-[11px] text-muted-foreground/80 flex items-center gap-0.5 ml-auto sm:ml-0">
                        <User className="w-2.5 h-2.5 opacity-60" />
                        {item.operator || 'admin'}
                      </span>

                      {/* Time */}
                      <span className="text-[11px] text-muted-foreground flex items-center gap-0.5 ml-auto">
                        <Clock className="w-2.5 h-2.5 opacity-60" />
                        {formatTimestamp(item.created_at)}
                      </span>
                    </div>

                    {/* Target Path */}
                    <div
                      className="text-xs font-mono text-foreground/90 truncate"
                      title={item.target}
                    >
                      {item.target}
                    </div>

                    {/* Error preview if failed */}
                    {item.error_msg && (
                      <p className="text-[11px] text-red-400 truncate">
                        错误: {item.error_msg}
                      </p>
                    )}
                  </div>

                  {/* Details Button */}
                  <Button
                    variant="ghost"
                    size="sm"
                    onClick={() => setDetailItem(item)}
                    className="h-7 px-2 text-xs text-muted-foreground hover:text-foreground shrink-0"
                    title="查看完整操作详情"
                  >
                    详情
                  </Button>
                </CardContent>
              </Card>
            );
          })}
        </div>
      )}

      {/* Pagination Bar */}
      {total > 0 && (
        <div className="flex items-center justify-between text-xs text-muted-foreground pt-1 border-t border-border/50">
          <span>
            第 {page} / {totalPages} 页 (共 {total} 条记录)
          </span>
          <div className="flex items-center gap-1">
            <Button
              variant="outline"
              size="sm"
              onClick={() => setPage((p) => Math.max(1, p - 1))}
              disabled={page <= 1 || loading}
              className="h-7 w-7 p-0"
              title="上一页"
            >
              <ChevronLeft className="w-3.5 h-3.5" />
            </Button>
            <Button
              variant="outline"
              size="sm"
              onClick={() => setPage((p) => Math.min(totalPages, p + 1))}
              disabled={page >= totalPages || loading}
              className="h-7 w-7 p-0"
              title="下一页"
            >
              <ChevronRight className="w-3.5 h-3.5" />
            </Button>
          </div>
        </div>
      )}

      {/* Detail Dialog */}
      {detailItem && (
        <Dialog open={Boolean(detailItem)} onOpenChange={(o) => !o && setDetailItem(null)}>
          <DialogContent className="sm:max-w-lg max-h-[85vh] overflow-y-auto">
            <DialogHeader>
              <DialogTitle className="text-sm flex items-center gap-2">
                <FileText className="w-4 h-4 text-primary" /> 操作日志详情 (ID #{detailItem.id})
              </DialogTitle>
            </DialogHeader>

            <div className="space-y-3 py-2 text-xs">
              <div className="grid grid-cols-2 gap-2 bg-muted/40 p-2.5 rounded-lg">
                <div>
                  <span className="text-muted-foreground">操作类型:</span>{' '}
                  <span className="font-medium">
                    {ACTION_CONFIG[detailItem.action]?.label || detailItem.action}
                  </span>
                </div>
                <div>
                  <span className="text-muted-foreground">执行状态:</span>{' '}
                  <span className="font-medium">
                    {STATUS_CONFIG[detailItem.status]?.label || detailItem.status}
                  </span>
                </div>
                <div>
                  <span className="text-muted-foreground">操作用户:</span>{' '}
                  <span className="font-mono">{detailItem.operator || 'admin'}</span>
                </div>
                <div>
                  <span className="text-muted-foreground">操作时间:</span>{' '}
                  <span className="font-mono">{formatTimestamp(detailItem.created_at)}</span>
                </div>
              </div>

              <div>
                <span className="text-muted-foreground block mb-1">目标对象:</span>
                <div className="p-2 rounded bg-muted/50 font-mono break-all text-[11px]">
                  {detailItem.target}
                </div>
              </div>

              {detailItem.error_msg && (
                <div>
                  <span className="text-red-400 block mb-1 font-medium">错误信息:</span>
                  <div className="p-2 rounded bg-red-500/10 border border-red-500/20 text-red-400 font-mono text-[11px] break-all">
                    {detailItem.error_msg}
                  </div>
                </div>
              )}

              <div>
                <span className="text-muted-foreground block mb-1">操作详细数据 (Details):</span>
                <pre className="p-2.5 rounded bg-muted/60 font-mono text-[11px] overflow-x-auto max-h-56 whitespace-pre-wrap">
                  {(() => {
                    try {
                      if (!detailItem.details) return '(无)';
                      const parsed = JSON.parse(detailItem.details);
                      return JSON.stringify(parsed, null, 2);
                    } catch {
                      return detailItem.details || '(无)';
                    }
                  })()}
                </pre>
              </div>
            </div>

            <DialogFooter>
              <Button size="sm" onClick={() => setDetailItem(null)}>
                关闭
              </Button>
            </DialogFooter>
          </DialogContent>
        </Dialog>
      )}

      {/* Clear Confirmation Dialog */}
      <Dialog open={clearDialogOpen} onOpenChange={setClearDialogOpen}>
        <DialogContent className="sm:max-w-sm">
          <DialogHeader>
            <DialogTitle className="text-sm text-red-400 flex items-center gap-2">
              <AlertTriangle className="w-4 h-4 text-red-400" /> 清空操作历史日志
            </DialogTitle>
          </DialogHeader>
          <p className="text-xs text-muted-foreground py-2">
            确定要清空全部操作历史审计日志吗？此操作无法撤销。
          </p>
          <DialogFooter className="gap-2 sm:gap-0">
            <Button
              variant="outline"
              size="sm"
              onClick={() => setClearDialogOpen(false)}
              disabled={clearing}
            >
              取消
            </Button>
            <Button
              variant="destructive"
              size="sm"
              onClick={handleClear}
              disabled={clearing}
            >
              {clearing ? <Loader2 className="w-3.5 h-3.5 mr-1 animate-spin" /> : null}
              确认清空
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}
