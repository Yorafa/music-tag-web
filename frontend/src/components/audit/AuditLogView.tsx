import { OperationLogsTab } from '@/components/audit/OperationLogsTab';

export function AuditLogView() {
  return (
    <div className="flex-1 flex flex-col overflow-hidden bg-surface-1 min-h-0 p-4">
      <div className="max-w-5xl w-full mx-auto flex-1 flex flex-col min-h-0 bg-surface-2/60 border border-border/70 rounded-2xl p-4 md:p-6 shadow-sm">
        <OperationLogsTab />
      </div>
    </div>
  );
}
