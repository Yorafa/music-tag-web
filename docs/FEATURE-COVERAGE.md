# Feature Coverage Matrix（功能覆盖矩阵）

> 单一真实来源：本表标注 README 中宣称的功能在代码里的实现状态（已落地 / 部分落地 / 未展开）。与 `README.md` 「核心功能 / Features」段双向耦合。
>
> 交叉引用：
> - [`docs/plugable-plugins.md`](docs/plugable-plugins.md) — 设计文档，把它列出的 ❌ 项目（Stage B/C/D）作为未来的 staging 计划；本表与该文档的"aspirational"项是协调关系。

**图例**
- ✅ **Implemented** — 对应代码路径、文件引用，端到端可用
- 🚧 **Partial** — 部分路径已实现，仍有已知 gap（gap 在后文标明）
- ❌ **Not implemented** — 是 aspirational；除 `node_modules` 与自动生成的 proto 外，grep 在仓库内返回 0 matches

**Path 引用采取"目录锚定"而非"行号锚定"。** 行号会随 commit 漂移；只要文件 / 段落的对应关系还在，矩阵就有用。

**最近一次 audit**：与 `frontend/src/**/*.{ts,tsx}` + `internal/{gateway,tasks,plugin,tag}/**` + `cmd/plugins/**/server.go` 逐一对照。

---

## 1. 标签编辑

| Claim | Status | Where |
|---|---|---|
| 全格式音频 ID3 / Vorbis / APE tag 读 | ✅ | `internal/tag/reader.go`（通过 `bogem/id3v2` + `dhowden/tag` 做格式分发） |
| 全格式 ID3 写 / 侧车歌词/封面 | ✅ | `internal/tag/writer.go`（lyric write + `HandleSidecars` 的 `is_save_lyrics_file` / `is_save_album_cover`） |
| 批量编辑 | ✅ | `internal/gateway/handler/update.go` 中 `BatchUpdateID3` + `internal/tasks/batchtag.go`（asynq batch 状态机） |
| 单条编辑 | ✅ | `frontend/src/components/editor/TagEditor.tsx`（MusicTagInfo 实时表单） |
| 列编辑（行内 inline） | 🚧 | 通过 selection+apply 实现批量编辑；per-row live-edit UI 仅支持单行 |

## 2. 元数据刮削与查找

| Claim | Status | Where |
|---|---|---|
| 多源音乐元数据（7 个 plugins） | ✅ | `internal/plugin/{netease,kugou,kuwo,migu,qmusic,musicbrainz,acoustid}/server.go` |
| 多源 fan-out 聚合 | ✅ | `internal/gateway/handler/tag.go` 中 `SearchMusic` → `plugin.ListTagSources()`（Stage A） |
| 网易云 / 酷狗 / 酷我 / 咪咕 / QQ 搜索 + FetchId3 | ✅ | 各 plugin `server.go` 中的 `Search` + `FetchId3ByTitle` |
| MusicBrainz lookup | ✅ | `internal/plugin/musicbrainz/server.go`（不支持歌词，`SupportsLyric=false`） |
| AcoustID 指纹匹配 | ✅ | `internal/plugin/acoustid/server.go`（`fpcalc` shell-out，结果为空时优雅降级） |
| 搜索源动态列表（`GET /api/sources/`） | ✅ | `internal/gateway/handler/source.go` 中 `ListSources` + `frontend/src/store/useSourceStore.ts` |
| 用户源启用 / 关闭（`localStorage` 持久化） | ✅ | `frontend/src/store/useSourceStore.ts` `persist` -> `localStorage["app.enabledSources"]` |
| 来源偏好设置页 | ✅ | `frontend/src/components/settings/SettingsModal.tsx` |

| 来源偏好设置页 | ✅ | `frontend/src/components/settings/SettingsModal.tsx` |
| fan-out 单源失败隔离（容忍 dirty upstream） | ✅ | `internal/gateway/handler/tag.go::SearchMusic` 内部 fan-out 收敛容错；具体行为：`internal/plugin/kuwo/server.go::doSearch`（bad JSON → log + 0 条 + nil err）+ `internal/plugin/qmusic/server.go`（`"list"` 空哨兵 → typed unmarshal 跳过）。运维**必须知道**：前端的 "启用 → 无结果" 不等于 "源坏掉" / "无匹配"；silent-fail log 落在 `[SearchMusic] plugin "<src>": ...`，第一排查动作是 grep 这行。 |

## 3. 歌词

| Claim | Status | Where |
|---|---|---|
| 多源歌词拉取（网易云 / 酷我 / 咪咕 / QQ） | ✅ | netease / kuwo / migu / qmusic plugin 的 `FetchLyric`，由 `handler FetchLyric` 转发 |
| 写入 lyrics tag + 同名 `.lrc` sidecar | ✅ | `internal/tag/writer.go`（lyrics write + `HandleSidecars`） |
| 内嵌双语歌词（中文-英文混排 + 翻译） | ❌ | 在 `frontend/src/**` 与 `internal/**` 跨仓 grep 返回 0 调用方；没有机器翻译 helper，也没有 sidecar 合并逻辑。仅 aspirational。 |

## 4. 封面

| Claim | Status | Where |
|---|---|---|
| 远端封面拉取（跨源） | ✅ | `handler/update.go` 中的 `fetchRemoteBytes`（带 `netguard` SSRF 防护） |
| 上传自定义封面 | ✅ | `internal/gateway/handler/file.go` 中 `UploadCover` |
| 批量导出封面（zip） | ❌ | 单图上传可用；批量 cover export UI 未实现 |

## 5. 曲库 / 文件管理

| Claim | Status | Where |
|---|---|---|
| 目录递归扫描 | ✅ | `internal/tasks/scanner.go`（递归，symlink-aware） |
| 多维度排序（文件名 / 大小 / 更新时间） | ✅ | `frontend/src/components/files/FileBrowser.tsx` |
| 文件按 艺术家 / 专辑 分组 | 🚧 | 前端有 `groupBy` selector 的部分草稿；完整的 album / artist grouping UI 未串通（目前只 sort） |
| 文件名解析（从 `Artist - Title.flac` 自动提取） | 🚧 | `frontend/src/api/client.ts` 中 `parseFromFilename` 已实现；handler round-trip 还在排队 |
| 整轨 APE / FLAC + CUE 自动切割（`shntool` / `cuebreakpoints`） | ❌ | 在仓库内 grep `cuesheet|splitCue|shntool|cuebreakpoints` 返回 0 hits；没有对应 worker task。aspirational。 |
| 集成 ffmpeg（通过 ffmpeg binary 做格式转换） | ❌ | `Dockerfile.worker` 只装了 `yt-dlp`，没有 ffmpeg；没有 `internal/tasks/ffmpeg.go`。aspirational。 |
| 整轨 APE → 多 track 拆轨 | ❌ | 同上；whole-track grab 未连 |

## 6. 文本清洗 / 编码

| Claim | Status | Where |
|---|---|---|
| 批量文本替换（tag cleanup） | 🚧 | `TagEditor.tsx` 已有 Replace 模态框；后端没有对应的 bulk text-replace endpoint |
| 繁简 / 简繁 metadata 转换（zhconv） | ❌ | `internal/tasks/matchscore.go` 的注释里明确 defer：P1 暂不引入 zhconv。前端没有 `opencc` / `HanziConvert`。aspirational。 |
| 常见乱码 / 多余字符清洗 | 🚧 | 前端有几个 trim helper；后端没有统一的清理 pass |

## 7. 下载 / youtube-dl

| Claim | Status | Where |
|---|---|---|
| yt-dlp 下载（YouTube / B 站等） | ✅ | `internal/tasks/yt_dl.go` + `cmd/plugins/*/server.go`（download sources：netease/kugou/kuwo/migu/qmusic） |
| yt-dlp 参数 sanitize（纵深防御） | ✅ | `internal/tasks/yt_dlp_validate.go`（`SanitizeYTDLPFormat` / `SanitizeYTDLPOutputFormat` / `SanitizeYTDLPQuality`）+ `handler/youtube.go` 中的 pre-sanitize |

## 8. 安全 / 运维卫生

| Claim | Status | Where |
|---|---|---|
| JWT 鉴权（in-memory，Fail-closed） | ✅ | `internal/gateway/handler/auth.go` + `config.Load()` 的占位 guard |
| bcrypt 密码 hash | ✅ | `handler/auth.go` 中 `loadUsers` 同时支持明文与 `$2a$…` |
| CORS 白名单（无反射） | ✅ | `internal/gateway/middleware/cors.go` |
| gRPC TLS 可选 | ✅ | `internal/plugin/grpc_adapter.go` 中 `DialOptions{UseTLS, CAFile}` |
| SSRF 拒 169.254 / RFC1918 | ✅ | `internal/netguard/ssrf.go` |
| 路径遍历（`SafeJoin`） | ✅ | `internal/utils/pathjoin.go` |
| 完整操作日志（per-file edit changelog + UI） | ❌ | `internal/db/models.go` 没有 `OperationLog` 表；`frontend/**` 没有对应 UI surface。aspirational。 |

## 9. UI / 设备覆盖

| Claim | Status | Where |
|---|---|---|
| 全响应式手机 UI（Tailwind sm:/md: 触发） | ✅ | `frontend/src/components/layout/AppShell.tsx`、`FileBrowser.tsx` 等使用了 Tailwind responsive utilities |

## 10. 播放 / 统计

| Claim | Status | Where |
|---|---|---|
| 播放数据柱形图 / 折线图 | ❌ | 在 `frontend/src/**` grep `BarChart` / `LineChart` / `recharts` / `plays_count` 0 hits。`internal/db/models.go` 的 `AccessedDate` 列只是 *reserved-for-future*，未消费。aspirational。 |
| 整合外部播放端统计上报 | ❌ | 没有 Subsonic-compatible `/rest/` endpoints（P2.0 已退役）；没有 playback webhook。aspirational。 |

---

## Deferred / Aspirational 汇总

> 这些 ❌ 项在 README 中已经宣称，但实际在 `frontend/src/**` + `internal/**` 跨仓 grep 返回 0 实现。
>
> **Status: aspirational-only. NOT in current roadmap。** PR 欢迎，但没有承诺任何特定 ❌ 在某版本内落地。git 历史中可见早期上下文（P2.0 移除 Django-era 表面）。与 [`docs/plugable-plugins.md`](docs/plugable-plugins.md) Stage B/C/D 的 staging 计划是 *相邻但独立* 的两套——后者属于插件粒度的扩展，与"自托管工具现状"不重合。选集时按所问问题对应文档，不要混为一谈。

| ❌ 项 | 暂缓原因 |
|---|---|
| 整轨 APE/FLAC/CUE 切割分轨（`shntool` / `cuebreakpoints`） | 需要再加一个 binary 到 image；worker image 目前只装 `yt-dlp` + ca-certs |
| ffmpeg 音频格式转换（任意 ↔ 任意） | 给 worker image 增加约 30 MB + 二进制许可证审核；P1 范围刻意只做 ID3 + sidecar + download |
| 繁简 / 简繁 metadata 转换（`zhconv` / `opencc-wasm`） | `matchscore.go` 显式 defer；Django-era 的 `zhconv` 库需要纯 Go 移植；`opencc-wasm` 增加 2-3 MB 到前端 bundle |
| 双语歌词翻译 + 合并写入 | 需要在线翻译 API（成本 + 隐私）。P1 只保留原歌词 + sidecar |
| 单条 + 批量 操作日志（model + UI） | Django 时代有 `operation_log`；这次 model 没迁，因为 UI 从未在生产 telemetry 中真正使用 |
| 播放统计 + 图表（model 列已留空，UI 缺失） | 上游没有 playback source-of-truth（SPA 不驱动播放；gateway 从 `/media/*` 出文件，不记录 listen count）。加 Subsonic 风格 counter 需要独立的 stats-collector 服务 |
| 批量封面导出 | 单图上传 OK；bulk-zip export 是个 UX 升级，需要 worker task + zip 库 |
| 同源问题（lib declining） | |

如果要从中挑一项复活，看 GitHub issues 里 `wontfix` / `P2` tag 的——这是维护人已 triage 过的候选。同样地，[`docs/plugable-plugins.md`](docs/plugable-plugins.md) Stage B（per-source YAML override）、C（sandboxed JS plugin runtime）、D（runtime admin UI）是 aspirational 的、考虑 self-host 可用性的：它们扩展插件粒度，与本矩阵相互独立。

---

## 如何更新本文档

当某个之前未实现的项被实现时：

1. 把该 row 的 Status 标为 ✅，并在 Where 列补一个具体的文件 / section 引用（**不要**用行号——目录锚定经得起 commit 间的微小漂移）。
2. 同步更新 `README.md` Features 列表（每个 bullet 配一个 emoji 标，❌/🚧 项可加一句理由）。在列表末尾让用户回流到本文档。
3. 在 status 表里加一行（如果关闭了一个被跟踪的 gap），或在 § H aspirational 段扩写（如果仍然挂着）。

当有意弱化或删除一个功能时：

1. 把对应 row 移到"Removed"区（删除 row，加一行 `Removed YYYY-MM`）。
2. 把 README 中的对应 bullet 删除。
3. 在 CHANGELOG 风格说明里引用这次 deprecation。
