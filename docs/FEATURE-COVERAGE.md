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
| 批量编辑 | ✅ | `internal/gateway/handler/update.go` 中 `BatchUpdateID3`（前端按目录分组提交；worker 侧 `tag:batch_auto` 链路已删除——它从不写入 `task_taskrecord` 行，处理数恒为 0，且前端无调用方） |
| 单条编辑 | ✅ | `frontend/src/components/editor/TagEditor.tsx`（MusicTagInfo 实时表单） |
| 列编辑（行内 inline） | 🚧 | 通过 selection+apply 实现批量编辑；per-row live-edit UI 仅支持单行 |

## 2. 元数据刮削与查找

| Claim | Status | Where |
|---|---|---|
| 多源音乐元数据（7 个 plugins） | ✅ | `internal/plugin/{netease,kugou,kuwo,migu,qmusic,musicbrainz,acoustid}/server.go` |
| 多源 fan-out 聚合 | ✅ | `internal/gateway/handler/tag.go` 中 `SearchMusic` → `plugin.ListTagSources()`（Stage A） |
| 网易云 / 酷狗 / 酷我 / 咪咕 / QQ 搜索 + FetchId3 | ✅ | 各 plugin `server.go` 中的 `Search` + `FetchId3ByTitle` |
| MusicBrainz lookup | ✅ | `internal/plugin/musicbrainz/server.go`（不支持歌词，`SupportsLyric=false`） |
| AcoustID 指纹匹配 | ✅ | `internal/plugin/acoustid/server.go`（`fpcalc` shell-out → `POST api.acoustid.org/v2/lookup`，结果为空时优雅降级）。需 `ACOUSTID_API_KEY`（可回退到官方公共测试 key，会过期）。声纹比对在 fan-out 里与其它源平级，失败只记日志不阻断 |
| 重复文件检测 · 声纹层（跨编码） | ✅ | `internal/dedup/fingerprint.go`（阈值策略）+ `internal/fingerprint`（fpcalc 调用与子指纹位距离，三个调用方共用）。按 `music_folder.duration` 选候选（同一首歌换编码大小可差 25 倍，按大小选会漏掉最典型的 flac/mp3 重压），再按 **子指纹位距离** 判同，阈值 0.90。实测同一首歌 7 种编码相似度 0.995+，无关音频 0.511 |
| 声纹索引自维护 | ✅ | `index:fp_duration` 不再只在 worker 启动时跑一次。跑完若**有进展**就排下一次（延迟 30s、`Unique` 去重），因此索引会追上曲库然后自己停下 —— 比“从每个生产者 enqueue”（三处调用点、下一个写库的人会忘）可靠，也比定时轮询（对没变化的库永远醒着）省。空跑不重排，否则一个解不出码的文件会被永远重试。`pendingFiles` 除 `duration=0` 外还挑**缓存已失效**的行（size/mtime 变了），修的是「原地转码后路径和 duration 都没变，于是永远不再被看一眼」 |
| 下载入库的文件对查重可见 | ✅ | `internal/dedup/audiotable.go`。`music_folder.file_type` 曾有三个写入方各写各的（scanner 写 `music`，yt_dl 写**音源名** `youtube`/`netease`…），于是所有按 `file_type='music'` 过滤的查询都**漏掉了每一个「加入库」下载的音轨**：拿不到 duration → 永不进候选集 → 它的重压副本查不出来，且全程无任何日志。现在改为**按扩展名**（`audioext`）判定，**按是否在 MUSIC_DIR 下**判定是否属于曲库（这才是把 `/tmp/audio_cache` 排除掉的那条约束，且不会随写入方漂移）。音源名仍记在 `TaskRecord.source` / 审计日志 / 缓存路径里 |
| 声纹缓存 | ✅ | `music_folder.{fingerprint,fp_size,fp_mtime}` + `internal/dedup/fpcache.go`。一次 `fpcalc` 解码 120 秒音频要 0.4s，而一次查重要解码**被测文件 + 时长窗口内每个候选**，所以重复查重（尤其刚做完的「查重」按钮）原本每次都重付一遍这笔钱。缓存后重复查重零解码。失效判据是 size **和** mtime（纳秒）：原地转码路径和行都不变，没有失效判据的缓存不是变陈旧，而是**永远报旧歌的重复**。索引任务顺带存指纹几乎免费 —— fpcalc 是解码瓶颈，读一行 DURATION 和读全部指纹都是 0.4s，所以原先「只读 DURATION 就退出」的优化方向本身是错的。**文件已删但行还在**时 stat 失败即视为 miss（扫描器和 tidy 都不清理这类残留行），否则一个已删音轨会靠缓存blob 永久留在候选集里，指向一个打不开的路径。实测重复查重 400ms → 24ms |
| 曲库查重页（只读） | ✅ | `POST /api/check_duplicate/`（`internal/gateway/handler/duplicate.go`）。智能刮削页选中行 → 「查重」→ 行上 `重复`/`疑似`/`唯一` 徽章 + 侧栏「只看重复」筛选。与写入路径共用 `dedupCheckFor`，两边判定不会分歧。逐行返回（单个文件不可查不拖垮整批），路径经 `SafeJoin` 限制在 MUSIC_DIR 内 |
| 删除重复文件 | ✅ | `POST /api/delete_files/`（同上文件）。仅接受内容级 `duplicate` 判定；两侧互相指认时两边都不删。实现为**移入 `DATA_DIR/.trash/<ts>/`** 而非 unlink（保留相对路径，可恢复；trash 在 MUSIC_DIR 之外所以扫描器与 `http.Dir(MUSIC_DIR)` 都不会再服务它）。拒绝目录 / 符号链接 / 越界路径；跨设备回退到 copy+remove（默认 compose 就是两个独立 bind mount）；删除后同步清掉 `music_folder` 索引行（否则陈旧的 `duration` 会让已删文件永久留在候选集里），并记一条 `delete_files` 审计 |
| 搜索源动态列表（`GET /api/sources/`） | ✅ | `internal/gateway/handler/source.go` 中 `ListSources` + `frontend/src/store/useSourceStore.ts` |
| 用户源启用 / 关闭（`localStorage` 持久化） | ✅ | `frontend/src/store/useSourceStore.ts` `persist` -> `localStorage["app.enabledSources"]` |
| 来源偏好设置页 | ✅ | `frontend/src/components/settings/SettingsModal.tsx` |
| per-source config override (C.4 Stage B) | ✅ | `internal/config/loader.go` + `internal/plugin/{kuwo,kg,migu,qmusic}/server.go` (const→var + `SetSecret`/`SetAPIBase`) + `frontend/src/components/settings/SettingsModal.tsx` Sources tab. 详见 `docs/plans/Unfinished-Features.md § C.4` |
| fan-out 单源失败隔离（容忍 dirty upstream） | ✅ | `internal/gateway/handler/tag.go::SearchMusic` 内部 fan-out 收敛容错；具体行为：`internal/plugin/kuwo/server.go::doSearch`（bad JSON → log + 0 条 + nil err）+ `internal/plugin/qmusic/server.go`（`"list"` 空哨兵 → typed unmarshal 跳过）。运维**必须知道**：前端的 "启用 → 无结果" 不等于 "源坏掉" / "无匹配"；silent-fail log 落在 `[SearchMusic] plugin "<src>": ...`，第一排查动作是 grep 这行。 |

## 3. 歌词

| Claim | Status | Where |
|---|---|---|
| 多源歌词拉取（网易云 / 酷我 / 咪咕 / QQ） | ✅ | netease / kuwo / migu / qmusic plugin 的 `FetchLyric`，由 `handler FetchLyric` 转发 |
| 写入 lyrics tag + 同名 `.lrc` sidecar | ✅ | `internal/tag/writer.go`（lyrics write + `HandleSidecars`） |

## 4. 封面

| Claim | Status | Where |
|---|---|---|
| 远端封面拉取（跨源） | ✅ | `handler/update.go` 中的 `fetchRemoteBytes`（带 `netguard` SSRF 防护） |
| 上传自定义封面 | ✅ | `internal/gateway/handler/file.go` 中 `UploadCover` |

## 5. 曲库 / 文件管理

| Claim | Status | Where |
|---|---|---|
| 目录递归扫描 | ✅ | `internal/tasks/scanner.go`（递归，symlink-aware） |
| 多维度排序（文件名 / 大小 / 更新时间） | ✅ | `frontend/src/components/files/FileBrowser.tsx` |
| 文件按 艺术家 / 专辑 分组 | ✅ | `frontend/src/store/useWorklistStore.ts` (grouping + `worklist.grouping.v1`) + `Worklist.tsx` (`deriveGrouped`) + `GroupHeaderRow.tsx` (新) + `WorklistHeaderBar.tsx` (chip row). 详见 `docs/plans/Unfinished-Features.md § C.3` |
| 文件名解析（从 `Artist - Title.flac` 自动提取） | ✅ | `internal/utils/filenames.go` (regex source-of-truth) + `internal/cache/parsed_preview.go` (10-min TTL cache) + `internal/tasks/parsedfilenames.go` (asynq worker) + `frontend/src/utils/parseFilename.ts` (TS mirror) + `ParseFilenamesModal.tsx`. 详见 `docs/plans/Unfinished-Features.md § C.2` |

## 6. 文本清洗 / 编码

| Claim | Status | Where |
|---|---|---|
| 批量文本替换（tag cleanup） | 🚧 | `TagEditor.tsx` 已有 Replace 模态框；后端没有对应的 bulk text-replace endpoint |
| 常见乱码 / 多余字符清洗 | 🚧 | 前端有几个 trim helper；后端没有统一的清理 pass |

## 7. 下载 / youtube-dl

| Claim | Status | Where |
|---|---|---|
| yt-dlp 下载（YouTube） | ✅ | worker 的 `download:generic` 委托 youtube 插件执行（`internal/plugin/youtube/server.go`），worker 本体纯 Go |
| yt-dlp 参数 sanitize（纵深防御） | ✅ | `internal/ytdlp`（`SanitizeYTDLPFormat` / `SanitizeYTDLPOutputFormat` / `SanitizeYTDLPQuality`）三层防线：gateway handler 预校验 → worker 重放校验 → youtube 插件拼 argv 前再校验 |

## 8. 安全 / 运维卫生

| Claim | Status | Where |
|---|---|---|
| JWT 鉴权（in-memory，Fail-closed） | ✅ | `internal/gateway/handler/auth.go` + `config.Load()` 的占位 guard |
| bcrypt 密码 hash | ✅ | `handler/auth.go` 中 `loadUsers` 同时支持明文与 `$2a$…` |
| CORS 白名单（无反射） | ✅ | `internal/gateway/middleware/cors.go` |
| gRPC TLS 可选 | ✅ | `internal/plugin/grpc_adapter.go` 中 `DialOptions{UseTLS, CAFile}` |
| SSRF 拒 169.254 / RFC1918 | ✅ | `internal/netguard/ssrf.go` |
| 路径遍历（`SafeJoin`） | ✅ | `internal/utils/pathjoin.go` |
| 完整操作日志（per-file edit changelog + UI） | ✅ | `internal/db/models.go` (`OperationLog`) + `internal/audit/audit.go` + `internal/gateway/handler/operation_log.go` + `frontend/src/components/audit/OperationLogsTab.tsx` |

## 9. UI / 设备覆盖

| Claim | Status | Where |
|---|---|---|
| 全响应式手机 UI（Tailwind sm:/md: 触发） | ✅ | `frontend/src/components/layout/AppShell.tsx`、`FileBrowser.tsx` 等使用了 Tailwind responsive utilities |

---

## Deferred / Aspirational 汇总

> 这些 ❌ 项在 README 中已经宣称，但实际在 `frontend/src/**` + `internal/**` 跨仓 grep 返回 0 实现。
>
> **Status: aspirational-only. NOT in current roadmap。** PR 欢迎，但没有承诺任何特定 ❌ 在某版本内落地。git 历史中可见早期上下文（P2.0 移除 Django-era 表面）。与 [`docs/plugable-plugins.md`](docs/plugable-plugins.md) Stage B/C/D 的 staging 计划是 *相邻但独立* 的两套——后者属于插件粒度的扩展，与"自托管工具现状"不重合。选集时按所问问题对应文档，不要混为一谈。

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
