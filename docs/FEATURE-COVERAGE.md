# Feature Coverage Matrix（功能覆盖矩阵）

> 单一真实来源：本表标注 README 中宣称的功能在代码里的实现状态（已落地 / 部分落地 / 未展开）。README 的「核心功能」只是一张一句话速览表，每一行都对应本表的一行；**理由、取舍、`path` 引用只写在这里**。
>
> 交叉引用：
> - [`plugable-plugins.md`](plugable-plugins.md) — 设计文档。它的 Stage A / B **已 ship**（见 §2 的 `per-source config override`），Stage C（goja 沙箱 JS plugin）/ D（runtime admin UI）仍是 future pitch；本表与该文档的"aspirational"项是协调关系。

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
| 批量编辑（机器填值） | ✅ | `internal/gateway/handler/update.go` 中 `BatchUpdateID3`（前端按目录分组提交） |
| 批量编辑（人工填值） | ✅ | `frontend/src/components/workstation/BatchEditDialog.tsx` + `batchEdit.ts`。选区 ≥ 2 首时工具栏出现「批量编辑标签」：一份表单写入选中全部曲目，每个字段带「不修改」勾选（**默认勾选**），取消勾选后留空即**清空该标签**。一次请求覆盖跨目录选区（`file_full_path: ""` + 含斜杠的相对路径，服务端 `SafeJoin` 逐行做包含性检查）。1 首时故意不出现——那一行的编辑面是 `TrackInspector`，两个入口做同一件事会让用户问「我改的到底是哪个」 |
| 批量清空标签（删除 tag） | ✅ | `tag.TagUpdate` 的 `Clear*` 开关 + `writer.go` 两条写入路径（taglib 写空值 / id3v2 `DeleteFrames`）。**线上契约：JSON `null` = 清除该 tag，key 缺失 = 不动，空字符串仍然 = 不动**（`handler.tagIntent`）。空字符串 ≠ 清除：单条表单把全部字段展开进 payload，其中大量是用户没碰过的空串，读成删除会把用户没看过的 tag 抹掉 |
| 单条编辑 | ✅ | `frontend/src/components/detail/TrackInspector.tsx`（MusicTagInfo 实时表单） |
| 列编辑（行内 inline） | ❌ | 未实现，**也不打算做**。人工多首填值由上一行的「批量编辑（人工填值）」覆盖；inline 剩下的只有开 spreadsheets：单元级 dirty / 逐行错误态 / 键盘导航 / 失败回滚，而收益只在「N 首每首要改的值都不同」这一种场景。本表标 ❌ 是因为**一行 UI 都没写**，不是「剩个 gap」 |

## 2. 元数据刮削与查找

| Claim | Status | Where |
|---|---|---|
| 多源音乐元数据（7 个 plugins） | ✅ | `internal/plugin/{netease,kugou,kuwo,migu,qmusic,musicbrainz,acoustid}/server.go` |
| 多源 fan-out 聚合 | ✅ | `internal/gateway/handler/tag.go` 中 `SearchMusic` → `plugin.ListTagSources()`（Stage A） |
| 网易云 / 酷狗 / 酷我 / 咪咕 / QQ 搜索 + FetchId3 | ✅ | 各 plugin `server.go` 中的 `Search` + `FetchId3ByTitle` |
| MusicBrainz lookup | ✅ | `internal/plugin/musicbrainz/server.go`（不支持歌词，`SupportsLyric=false`） |
| AcoustID 指纹匹配 | ✅ | `internal/plugin/acoustid/server.go`（`fpcalc -json` → `POST api.acoustid.org/v2/lookup`）。**入口只有一个**：`TrackInspector` 的「计算声纹并在线识别」按钮，逐首调用。它**不在** `smart_tag` 的 fan-out 里——`tag.go::smartTagSources` 显式跳过 `acoustid`，所以刮削永远不会自动走到它。<br>**在线识别经常不出结果，而失败与「库里没有」在界面上完全一样**：`FetchId3ByTitle` 的每个失败分支都返回空结果 + nil error，前端一律弹「声纹未匹配到任何候选」。判断断在哪一步只能看日志（`docker compose logs acoustid`），那里会打 `acoustid: api error <code>: <message>` 或 `fpcalc failed for <path>: ...`。四个已知原因：<br>① **API key**。`ACOUSTID_API_KEY` 未设时回退到 `defaultAPIKey`，那是一把**已过期**的官方示例 key（官方文档现在的示例 key 是另一把，且明写示例 key 几天后自动过期、不要用在真实应用里）。解法：在 https://acoustid.org/login 注册，把真 key 写进 `.env`。<br>② **限流**。官方限制 3 请求/秒，而这把公共 key 由所有抄示例代码的人共用，批量识别必然吃到。<br>③ **文件打不开**。插件要自己 `fpcalc` 打开音频文件，拿到的是 gateway/worker 视角的路径。compose 已挂 `./music:/app/media:ro`，容器内路径一致；**任何非 compose 的部署**会全盘失败。<br>④ **库里真没有**。AcoustID 的指纹库只覆盖被提交过的录音，个人库里的冷门 rip / DJ 版 / 现场版查不到——这一条不是故障。<br>本地查重（下面三行）是**另一条完全独立的路**：不需要网络，也不需要 key。声纹比对在 fan-out 里与其它源平级，失败只记日志不阻断。API 返回的 0..1 `score` 随 `pb.Song.Score` → `plugin.Song.Score` 一路上行，是全系统**唯一一个被真正测量过的**置信度。 |
| 候选匹配度的陈述方式 | ✅ | `internal/plugin/interface.go::Song.Score` / `TitleMatch` + `internal/gateway/handler/tag.go::annotateCandidate` + `frontend/src/components/detail/CandidateCard.tsx`。`Score` 的契约是「对**音频**的置信度 0..1，只有真的测过的源才填」（`omitempty`，不填就是没测过），前端按 `score × 100` 渲染成百分比。标题比较不进这个数字，单列为 `TitleMatch` 事实字段（`exact`/`partial`），UI 上和百分比分开显示。<br>**`annotateCandidate` 不写 `Score`**：三维标题相似度和 `scoreMatch`（0..6）只喂 `sortSongsByRank` 的 rank map，不出网关。把启发式分数折进 `Score` 会同时毁掉两件事——量纲和前端对不上；且刮削时通常只有文件名可比、歌手和专辑是空的，于是每个候选都算出同一个和，唯一一个真实测量出来的数字被覆盖掉。 |
| 重复文件检测 · 声纹层（跨编码） | ✅ | `internal/dedup/fingerprint.go`（阈值策略）+ `internal/fingerprint`（fpcalc 调用与子指纹位距离，三个调用方共用）。按 `music_folder.duration` 选候选（同一首歌换编码大小可差 25 倍，按大小选会漏掉最典型的 flac/mp3 重压），再按 **子指纹位距离** 判同，阈值 0.90。实测同一首歌 7 种编码相似度 0.995+，无关音频 0.511 |
| 声纹索引自维护 | ✅ | `index:fp_duration`：worker 启动时跑一次，跑完若**有进展**就排下一次（延迟 30s、`Unique` 去重），索引因此会追上曲库然后自己停下。空跑不重排，否则一个解不出码的文件会被永远重试。`pendingFiles` 除 `duration=0` 外还挑**缓存已失效**的行（size/mtime 变了）——原地转编码不改变路径和 duration，只靠这两个字段挑行会让这种文件永远不再被看一眼。 |
| 下载入库的文件对查重可见 | ✅ | `internal/dedup/audiotable.go`。查重侧的音频判定**不看** `music_folder.file_type`，而是**按扩展名**（`audioext`）判是不是音频、**按是否在 MUSIC_DIR 下**判属不属于曲库——后者才是把 `/tmp/audio_cache` 排除掉的那条约束，且不随写入方漂移。写入侧统一用 `audioext.FileTypeForRow`。音源名记在 `TaskRecord.source` / 审计日志 / 缓存路径里。 |
| 声纹缓存 | ✅ | `music_folder.{fingerprint,fp_size,fp_mtime}` + `internal/dedup/fpcache.go`。一次 `fpcalc` 解码 120 秒音频要 0.4s，而一次查重要解码**被测文件 + 时长窗口内每个候选**；有了这层缓存，重复查重（尤其刚点完「查重」又点一次）零解码。失效判据是 size **和** mtime（纳秒）——原地转编码时路径和行都不变，缺任一判据都会让缓存**永远报旧歌的重复**。指纹由索引任务顺带写入，代价为零：读一行 DURATION 和读全部指纹是同一次 0.4s 解码。**文件已删但行还在**时 stat 失败即视为 miss，否则一个已删音轨会靠缓存 blob 永久留在候选集里，指向一个打不开的路径。实测重复查重 400ms → 24ms。 |
| 清理残留索引行 | ✅ | `internal/tasks/prune.go` 的 `pruneVanished`，跟在「清理残留」按钮后（与清理空目录同一个任务，审计里分开记 `removed` / `vanished_rows`）。文件从外部消失（文件管理器、宿主机改挂载卷、rsync）时扫描器和 tidy 都不会删它的行，于是残留行累积，**每次查重都要先 stat 再逐个驳回**；曲库页面读的是盘不是表，所以用户看不见它们 —— 纯浪费。**刻意不做集合差集**（`WHERE path NOT IN`，扫描器明文禁止：子扫描失败会连整个没访问到的库一起删掉），而是**逐行问内核文件在不在**，只有确定的 ENOENT 才删；权限/IO 错误一律保留（“不知道”不等于“没了”，测试用 ENOTDIR 而不是 chmod，因为容器里是 root）。folder 行不删（子行的 `parent_id` 指着它）。LIKE 通配符要显式 `ESCAPE`：LIKE **没有**默认转义字符，`\_` 会被当成「反斜杠 + 任意字符」，而库路径里带下划线是常态（`Album_One`） |

「空目录」的判据：**只剩专辑级元数据**的目录也算残留（`album.nfo` / `*.cue` / `cover*.*` / `folder.*` / `<base>.lrc`，判定复用 `tag.IsAlbumScopedSidecar`，和整理时「跟着搬」用的是同一份规则）。元数据**先进回收站**再问内核，目录本身仍然是 `os.Remove`、仍然是内核说了算，所以「残留 `.lrc` / 音频 / 符号链接 / 未知文件」的目录一律保留。预览接口返回的 `sidecars` 列表必须和目录一起列进确认框：只列目录等于让用户在没看到文件的情况下同意删文件。<br>
`.lrc` 算进 sidecar，是因为删除路径会把同名 `.lrc` 搬进**同一批次**（`DeleteFiles`，一次放回两个文件一起回来，报告里多一个 `lyrics` 列表）；整理目录 / 改名的三条路径也都无条件搬走 `<base>.lrc`（`tag.MoveSidecars` 的第一句）。pruner 这边不需要「归属检查」：目录里只要还有音频，它就不是候选（音频不是 sidecar），所以活着的歌词在结构上就碰不到。
| 清理前预览 + 确认 | ✅ | `POST /api/prune_empty_folders/preview/`（`handler.PreviewPruneEmptyFolders`）+ 「清理残留」确认框。确认框列出**具体路径**而不是数量：空目录取决于上一次 tidy 留下了什么，残留行取决于哪些文件从外部消失了，用户都预测不到。预览走的是任务处理函数自己的 dry-run（`pruneEmptyMode` / `pruneVanishedMode`），不是另写一份 —— 两份实现一旦分家，确认框就成了谎话。dry run 必须自己模拟向上的级联（`directoryWouldEmpty`）：真实清理靠 `os.Remove` 失败来判定非空，dry run 不能真删，就只能按“子目录都已被清掉”递归判断，否则预览会比实际少报一层。预览只读，所以不进 worker 队列，gateway 直接答 |
| 曲库查重页（只读） | ✅ | `POST /api/check_duplicate/`（`internal/gateway/handler/duplicate.go`）。智能刮削页选中行 → 「查重」→ 行上 `重复`/`疑似`/`唯一` 徽章 + 侧栏「只看重复」筛选。与写入路径共用 `dedupCheckFor`，两边判定不会分歧。逐行返回（单个文件不可查不拖垮整批），路径经 `SafeJoin` 限制在 MUSIC_DIR 内 |
| 删除重复文件 | ✅ | `POST /api/delete_files/`（同上文件）。仅接受内容级 `duplicate` 判定；两侧互相指认时两边都不删。实现为**移入 `DATA_DIR/.trash/<ts>/`** 而非 unlink（保留相对路径，可恢复；trash 在 MUSIC_DIR 之外所以扫描器与 `http.Dir(MUSIC_DIR)` 都不会再服务它）。拒绝目录 / 符号链接 / 越界路径；跨设备回退到 copy+remove（默认 compose 就是两个独立 bind mount）；删除后同步清掉 `music_folder` 索引行（否则陈旧的 `duration` 会让已删文件永久留在候选集里），并记一条 `delete_files` 审计 |
| 搜索源动态列表（`GET /api/sources/`） | ✅ | `internal/gateway/handler/source.go` 中 `ListSources` + `frontend/src/store/useSourceStore.ts` |
| 用户源启用 / 关闭（`localStorage` 持久化） | ✅ | `frontend/src/store/useSourceStore.ts` `persist` -> `localStorage["app.enabledSources"]` |
| 来源偏好设置页 | ✅ | `frontend/src/components/settings/SettingsView.tsx` + `settings/SourcesTabContent.tsx` |
| per-source config override (C.4 Stage B) | ✅ | `internal/config/loader.go` + `internal/plugin/{kuwo,kg,migu,qmusic}/server.go` (const→var + `SetSecret`/`SetAPIBase`) + `frontend/src/components/settings/SourcesTabContent.tsx`. 详见 `docs/plans/Unfinished-Features.md § C.4` |
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
| 多维度排序（文件名 / 大小 / 更新时间） | 🚧 | `frontend/src/store/useBrowserStore.ts` 定义了 `sortField: 'name'\|'size'\|'update_time'` + `setSort` / `setSortDir` + 旧 key 迁移，但**没有任何组件订阅它**（该 store 目前只被 `HomePage` / `DirPickerDrawer` 用来读 `filePath`）。排序状态在，排序 UI 不在 |
| 文件按 艺术家 / 专辑 分组 | ✅ | `frontend/src/store/useWorklistStore.ts` (grouping + `worklist.grouping.v1`) + `workstation/WorkstationTable.tsx` (分组渲染) + `workstation/WorkstationToolbar.tsx` (chip row). 详见 `docs/plans/Unfinished-Features.md § C.3` |
| 从标签改名（8 个 tag → 文件名；写入前列出每个文件的旧名/新名） | ✅ | `internal/gateway/handler/rename_from_tags.go` (`ApplyRenameFromTags`，规划与改名同一趟) + `internal/utils/rename_template.go` (严格渲染：未知字段报错而非写出 `${genre}` 字面量) + `workstation/RenameFromTagsDialog.tsx` + `workstation/renameFromTags.ts` + `workstation/localPreview.ts`. 方案在浏览器里按行的缓存标签实时算出（只列前 10 条，提交覆盖全部选中行）；**目标名是否已被占用不在本地查**，由服务端那一趟的 `taken` 回答。批次内重名取先到者（`os.Rename` 会覆盖，逐文件 stat 看不见这种冲突）；缺字段留空位并在方案里标出。上一行「解析文件名」的逆运算 |
| 文件名解析预填（`Artist - Title.flac` → 8 个 tag；点选字段自动生成规则，附 6 个常见命名预设） | ✅ | `internal/utils/filenames.go` (regex source-of-truth) + `internal/cache/parsed_result.go` (写入任务携带的行结构) + `internal/tasks/parsedfilenames.go` (asynq worker) + `frontend/src/utils/parseFilename.testdata.json` (共享 fixture) + `scraper/ParseFilenamesModal.tsx` + `scraper/parseAssist.ts` (规则生成器/本地解析镜像/RE2 限制/方案计数). 方案在浏览器里算（`tryPattern` 镜像 `PortParseFilename`，测试跑同一份 fixture），写入时 `POST /api/tag/apply_parsed_filenames/` 带 paths + 规则，服务端重新解析后入队——没有预览路由，也没有 token。弹窗只回答「规则对不对」，逐字段手填与清空都在「批量编辑标签」. 只填不删：清除是「批量编辑标签」的 `null` 契约。服务端 `Failure()` 是 HTTP 200 + 信封 `code:"400"`，非 HTTP 400 |

## 6. 文本清洗 / 编码

| Claim | Status | Where |
|---|---|---|
| 批量文本替换（tag cleanup） | ❌ | 仓库内**没有**这个 UI：非 shadcn 模态框只有 `detail/TrackDetailDialog`、`scraper/ParseFilenamesModal`、`search/SourcePickerModal` 三个，没有一个做 tag 文本查找替换；后端也没有 bulk text-replace endpoint（只有 `POST /api/batch_update_id3/`，它按字段整体覆盖，不做文本变换）。`grep -rn 替换 frontend/src` 为 0 命中 |
| 常见乱码 / 多余字符清洗 | 🚧 | 前端有几个 trim helper；后端没有统一的清理 pass |

## 7. 下载 / youtube-dl

| Claim | Status | Where |
|---|---|---|
| yt-dlp 下载（YouTube） | ✅ | worker 的 `download:generic` 委托 youtube 插件执行（`internal/plugin/youtube/server.go`），worker 本体纯 Go |
| yt-dlp 参数 sanitize（纵深防御） | ✅ | `internal/ytdlp`（`SanitizeYTDLPFormat` / `SanitizeYTDLPOutputFormat` / `SanitizeYTDLPQuality`）三层防线：gateway handler 预校验 → worker
| 下载缓存有上限、也能手动清 | ✅ | `internal/audiocache`（`Inspect` / `Select` / `Remove`）+ worker 的 `cache:prune_audio` + `GET /api/audio_cache/` 与 `POST /api/audio_cache/clear/` + 设置页「通用设置 → 下载缓存」。`AUDIO_CACHE_DIR` 是 `audio-cache` 这个 named volume 里的常驻数据，**从前只进不出**：每次试听或「加库」都往里落一份（加库是 copy 不是 move），而 `docker compose down` 不删它，所以唯一能回收的方式是手动 `docker volume rm`。现在两条路都在：worker 每 30 分钟把超出 `AUDIO_CACHE_MAX_MB`（默认 2048）的部分**从 mtime 最旧的开始删**，超过阈值刚好够即停（多删一个就从「上限」变成「悬崖」）；`AUDIO_CACHE_MIN_AGE_MIN`（默认 30）保护这段时间内写过的文件——`/api/stream` 是直接 `ServeFile` 这个目录的，正在播的那首和刚下载、正准备复制进曲库的那次下载都在窗口里，**宁可让缓存暂时超上限也不删它们**（这种情况会打日志说明「超上限但按保留窗口全部留下」，不是静默放过）。手动清理复用同一个 `Select(0, minAge)`，「全部删除」是弹窗里另一个按钮（会打断正在播放的音频）。三条刻意的边界：`Remove` 对每个路径重做「是否在缓存根下、是否恰好是 `<root>/<source>/<file>`」的检查（它是 HTTP 可达的删除入口，信调用方的路径算术就等于离 `rm -rf` 只差一次重构），符号链接和目录一律拒绝而不是跟随；`Select` 只删 `mtime` 相同则按路径排序的稳定序列，否则「它保留了哪些」无法解释；只有手动清理写操作审计（`audio_cache_clear`），30 分钟一次的自动清理写日志就够了，写审计会把这个表淹掉 | 重放校验 → youtube 插件拼 argv 前再校验 |

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
| 全响应式手机 UI（Tailwind sm:/md: 触发） | ✅ | `frontend/src/components/layout/AppShell.tsx`、`workstation/WorkstationView.tsx` 等使用了 Tailwind responsive utilities |

---

## Deferred / Aspirational 汇总

> 这些 ❌ 项在 README 中已经宣称，但实际在 `frontend/src/**` + `internal/**` 跨仓 grep 返回 0 实现。
>
> **Status: aspirational-only. NOT in current roadmap。** PR 欢迎，但没有承诺任何特定 ❌ 在某版本内落地。git 历史中可见早期上下文（P2.0 移除 Django-era 表面）。与 [`plugable-plugins.md`](plugable-plugins.md) Stage B/C/D 的 staging 计划是 *相邻但独立* 的两套——后者属于插件粒度的扩展，与"自托管工具现状"不重合。选集时按所问问题对应文档，不要混为一谈。

---

## 如何更新本文档

当某个尚未实现的项被实现时：

1. 把该 row 的 Status 标为 ✅，并在 Where 列补一个具体的文件 / section 引用（**不要**用行号——目录锚定经得起 commit 间的微小漂移）。
2. 同步更新 `README.md` 「核心功能」速览表（每个 row 配一个 emoji 标，❌/🚧 项可加一句理由）。README 只留一句话摘要，**理由与细节写在这里**，不要搬回 README —— 那会让它重新长回几百行。
3. 在 status 表里加一行（如果关闭了一个被跟踪的 gap），或在 § H aspirational 段扩写（如果仍然挂着）。

当有意弱化或删除一个功能时：

1. 把对应 row 移到"Removed"区（删除 row，加一行 `Removed YYYY-MM`）。
2. 把 README 速览表中的对应 row 删除。
3. 在 CHANGELOG 风格说明里引用这次 deprecation。
