# Feature Coverage Matrix

> Single source of truth: which READMEs-claimed features are wired into code, partially wired, or open work. Tightly coupled with `README.md` / `readme_en.md` 核心功能 / Features list.

**Legend**
- ✅ **Implemented** — concrete code path, file:line reference, end-to-end usable
- 🚧 **Partial** — wired for one path but has known gap (named below)
- ❌ **Not implemented** — claim is aspirational, code grep returns zero matches outside `node_modules` / generated proto

**Last audit**: cross-referenced against `frontend/src/**/*.{ts,tsx}` + `internal/{gateway,tasks,plugin,tasks}/**` + `cmd/plugins/**/server.go`.

---

## 1. Tag editing

| Claim | Status | Where |
|---|---|---|
| 全格式音频 ID3 / Vorbis / APE tag 读 (`internal/tag/reader.go`) | ✅ | `internal/tag/reader.go` (format dispatch via `bogem/id3v2` + `dhowden/tag`) |
| 全格式 ID3 写 / 侧车歌词/封面 (`internal/tag/writer.go`) | ✅ | `internal/tag/writer.go:108` lyrics, `:162` `HandleSidecars` (`is_save_lyrics_file`, `is_save_album_cover`) |
| 批量编辑 (`handler.BatchUpdateID3` + `tasks/batchtag.go`) | ✅ | `internal/gateway/handler/update.go` `BatchUpdateID3` + `internal/tasks/batchtag.go` (asynq batch state machine) |
| 单条编辑 (`TagEditor.tsx`) | ✅ | `frontend/src/components/editor/TagEditor.tsx` (MusicTagInfo live form) |
| 列编辑 (inline) | 🚧 | Batch-edit is handled by selection + apply; per-row edit UI is single-row only (see `TagEditor.tsx`) |

## 2. Metadata scraping & lookup

| Claim | Status | Where |
|---|---|---|
| 多源音乐元数据 (7 plugins) | ✅ | `internal/plugin/{netease,kugou,kuwo,migu,qmusic,musicbrainz,acoustid}/server.go` |
| 多源 fan-out 聚合 | ✅ | `internal/gateway/handler/tag.go` `SearchMusic` → `plugin.ListTagSources()` (after Stage A) |
| 网易云 / 酷狗 / 酷我 / 咪咕 / QQ 搜索 + FetchId3 | ✅ | per-plugin `server.go` `Search` + `FetchId3ByTitle` |
| MusicBrainz lookup | ✅ | `internal/plugin/musicbrainz/server.go` (no lyric, `SupportsLyric=false`) |
| AcoustID 指纹匹配 | ✅ | `internal/plugin/acoustid/server.go` (fpcalc shell-out, graceful empty-result on missing binary) |
| 搜索源动态列表 (`GET /api/sources/`) | ✅ | `internal/gateway/handler/source.go` `ListSources` + `frontend/src/store/useSourceStore.ts` |
| 用户源启用 / 关闭 localStorage 持久化 | ✅ | `frontend/src/store/useSourceStore.ts` `persist` → `localStorage["app.enabledSources"]` |
| 来源偏好设置页 (`SettingsModal.tsx`) | ✅ | `frontend/src/components/settings/SettingsModal.tsx` |

## 3. Lyrics

| Claim | Status | Where |
|---|---|---|
| 多源歌词拉取 (网易云 / 酷我 / 咪咕 / QQ) | ✅ | netease/kuwo/migu/qmusic plugin `FetchLyric`, dispatch in `handler FetchLyric` |
| 写入 lyrics tag + sidecar `.lrc` | ✅ | `writer.go:108` + `:162` `HandleSidecars` (`is_save_lyrics_file`) |
| 内嵌双语歌词 (中文-英文混排 + 翻译) | ❌ | grep across `frontend/src/**` and `internal/**` returns 0 callers; no machine-translation helper, no sidecar merge. Aspirational only. |

## 4. Cover art

| Claim | Status | Where |
|---|---|---|
| 远端封面拉取 (跨源) | ✅ | `handler/update.go` `fetchRemoteBytes` (`netguard` SSRF guard) |
| 上传自定义封面 | ✅ | `internal/gateway/handler/file.go` `UploadCover` |
| 批量导出封面 (zip?) | ❌ | single-file upload works; per-batch cover export UI not implemented. Aspirational. |

## 5. Library / file management

| Claim | Status | Where |
|---|---|---|
| 目录 scan (`scanner.go`) | ✅ | `internal/tasks/scanner.go` (recursive, symlink aware) |
| 多维度排序 (文件名/大小/更新时间) | ✅ | `frontend/src/components/files/FileBrowser.tsx` |
| 文件按 艺术家/专辑 分组 | 🚧 | frontend has `groupBy` selector partial draft; full album/artist grouping UI not wired (only sort) |
| 文件名解析 (parse from `Artist - Title.flac`) | 🚧 | `frontend/src/api/client.ts` `parseFromFilename` exists; handler coverage pending |
| 整轨 APE/FLAC 自动切割 (`shntool` / `cuebreakpoints`) | ❌ | grep `cuesheet|splitCue|shntool|cuebreakpoints` returns 0 in code; no worker task. Aspirational. |
| 集成 ffmpeg (format conversion via ffmpeg binary) | ❌ | Dockerfile.worker installs `yt-dlp` but NOT `ffmpeg`; no `internal/tasks/ffmpeg.go`. Aspirational. |
| 整轨 APE → 多 track 拆轨 | ❌ | 同上，整轨/grab track 切割未实现 |

## 6. Text cleanup / encoding

| Claim | Status | Where |
|---|---|---|
| 批量文本替换 tag-cleanup | 🚧 | `TagEditor.tsx` ships Replace modal; backend has no equivalent bulk endpoint |
| 繁简转换 (t2s / s2t / s2tw) | ❌ | `internal/tasks/matchscore.go:2` explicitly defers: `// 与 applications/task/utils.py 对齐；P1 暂不引入 zhconv 繁简转换。`. P2 candidate. |
| 常见乱码 / 多余字符清洗 | 🚧 | frontend has trim helpers; backend has no canonical cleanup pass |

## 7. Download / youtube-dl

| Claim | Status | Where |
|---|---|---|
| YouTube 下载 via yt-dlp | ✅ | `internal/tasks/yt_dl.go` + `cmd/plugins/*/server.go` (download sources: netease/kugou/kuwo/migu/qmusic) |
| yt-dlp 参数 sanitize (defense-in-depth) | ✅ | `internal/tasks/yt_dlp_validate.go` (`SanitizeYTDLPFormat/OutputFormat/Quality`) + handler-level pre-sanitize in `handler/youtube.go` |

## 8. Security / operation hygiene

| Claim | Status | Where |
|---|---|---|
| JWT 鉴权 (in-memory, fail-closed) | ✅ | `internal/gateway/handler/auth.go` + `config.Load()` Fail-closed placeholder guard |
| bcrypt 密码 hash | ✅ | `handler/auth.go` `loadUsers` accepts both plain + `$2a$…` |
| CORS 白名单（无反射） | ✅ | `internal/gateway/middleware/cors.go` |
| gRPC TLS 可选 | ✅ | `internal/plugin/grpc_adapter.go` `DialOptions{UseTLS, CAFile}` |
| SSRF 拒 169.254 / RFC1918 | ✅ | `internal/netguard/ssrf.go` |
| 路径遍历 (`SafeJoin`) | ✅ | `internal/utils/pathjoin.go` |
| 完整操作日志记录 (per-file edit changelog + UI) | ❌ | model `OperationLog` 不存在（`internal/db/models.go` 没有 OperationLog 表），UI `operationLog` 缺失。aspirational. |

## 9. UI / device coverage

| Claim | Status | Where |
|---|---|---|
| 全响应式手机 UI (Tailwind sm:/md: 触发) | ✅ | `frontend/src/components/layout/AppShell.tsx`, `FileBrowser.tsx` 等使用 Tailwind responsive utilities |
| 适配小爱同学 本地曲库播放 | ❌ | 0 grep matches for `XiaoAI` / `xiaomi` / 小爱 / 米家. no API integration. aspirational. |
| 兼容各类私人网盘挂载曲库在线播放 | ✅ (环境-wise) | nginx volume mount via `${MUSIC_DIR}` 服务任意 bind-mount — playlist URL `/media/...` 由 nginx 直出. no application code required for "cloud drive" — only docker-compose volume config. |

## 10. Playback / statistics

| Claim | Status | Where |
|---|---|---|
| 播放数据统计 柱形 / 折线 图 | ❌ | `frontend/src/**` 0 matches for `BarChart` / `LineChart` / `recharts` / `plays_count`. `internal/db/models.go:89` has `AccessedDate` *reserved-for-future* comment — column not yet populated. aspirational. |
| 整合外部播放端统计上报 | ❌ | no Subsonic-compatible `/rest/` endpoints (P2.0 retired); no playback webhook. aspirational. |

---

## Deferred / aspirational summary (one-liner)

These are claims that exist in the README but currently return 0 implementations when grepped across `frontend/src/**` + `internal/**`:

- ❌ 整轨 APE/FLAC/CUE 切割分轨 (`shntool` / `cuebreakpoints`)
- ❌ ffmpeg 音频格式转换（任一 → 任一）
- ❌ 繁简/简繁 metadata 转换 (`zhconv` / `opencc`-wasm)
- ❌ 内嵌双语歌词翻译 + 合并写入
- ❌ 单条 + 批量 操作日志 (model + UI 都没有)
- ❌ 播放统计 + 图表 (model column exists but unused; UI 缺失)
- ❌ 适配小爱同学 / 米家 联动 (no API)
- ❌ 批量封面导出 (单条上传已实现)

## Why these are deferred (not removed)

Each item has a known architecture reason (per `P1.5.md` / planning notes):

| Item | Reason |
|---|---|
| 整轨切割 | requires shelling out to a second binary (`shntool` / `cuetools`) and chapter-frame preservation; not in Docker image (worker image only installs `yt-dlp` + ca-certs). |
| ffmpeg 转换 | adds ~30 MB to the worker image + license review for embedded ffmpeg; P1 deliberately scoped to ID3 + sidecar + download, leaving conversion to a future "media" plugin. |
| 繁简转换 | `matchscore.go:2` explicitly deferred — the Django-era `zhconv` lib wasn't migrated to Go because it would need a pure-Go port; `opencc-wasm` adds 2-3 MB to the frontend bundle. |
| 双语歌词 | requires an online translation API (cost + privacy). P1 keeps raw lyrics + sidecar only. |
| 操作日志 | Django had `operation_log` per file; the model wasn't migrated because the UI was never used in production telemetry. Adding it now costs a model migration + a UI surface area for a feature the audience hasn't asked for. |
| 播放统计 | no playback source-of-truth upstream (the SPA does not drive playback; nginx serves files). Adding a Subsonic-style counter requires a separate stats-collector service. |
| XiaoAI 联动 | vendor API is undocumented + China-only. Decided on privacy grounds. |
| 批量封面导出 | single-image upload works; bulk-zip export is a UX refinement that requires a worker task + zip library. |

When picking which deferred items to revive, look at GitHub issues tagged `wontfix` / `P2` — these are the candidates the maintainer has already triaged.

---

## How to update this file

When you implement a previously-not-implemented item:

1. Mark ✅ in the row + add concrete `path:line` reference.
2. Flip the matching marker in `README.md` and `readme_en.md` (`✅ → 🚧` when partially), and vice-versa.
3. Add a row to `P1.5.md` under the `✅` table if it closed a tracked gap, or under `P1.5 followups` if it remains open.

When you intentionally weaken or remove a feature:

1. Move the corresponding row to "Removed" (delete the row).
2. Remove the matching line from both READMEs.
3. Reference the deprecation in a CHANGELOG-style note.

