import { useState, useMemo } from 'react';
import { FileTreeBrowser } from './FileTreeBrowser';
import { WorkstationToolbar } from './WorkstationToolbar';
import { WorkstationTable } from './WorkstationTable';
import { DirPickerDrawer } from '@/components/scraper/DirPickerDrawer';
import { useWorklistStore } from '@/store/useWorklistStore';
import { selectWorklistRow } from '@/components/workstation/rowSelection';
import { ResizeHandle } from '@/components/layout/ResizeHandle';
import { readNumber, writeNumber } from '@/utils/persist';
import type { WorklistRow } from '@/types';

const LEFT_WIDTH_KEY = 'workstation.leftWidth';
const DEFAULT_LEFT_WIDTH = 240;

export function WorkstationView() {
  const rows = useWorklistStore((s) => s.rows);

  const [dirPickerOpen, setDirPickerOpen] = useState(false);
  const [selectedPath, setSelectedPath] = useState<string | null>(null);

  const [leftWidth, setLeftWidth] = useState<number>(
    () => readNumber(LEFT_WIDTH_KEY) ?? DEFAULT_LEFT_WIDTH,
  );
  // Derive active row without triggering cascading effect renders
  const activeRow = useMemo(() => {
    if (selectedPath) {
      const found = rows.find((r) => r.fullPath === selectedPath);
      if (found) return found;
    }
    return rows[0] || null;
  }, [rows, selectedPath]);

  const handleResizeLeft = (deltaX: number) => {
    setLeftWidth((prev) => {
      const next = Math.max(180, Math.min(400, prev + deltaX));
      writeNumber(LEFT_WIDTH_KEY, next);
      return next;
    });
  };

  const handleSelectRow = (row: WorklistRow) => {
    selectWorklistRow(row, { setSelectedPath });
  };

  return (
    <div className="flex-1 flex h-full w-full min-h-0 overflow-hidden relative bg-background">
      {/* Left Column: File Tree & Collections */}
      <div
        style={{ width: `${leftWidth}px` }}
        className="hidden md:flex flex-col h-full shrink-0 min-w-[180px] max-w-[400px]"
      >
        <FileTreeBrowser onOpenDirPicker={() => setDirPickerOpen(true)} />
      </div>

      <div className="hidden md:block">
        <ResizeHandle
          onResize={handleResizeLeft}
          valueNow={leftWidth}
          min={180}
          max={400}
        />
      </div>

      {/* Center Column: Toolbar + Primary Music Data Table */}
      <div className="flex-1 flex flex-col h-full min-w-0 overflow-hidden">
        <WorkstationToolbar onOpenDirPicker={() => setDirPickerOpen(true)} />
        <WorkstationTable
          activeRow={activeRow}
          onSelectRow={handleSelectRow}
        />
      </div>

      {/* Directory Picker Drawer */}
      <DirPickerDrawer
        open={dirPickerOpen}
        onOpenChange={setDirPickerOpen}
      />
    </div>
  );
}
