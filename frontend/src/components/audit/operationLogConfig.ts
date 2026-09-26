import {
  Tag,
  Layers,
  Sparkles,
  FileText,
  FolderSync,
  FolderX,
  Download,
  Image as ImageIcon,
  CheckCircle2,
  XCircle,
  AlertTriangle,
  MinusCircle,
  type LucideIcon,
} from 'lucide-react';

export interface ActionConfigItem {
  label: string;
  icon: LucideIcon;
  color: string;
}

export interface StatusConfigItem {
  label: string;
  icon: LucideIcon;
  color: string;
}

export const ACTION_CONFIG: Record<string, ActionConfigItem> = {
  update_id3: {
    label: '单曲标签',
    icon: Tag,
    color: 'bg-blue-500/10 text-blue-400 border-blue-500/30',
  },
  batch_update_id3: {
    label: '批量标签',
    icon: Layers,
    color: 'bg-purple-500/10 text-purple-400 border-purple-500/30',
  },
  auto_scrape: {
    label: '自动刮削',
    icon: Sparkles,
    color: 'bg-amber-500/10 text-amber-400 border-amber-500/30',
  },
  filename_parse: {
    label: '文件名解析',
    icon: FileText,
    color: 'bg-cyan-500/10 text-cyan-400 border-cyan-500/30',
  },
  prune_empty_folders: {
    label: '清理残留',
    icon: FolderX,
    color: 'bg-stone-500/10 text-stone-400 border-stone-500/30',
  },
  tidy_folder: {
    label: '目录整理',
    icon: FolderSync,
    color: 'bg-indigo-500/10 text-indigo-400 border-indigo-500/30',
  },
  download: {
    label: '音乐下载',
    icon: Download,
    color: 'bg-emerald-500/10 text-emerald-400 border-emerald-500/30',
  },
  upload_cover: {
    label: '上传封面',
    icon: ImageIcon,
    color: 'bg-rose-500/10 text-rose-400 border-rose-500/30',
  },
};

export const STATUS_CONFIG: Record<string, StatusConfigItem> = {
  success: {
    label: '成功',
    icon: CheckCircle2,
    color: 'bg-emerald-500/10 text-emerald-400 border-emerald-500/30',
  },
  failed: {
    label: '失败',
    icon: XCircle,
    color: 'bg-red-500/10 text-red-400 border-red-500/30',
  },
  partial: {
    label: '部分成功',
    icon: AlertTriangle,
    color: 'bg-amber-500/10 text-amber-400 border-amber-500/30',
  },
  skipped: {
    label: '跳过',
    icon: MinusCircle,
    color: 'bg-zinc-500/10 text-zinc-400 border-zinc-500/30',
  },
};
