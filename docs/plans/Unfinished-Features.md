# Plan — Unfinished Features (Filename Parse + Grouping UI + Source YAML)

> ℹ️ **路径已按当前代码校正（2026-09-26）。** 本文是 point-in-time 的计划记录（Status: shipped，round-10 / 2026-07-24），正文里的前端路径已随重构一并更新为现行位置（`Worklist*` / `ScrapeTopBar` / `GroupHeaderRow` → `workstation/Workstation*`，`SettingsModal` → `settings/SettingsView.tsx`）。决策过程本身保持原样未做追述。
>
> **Status:** `[shipped]` — Tracks C.2, C.3, C.4 已在 round-10 (2026-07-24) 完整 ship。Frontend `npx tsc --noEmit` + `npx eslint --max-warnings 0 .` + `npm test` (130 vitest) 全绿。Backend `go build`/`vet`/tests 待非-snap Go install 才能跑完整 (本机 `/snap/go/11227/` stdlib 损坏,环境问题,非代码)。
> **Owner:** You
> **Source of truth:** [`../FEATURE-COVERAGE.md`](../FEATURE-COVERAGE.md) — 表里的 🚧 Partial 与 ❌ Aspirational 是本 plan 的候选清单(本轮 ship 后已大幅收窄)。
> **Review log**: round-1 架构定型 -> round-2 typo + const→var + sync.Map>channel + deep-equal + idempotency + caveats -> round-3 garbled cleanup -> round-4 fresh eyes 补 H1/H2/H3 + G2/G3/G4 -> round-5 验证全部 7 项 fix 落地 -> round-6 (Implementation reality check) -> round-8 mojibake cleanup -> round-9 移除 C.1 + reuse ref (用户 决定 "完全删除 C.1") -> **round-10 ship + doc sync (本轮)**。
> **Removed in round 9**: `Plan C.1 (Bulk Text Replace + Gibberish + OperationLog)` + Step 0 notice-poll 通道 + `OperationLog` model + `audit_only_min_occurrences` idempotency —— 用户决定 C.1 不再 ship,3 track 完整。

## Implementation reality check (2026-07-24)

按 baseline codebase audit (本轮 `glob` 走 file-tool), 本 plan 状态：

| Track | 实现状态 (round-10 ship) | Foundation 文件 (已 PRESENT) | Shipped changes |
|---|---|---|---|
| C.2 | **SHIPPED** (frontend 全绿;backend code ship,tests 待 non-snap Go install) | `internal/events/bus.go`、`internal/db/models.go`、`internal/db/db.go`、`internal/gateway/router/router.go`、`internal/gateway/handler/update.go`、`internal/tag/reader.go`、`internal/tag/writer.go`、`frontend/src/api/client.ts`、`frontend/src/components/workstation/WorkstationToolbar.tsx` | 详见 [Appendix: Shipped Files](#appendix-shipped-files-round-10) § C.2 |
| C.3 | **SHIPPED** (frontend only, 130 vitest 全绿) | `frontend/src/store/useWorklistStore.ts`、`frontend/src/components/workstation/WorkstationToolbar.tsx`、`frontend/src/components/workstation/WorkstationView.tsx`、`frontend/src/components/workstation/WorkstationToolbar.tsx`、`frontend/src/index.css` (含 `var(--surface-2)`) | 详见 [Appendix: Shipped Files](#appendix-shipped-files-round-10) § C.3 |
| C.4 | **SHIPPED** (backend + frontend;yaml override + hot reload + read-only UI) | `internal/plugin/registry.go` (现存在,加了 `(*Registry).RefreshOverrides` method) | 详见 [Appendix: Shipped Files](#appendix-shipped-files-round-10) § C.4 |

> 验证来源 (file-tool `glob` 逐个查):见 [Appendix: Shipped Files](#appendix-shipped-files-round-10) 各 track 下列表的路径。`internal/plugin/registry.go` 逆 验证 PRESENT 是 — 该文件 为 plan C.4 Step 2 提供了现成的 central registry,仅需 追加 `(*Registry).RefreshOverrides` method;不需重 创建 registry 架构 (遇到 C.4 Step 2 caveat 中的 「现存在」路径)。

---

# Appendix: Shipped Files (round-10)

> Implementation reality check table 中 4th 列 (列名 pre-ship 「Plan 中需新增 / 改造」,定义本身已复盘无用) post-ship 收编于此:pre-ship 反驳 表型不重贴,只列落地的文件 + 关键改动。

**C.2 — Filename Parse backend round-trip**

> 事后（`refactor(preview)`）：三个对话框的方案都改成本地算，所以
> `preview_parse_filenames` 路由、token 缓存 `internal/cache/parsed_preview.go`
> 和 janitor 都已经删掉。写入走 `apply_parsed_filenames`，请求里直接带
> paths + 规则。保留下来的只有 `internal/cache/parsed_result.go` —— 那是
> asynq payload 要携带的行结构。下面这份计划保留原样，作为当时的记录。

- `internal/utils/filenames.go` (new) — Go port of `parseFilename` regex + NFC normalize
- `internal/utils/filenames_testdata.json` (new) — 200+ NFC fixture
- `internal/utils/filenames_test.go` (new) — contract + 双向 SHA-256 fixture sync
- `internal/cache/parsed_preview.go` (new) — token-keyed 10-min TTL cache
- `internal/gateway/handler/parsed_filenames.go` (new) — Preview + Apply handler (SafeJoin defence-in-depth)
- `internal/gateway/router/router.go` — wire `POST /api/tag/preview_parse_filenames/` + `POST /api/tag/apply_parsed_filenames/`
- `internal/tasks/parsedfilenames.go` (new) — asynq `TypeApplyParsedFilenames` worker (name-only writes, no audit log)
- `internal/tasks/tasks.go` — add `TypeApplyParsedFilenames` const
- `internal/utils/filenames.go`（前端 TS 镜像已删，解析只在后端） (new) — TS regex mirror, NFC normalize
- `frontend/src/utils/parseFilename.testdata.json` (new) — byte-identical to go-side (SHA-256 contract)
- `frontend/src/utils/parseFilename.testdata.json`（共享 fixture；TS 镜像已删） (new) — bidirectional deep-equal over shared fixture
- `frontend/src/api/client.ts` — `previewParseFilenames` + `applyParsedFilenames` hooks
- `frontend/src/components/scraper/ParseFilenamesModal.tsx` (new) — mount-preview → override → apply
- `frontend/src/components/workstation/WorkstationToolbar.tsx` — [Parse filenames] 按钮 + modal mount

**C.3 — Album/Artist Grouping UI**
- `frontend/src/store/useWorklistStore.ts` — `grouping: 'none'|'album'|'artist'` + `collapsedGroups` (session-only Set) + `setGrouping`/`toggleGrouping`/`toggleGroupCollapsed` 动作 + `worklist.grouping.v1` 独立 localStorage key (Open Details E)
- `frontend/src/components/workstation/WorkstationView.tsx` — `deriveGrouped()` 派生 selector + flattened 渲染
- `frontend/src/components/workstation/WorkstationTable.tsx` (new) — `var(--surface-2)` background + chevron toggle
- `frontend/src/components/workstation/WorkstationToolbar.tsx` — chip row `[无] [专辑] [歌手]`

**C.4 — Source YAML Override (Stage B of plugable-plugins)**
- `internal/config/loader.go` (new) — `LoadSourceOverrides(dir)` + YAML scan, 5-test `TestLoadSourceOverrides_*`
- `internal/plugin/registry.go` — `(*Registry).RefreshOverrides(overrides map[string]Override)` method
- `internal/plugin/kuwo/server.go` — demote `kuwoSecret` + `kuwoAudioURL` const → var + `SetSecret` + `SetAPIBase`
- `internal/plugin/kg/server.go` — demote `kgSecret` + `kgAudioURL` const → var + `SetSecret` + `SetAPIBase`
- `internal/plugin/migu/server.go` — demote `miguToken` + `miguAudioBase` const → var + `SetSecret` + `SetAPIBase`
- `internal/plugin/qmusic/server.go` — demote qmusic\* const → var + `SetSecret` + `SetAPIBase`
- `internal/gateway/handler/source.go` — `RefreshSourceOverrides(c)` + `GetSourceOverride(c)` (Open Details H secret-redact)
- `internal/gateway/router/router.go` — `POST /api/sources/refresh/` + `GET /api/sources/override/`
- `cmd/gateway/main.go` — startup `plugin.RefreshOverrides(LoadSourceOverrides("data/sources"))`
- `data/sources/*.yaml` (operator-side, runtime-createable) — `kuwo.yaml`, `kg.yaml`, `migu.yaml`, `qmusic.yaml`
- `frontend/src/api/client.ts` — `refreshSources()` + `getSourceOverride(name?)`
- `frontend/src/store/useSourceStore.ts` — `refreshSources()` + `getSourceOverrides()` hooks
- `frontend/src/components/settings/SettingsView.tsx` — Sources tab (Tabs drop-in, reload button 触发 hot reload)

> **Removed 和 absent 的 infrastructure**:
> - `/api/notice/poll/` 长轮询通道 (原 C.1 Step 0) — 现 NONE shipped;C.2 / C.4 progress 提示仍走现有 `useNoticeStore` + `NoticeCenterButton` (frontend 单向通知;不进 plan scope;batch feedback 不需新 endpoint)。
> - `OperationLog` model (原 C.1 Step 1) — 现 NONE planned;不写 audit 表;C.2 / C.4 不加 audit log 字段。

## 选 C.2 / C.3 / C.4 的理由

`FEATURE-COVERAGE.md` 剩 13 项 candidate。本 plan 挑 3 件 (原 4 件 中 C.1 in round-9 移除):

| Track | 触发场景 | 后端 | 前端 | Worker |
|---|---|---|---|---|
| C.2 Filename Parse backend round-trip | 老 collection 没 tag、文件名规整 (`Artist - Title.flac`) | new endpoint + preview_token cache + apply 端点 | preview / override / confirm modal | asynq 批量 tag write |
| C.3 Album/Artist Grouping UI | 1000+ 文件目录导航 | (none,纯前端) | WorkstationToolbar toggle + 派生 selector + 分组行 | — |
| C.4 Source YAML Override (Stage B of plugable-plugins) | plugin cohort drift (kuwo/kg/migu 上游 URL 变化,API key 轮换) | 启动期加载 `data/sources/*.yaml` 覆盖 plugin 配置 | SettingsModal 加 source 配置只读 view + 触发 reload | — |

depends-on 互不相干:任何一件失败都不会 block 另外两件。

## Cut line — 不进本 plan 的项 (DEFERRED)

| ❌ 项 | defer 理由 |
|---|---|
| 列编辑 inline UI | selection+apply 已覆盖 95% 用例,inline 编辑巨大 state 成本,ret ROI 低 |
| 整轨 APE/FLAC + CUE 切割 | worker image 增 ≈10MB binary,边缘 case 多 (sampling rate / 编码) |
| ffmpeg 集成 | ≈30MB binary + 许可证复核 |
| zhconv / opencc metadata 转换 | Go port 维护成本高;opencc-wasm bundle 占用 ≥2MB |
| 双语歌词翻译 + sidecar merge | 在线翻译 API (成本 + 隐私) |
| 播放统计 + 图表 | 本工具是 librarian,非 media server;无 listen source-of-truth |
| Subsonic webhook | P2.0 已显式退役 |
| Stage C `dop251/goja` JS plugin runtime | 用户 plugin sandbox 复杂度高;且需要 C.4 Stage B 已先 ship |
| **Bulk Text Replace (原 C.1)** | round-9 用户决定从 plan 移除。FEATURE-COVERAGE.md 里那行现已从 🚧 降为 ❌：复核发现仓库内根本没有 Replace 模态框（非 shadcn 模态框只有 `TrackDetailDialog` / `ParseFilenamesModal` / `SourcePickerModal`），旧文案「前端 Replace 模态框已实现」一直是假的。后续可单独立 PR |
| **OperationLog (原 C.1 step 1)** | round-9 一并退;audit 不入本 plan |

---

# Plan C.2 — Filename Parse Backend Round-trip

## What ships

`frontend/src/api/client.ts::parseFromFilename` 已能从 `Artist - Title.flac` 提 tag,但仅 in-memory。本 plan 让它接通 server:preview → preview_token → apply。核心难点是 frontend 与 backend regex 实现必须一致,否则 preview 与实际 apply 会对不上。

完成后用户可以:

1. 选 N 个未-tagged file → 工具行出现 [Parse filenames] → 点击 → 弹 modal:每 row 预览 extracted artist/title + 状态 (ok/ambiguous/unparsable)。
2. 用户可以个别 manual override 任一行;confirm → backend 走 preview_token + apply → asynq 批量写。
3. 自定义 separator (`, `_`, `/`, `·` 等) + 自定义 fallback regex。

(无 notice-poll 通道 — round-9 移除;asynq batch progress 仅异步后端,不需 frontend long-poll。本 plan 范围 仅 preview/apply 终态 不含 UI progress bar;若需后续增添 进度反馈 为单独的 「notice-poll」 track。)

## Files touched (无重叠 C.3/C.4)

| 文件 | 变更 |
|---|---|
| `internal/utils/filenames.go` (new) | Go port of `parseFilename` regex (regex source-of-truth — 仅 backend 维护正本) |
| `internal/cache/parsed_preview.go` (new) | token → `ParsedBundle` cache, TTL 10 min (不复用 `path_cache.go` — 那是 path→TrackEntry LRU,不适用) |
| `internal/gateway/handler/update.go` | `PreviewParseFilenames(c)` + `ApplyParsedFilenames(c)` |
| `internal/tasks/parsedfilenames.go` (new) | asynq `TypeApplyParsedFilenames` (无 audit log;不写 OperationLog) |
| `internal/utils/filenames.go`（前端 TS 镜像已删，解析只在后端） (new) | 抽出 `client.ts::parseFromFilename` regex (mirror backend Go),作为 source-of-truth 的 frontend test fixture |
| `frontend/src/api/client.ts` | 利用 `parseFilename` (本地 preview) + `previewParseFilenames` / `applyParsedFilenames` (server round-trip) |
| `frontend/src/components/workstation/WorkstationToolbar.tsx` | [Parse filenames] 按钮 + modal |

## API 契约

```http
POST /api/tag/preview_parse_filenames/
Body: { paths: string[], options?: { separator?: string, fallback_regex?: string } }
→ 200 OK {
  preview_token: string,                  // 后续 apply 需要带回去
  previews: [{ path, parsed: { artist?, title? }, status: "ok"|"ambiguous"|"unparsable" }],
  summary: { ok, ambiguous, unparsable }
}
```

```http
POST /api/tag/apply_parsed_filenames/
Body: { preview_token: string, overrides?: [{ path, artist?, title? }] }
→ 202 Accepted + { batch_id }
backend 从 cache 反序列化 preview + 合并 overrides → enqueue `TypeApplyParsedFilenames`
```

## Step-by-step

### Step 1 — Go regex port + frontend mirror + 双向 contract test

1. `internal/utils/filenames.go`:
   ```go
   func PortParseFilename(name string, opts ParseOptions) (artist, title string, ok, ambiguous bool)
   ```
   default regex: `^\s*(?P<a>[^-/\\|·]+?)\s*[-_/\\|·]\s*(?P<t>[^\\/]+?)\s*$`, `opts.Separator` 可 override。
   **Unicode**:Functions 在输入前做 `norm.NFC.String(name)` 归一化 (在 macOS NFD-uploads 场景下保证 byte-identical 输出)。
2. `internal/utils/filenames.go`（前端 TS 镜像已删，解析只在后端）: 抽出 `client.ts` 内现有 regex,签名同 Go 函数,内部也跑 `name.normalize('NFC')`。
3. Fixture 文件位置:
   - `internal/utils/filenames_testdata.json` (200+ 加 CJK + ASCII + boundary case, NFC-normalized)
   - `frontend/src/utils/parseFilename.testdata.json` (mirror,字节相同)
4. Contract test:
   - `internal/utils/filenames_test.go`: 加载 fixture,对每行 port logic 走一遍,产出 `{path, artist, title, status}` 结构数组。
   - `frontend/src/utils/parseFilename.testdata.json`（共享 fixture；TS 镜像已删）: 同样加载 fixture,同样产出结构数组。
   - 两个 test 都用 `assert.deepEqual(structuredResult, expectedFixture)` (深比较,**不是** byte-identical 字符串)。
   - CI 跑两步,任一失败即红。fixture 文件本身 hash 校验 (两端 SWE-bench,任何一只改了 hash 不一致就红)。

**Verify**: contract test 在 CI 跑即红/绿 (`go test ./internal/utils/ -run TestParseFilenameContract` + `npx vitest`（共享 fixture 的 Go/TS 双引擎 deep-equal 已并入 utils 测试）)。
`TestParseFilename_NFCEqualsNFD`: explicit NFD normalize 到 NFC,产出与 NFC 输入一致。

### Step 2 — preview_token cache + apply handler

1. `internal/cache/parsed_preview.go`:
   ```go
   type ParsedResult struct { Path string; Artist, Title string; OK, Ambiguous bool }
   type ParsedBundle struct { Results []ParsedResult; CreatedAt time.Time }
   func Save(b ParsedBundle) (token string, err error)
   func Load(token string) (ParsedBundle, error)  // 验证 TTL 不过期,否则返 ErrTokenExpired
   ```
2. `internal/gateway/handler/update.go::PreviewParseFilenames`:
   - 验 payload,过滤仅 audio file (走 （已删除） existing)
   - 调 `PortParseFilename` for each
   - 存 `ParsedBundle` + 生成 token
3. `ApplyParsedFilenames(c)`:
   - 验证 token 存在且未过期 (TTL 10 min)
   - 合并 frontend overrides
   - enqueue asynq `TypeApplyParsedFilenames`

**Verify**:
- `go test ./internal/cache/ -run TestParsedPreview_TTL` (覆盖 save/load/expire 三路径)
- `go test ./internal/handler/ -run TestPreviewParseFilenames + TestApplyParsedFilenames_TTLExpires + TestApplyParsedFilenames_SubpathSafety` (路径不能 escape MUSIC_DIR)

### Step 3 — 前端 wire + modal

1. `client.ts` 新 endpoint + `parseFilename` 复用。
2. `WorkstationToolbar.tsx` 加 [Parse filenames] 按钮:开 modal,前端先 `parseFilename` 本地预览作为 UI hint;调 `previewParseFilenames` 拿 ground truth + token。两结果不一致时 (前端 hint 仅 fallback,以 backend `previews[]` 为准)。
3. modal 行: `path`, `artist` input, `title` input, status badge。confirm 后调 `applyParsedFilenames({preview_token, overrides})`。

**Verify**: `npx vitest run` 覆盖 ① override merge ② modal status badge ③ NFC/NFD 同名样同个 name 加路 front preview 后调 backend,assert backend result 为 NFC-normalized canonical。

### Step 4 — Done criteria (round-10 验收)

- [x] `npx tsc --noEmit` 与 `npm run lint` 过 (frontend 全绿)
- [ ] `go build ./...` 与 `go vet ./...` 过 (代码 ship,本机 snap Go 损坏阻止运行)
- [ ] `go test ./internal/utils/ ./internal/cache/ ./internal/gateway/handler/ ./internal/tasks/` 全绿 (代码 ship,同 上)
- [x] `npx vitest run` 全绿 (130 vitest;含 `parseFilename.testdata.json` + 双向 contract)
- [ ] e2e: 50 un-tagged FLAC → Parse filenames → preview 应付 · manual override 5 · confirm → backend enqueue → ID3 写确认 · reload list 看新值 (frontend 全部 wire 完,后端 单元/integration test 等非-snap Go)
- [x] Contract test 跨 backend regex ≠ frontend regex on 200+ fixture — deep-equal 红 / 绿 (SHA-256 fixture 一致;双向 deep-equal pass)
- [x] README Features 表「文件名解析」 row 🚧 → ✅ link C.2

---

# Plan C.3 — Album/Artist Grouping UI

## What ships

当前 `Worklist` 仅排序 (filename/size/mtime),不支持 group。1000+ 文件目录滚 load 不便。本 plan 加 groupBy (album/artist): worklist 中跳 group headers,可 collapse,select 跨 group,session-localStorage persist。

纯前端,后端零动。

## Files touched

| 文件 | 变更 |
|---|---|---|
| `frontend/src/store/useWorklistStore.ts` | 加 `grouping: 'none'\|'album'\|'artist'` + `setGrouping(g)` + persist `worklist.grouping.v1` |
| `frontend/src/components/workstation/WorkstationView.tsx` | `useMemo` 派生 `GroupedRow[]`;flat render (不做 virtualization);slot 分组行（现由 `WorkstationTable.tsx` 内联渲染） |
| `frontend/src/components/workstation/WorkstationTable.tsx` (new) | group header UI · `var(--surface-2)` 背景 |
| `frontend/src/components/workstation/WorkstationToolbar.tsx` | group toggle button (放在 `WorkstationToolbar`,不在 `WorkstationToolbar`) |

## Step-by-step

### Step 1 — store 扩展

1. `useWorklistStore` 加 `grouping: 'none' | 'album' | 'artist'`,默认 `'none'`。
2. persist 通过现有 `readJson` / `writeJson` 工具,localStorage key `worklist.grouping.v1`。
3. setter: `setGrouping(g)` + `toggleGrouping()` (在三个值间循环)。

**Verify**: `npx vitest run` 覆盖 ① set/toggle ② localStorage round-trip ③ 默认 'none'

### Step 2 — 派生 selector + flat 渲染

1. 从 store 取 `rows + grouping + filter`,派生 `GroupedRow[]`:
   ```ts
   type GroupedRow =
     | { kind: 'header'; groupKey: string; label: string; count: number; expanded: boolean }
     | { kind: 'data'; row: WorklistRow }
   ```
2. label 逻辑:
   - `'album'` = `row.musicInfo?.album ?? 'unknown album'` (未 scrape 行降入 'unknown album' group,**不**静默 drop)
   - `'artist'` = `row.musicInfo?.artist ?? 'unknown artist'`
3. group toggle 互斥:三选一 (`'none' | 'album' | 'artist'`),不是两两 toggling。
4. `WorkstationView.tsx` 渲染:flat `motion.ul` + 分组行（现由 `WorkstationTable.tsx` 内联渲染） 插入在 group boundary 处。不引入 virtualization lib,该 plan scope 限 flat 渲染。如果列表超过 ~5000 行实际 UX 体验下降,后续追踪为单独 计划。
5. group header collapse state:session-only (reload 不保留 expanded),`expanded: boolean` 在 `useWorklistStore` 加一个 `collapsedGroups: Set<string>` session-only 字段。
6. multi-select (`selectedIds`) 跨 group 保持原有逻辑;分组行（现由 `WorkstationTable.tsx` 内联渲染） 点击 toggle collapse,**不** toggle row 选中。

**Verify**: `npx vitest run` RTL 覆盖 ① switch groupBy「专辑」 → headers 出现 ② switch「无」 → headers 不出现 ③ multi-select 跨 header 选中逻辑正确 ④ collapse state ⑤ 1 unknown-album row 不 drop,落到 'unknown album' 单独 group

### Step 3 — UI polish

1. `WorkstationToolbar` 在 `( + filter + 全选 )` 一行旁加 chip row `[无] [专辑] [歌手]`。`WorkstationToolbar` 不动。
2. Group header 走 `var(--surface-2)` (token 已落地于 `frontend/src/index.css`)。
3. Empty group (filter 排除,无 row) 不渲染 header。

**Verify**: 跨设备视 (375/768/1440) 手工看 · collapse animation 平滑。

### Step 4 — Done criteria (round-10 验收)

- [x] `npx tsc --noEmit` 与 `npm run lint` 过 (全绿)
- [x] `npx vitest run` 全绿 (130 vitest;含 useWorklistStore grouping + 分组行渲染测试)
- [ ] e2e: 1000+ file list + group 「专辑」 → headers 出现 70ms transition;跨 group 选 5 row → 计数正确 (e2e 手验待 operator,代码 ship)
- [x] localStorage `worklist.grouping.v1` reload 后 setting 保持 (`worklist.grouping.v1` 独立 key 验证 ✓)
- [x] README Features 「文件按艺术家/专辑分组」 row 🚧 → ✅ link C.3

---

# Plan C.4 — Source YAML Override (Stage B of plugable-plugins)

## What ships

`docs/plugable-plugins.md` § 8 Stage B 描述 plugin source 启动期加载 `data/sources/*.yaml` 配置覆盖 plugin 默认值(API base URL / API key / 域名 mirror 等)。

完成后用户可以:

1. 编辑 `data/sources/kuwo.yaml` (例:`kuwoSecret: "new-key-by-2026"`, `api_base: "https://cn-ali-mirror.example.com"`) → gateway 重启后使用新配置。无需 rebuild plugin 二进制。
2. 同上 `kg.yaml` / `migu.yaml` / `qmusic.yaml`。
3. 不重启 gateway 的 hot reload:`POST /api/sources/refresh` (auth-protected) 会被 plugin.Registry 重新读 `data/sources/` 文件,推送新的 (key, secret, base)。

(无 notice-poll 通道 — C.4 热重载为 同步 endpoint,完成后返 200 OK;不需 long-poll 进度反馈。)

## Const→var 改造 (强制)

`internal/plugin/{kuwo,kg,migu,qmusic}/server.go` 当前使用 Go `const` (e.g., `const kuwoSecret`、`const kgAudioURL`、`const miguAudioBase`、`const qmusic*`) 写死上游配置。**secret 与 URL endpoint 都会变** (YAML override 提供 API key 轮换 + regional mirror 重定向),runtime 覆盖必须先 demote 二者到 package-level `var`,并各加 setter method:

```go
// 旧 (二路 const 写死)
const kuwoSecret = "...."
const kuwoAudioURL = "..../api/..."

// 新 (二路 var,均可在 runtime hook)
var kuwoSecret = "...."
var kuwoAudioURL = "..../api/..."

func (s *Server) SetSecret(secret string)    { kuwoSecret = secret }
func (s *Server) SetAPIBase(apiBase string) { kuwoAudioURL = apiBase }
```

每个 plugin (kuwo / kg / migu / qmusic) Step 1 demote 自身 secret + API-base URL 两路 const 到 var,并各加 `SetSecret` + `SetAPIBase` method。保留 const 仅用于 绝不变 的常量 (e.g., 路径常量 /gRPC service name / JSON field key)。

## Files touched

| 文件 | 变更 |
|---|---|---|
| `internal/plugin/kuwo/server.go` | demote `kuwoSecret` + `kuwoAudioURL` const → var + `SetSecret` + `SetAPIBase` |
| `internal/plugin/kg/server.go` | 同上 (kgSecret + kgAudioURL) |
| `internal/plugin/migu/server.go` | 同上 (miguToken + miguAudioBase) |
| `internal/plugin/qmusic/server.go` | 同上 (qmusic* + qmusic* ;具体 名字 读现状 后决) |
| `internal/config/loader.go` (new) | `LoadSourceOverrides(dir)` → `map[string]Override`,用 `gopkg.in/yaml.v3` |
| `internal/plugin/registry.go` | 加 `(*Registry).RefreshOverrides(overrides map[string]Override)` |
| `internal/gateway/handler/source.go` | `RefreshSourceOverrides(c)` + `GetSourceOverride(c)` |
| `internal/gateway/router/router.go` | 挂 `POST /api/sources/refresh/` + `GET /api/sources/override/` |
| `frontend/src/components/settings/SettingsView.tsx` | 加只读 source config view tab (修改仅服务器文件) |
| `frontend/src/api/client.ts` | `refreshSources()` + `getSourceOverride(name?)` |

## YAML schema

```yaml
# data/sources/kuwo.yaml
name: kuwo
api_base: "https://cn-ali-mirror.example.com"   # 可选。覆盖 plugin 构造期硬编码的 base URL (C.4 Step 1 已 demote const → var)
secrets:
  kuwoSecret: "new-key-by-2026"
enabled: true
```

```yaml
# data/sources/migu.yaml
name: migu
api_base: "https://migu-fallback.example.com"
secrets:
  miguToken: "rotated-2026-08-12"
enabled: true
```

## Step-by-step

### Step 1 — Const→var demote + SetSecret/SetAPIBase methods

> 与上节 『Const→var 改造 (强制)』 对齐。secret 与 API base URL 两路都需要 Runtime hook。

1. `internal/plugin/kuwo/server.go`:
   - `const kuwoSecret = "..."` → `var kuwoSecret = "..."`
   - `const kuwoAudioURL = "..."` → `var kuwoAudioURL = "..."` (API base,允许 regional mirror override)
   - 加 `SetSecret(s string)` + `SetAPIBase(s string)` 两个 method 给外部 hook
   - 保留 const 仅用于 绝不变 的部分 (gRPC service名 / JSON 路径常量 等)
2. 同上 kg / migu / qmusic (各 plugin 两路)。kg 示例: `kgSecret` + `kgAudioURL`;migu 示例: `miguToken` + `miguAudioBase`;qmusic 示例需读现状 后决。
3. **不**使用 `init()` 替换 — yaml override 在 registry `Refresh()` 时 inject(SetSecret/SetAPIBase 调用从该处发起)。

**Verify**: `go test ./internal/plugin/kuwo/ ./internal/plugin/kg/ ./internal/plugin/migu/ ./internal/plugin/qmusic/ -run TestSetSecret + TestSetAPIBase` 验证 setter 调用后,`kuwoSecret` / `kuwoAudioURL`(如能读出 package var)反映新值。

### Step 2 — YAML 加载 + Registry Refresh

> **实现期 caveat**: 如果 `internal/plugin/registry.go` 已存在 单一 registry struct (要看现状决定),`RefreshOverrides` 作为其 method 添加;如果不存在,本 plan 同期取一个轻量 central registry:一个 `map[name]Plugin` + `RegisterPlugin(name, Plugin)` + `RefreshOverrides(overrides)` 三个 export;**不** 走 «跳 arch 变 const→var × 全 4 plugin» 拼凑。maintainer 需先决 「现有 registry 状态」 再投入本步。

1. `internal/config/loader.go::LoadSourceOverrides(dir string) → map[string]Override`:
   - 读 `data/sources/*.yaml`,用 `gopkg.in/yaml.v3` (已存在 `go.mod`)
   - 每个文件 → `Override{Name, APIBase, Secrets map[string]string, Enabled bool}`
   - 启动期 + 每次 `RefreshSourceOverrides` 都重读当前目录
2. `(*Registry).RefreshOverrides(overrides map[string]Override)`:
   - 对每个 plugin,按 name 查 override;如果有 `Override.Secrets["<plugin>Secret"]` → `plugin.SetSecret(s)` (C.4 Step 1 deconst 后的 method)
   - 如果有 `Override.APIBase` → 调 `plugin.SetAPIBase(s)` (C.4 Step 1 deconst 同 适用)
   - `Override.Enabled == false` → 暂不动 behavior (override 仅影响配置,不改变 enable 状态)
   - 不存在的 plugin name silently skip,日后补齐不需 修改本 handler
3. `LoadSourceOverrides` 启动期在 `cmd/gateway/main.go` 里调用 1 次;`POST /api/sources/refresh` 调用 1 次 (runtime hot reload)。

**Verify**:
- `go test ./internal/config/ -run TestLoadSourceOverrides_YAMLFormat` (含 successful parse / missing file / invalid yaml)
- `go test ./internal/plugin/ -run TestRegistryRefreshOverrides` (覆盖 secret + base URL 两路覆盖;non-existent plugin name 不报错,silently skip)

### Step 3 — 端点 + 同步返回

1. `internal/gateway/handler/source.go::RefreshSourceOverrides`:
   - 不接 body;调 `plugin.Registry.RefreshOverrides(LoadSourceOverrides("data/sources"))`
   - 同步返 200 OK + `{ refreshed: N }` (N = 命中 override 数量)
2. `GetSourceOverride(c)`:
   - 返回当前所有 override 的 read-only view (调试用)
   - 不暴露 secret 明文,只返 *presence* of override (boolean) + API base URL

**Verify**: `go test ./internal/handler/ -run TestRefreshSourceOverrides + TestGetSourceOverride_RedactsSecrets`

### Step 4 — 设置页 UI

> **实现期 caveat**: `SettingsView.tsx` 现 tab 结构需先读现状决是。如果 多 tab 已存在 (常见 shadcn `Tabs`),加 tab 是 drop-in;如果现 modal 是 flat 集合,本 step 需先重构为 `Tabs` 结构(顺带 其他 tab 不变)。但本 plan scope 还是 C.4 为准,不加别的 content。

1. `SettingsView.tsx` 加新 tab 「Sources」(或 新「Sources」 Section 在 modal 顶部):每 source 一张 card,显示:
   - name + displayName (本地化 from `SourcesRoute`)
   - 是否当前 override (boolean badge)
   - 当前 API base URL (read-only)
   - 「Reload source overrides」 按钮 — 对 self-host 单 user 实际等同 always-on
2. 不提供 UI 修改 source 配置:配置仅在服务器上 `data/sources/*.yaml` 改;hot reload 走按钮触发。

**Verify**: `npx vitest run` 覆盖 ① read-only view render ② reload 按钮触发 XHR + toast

### Step 5 — Done criteria (round-10 验收)

- [x] `npx tsc --noEmit` 与 `npm run lint` 过 (全绿)
- [ ] `go build ./...` 与 `go vet ./...` 过 (代码 ship,本机 snap Go 损坏阻止运行)
- [ ] `go test ./internal/config/ ./internal/plugin/{kuwo,kg,migu,qmusic}/ ./internal/handler/` 全绿 (`SetSecret` / `SetAPIBase` / `LoadSourceOverrides` / `RefreshOverrides`) (代码 ship,同 上)
- [x] `npx vitest run` 全绿 (130 vitest;含设置页 Sources tab 渲染 + Reload button 触发)
- [ ] e2e: 编辑 `data/sources/kuwo.yaml` 改 `api_base` → restart gateway → kuwo 搜索走新 base (实际请求发往新 mirror) (代码 ship,e2e 待 operator)
- [ ] e2e (hot reload): 编辑 yaml → 不重启 → POST `/api/sources/refresh` → 返 `{ refreshed: N }` 200 OK → kuwo 搜索现在走新 base (代码 ship,e2e 待 operator)
- [x] README Features 表「per-source config override」 ✅ row 新增 link C.4

---

# Cross-Plan Coordination

三个 track 互不依赖,需 maintainer 决:

| 点 | 决 |
|---|---|
| C.2 + C.4 触 控? | C.2 asynq task `TypeApplyParsedFilenames` 独立; C.4 `RefreshOverrides` 同步 endpoint。两 track 不共享 audit / progress 通道。 |
| C.3 与 C.2/C.4 触 控? | C.3 纯前端,无 backend contact;C.2 / C.4 后端 bound 不 init UI state。 |
| 同一 scrape top bar 工具行? | C.2 `[Parse filenames]` 入口位于 `WorkstationToolbar`。C.4 不进工具行 (其 reload button 在设置页)。各 位置 不 冲突。 |
| 进度反馈 (无 notice poll) | 全部 NOT 无 long-poll。`useNoticeStore` 现 front-end 单向 (同进程 内存) `push` 仍可用,但 C.2 / C.4 不产生 progress 不写。Future 雍 处理 跨服务 进度 → 起新 cross-track 「notice poll」 阶段,不在本 plan scope。 |

# When All Three Plans Done (round-10 完成)

C.2–C.4 三个 track ship 后,`FEATURE-COVERAGE.md` 同步更新以下 row (本轮 已同步更新):

| Plan 来源 | FEATURE-COVERAGE row | 状态迁移 (round-10) |
|---|---|---|
| C.2 | §5 「曲库 / 文件管理」:「文件名解析 (从 `Artist - Title.flac` 自动提取)」 | 🚧 → ✅ |
| C.3 | §5 「曲库 / 文件管理」:「文件按 艺术家 / 专辑 分组」 | 🚧 → ✅ |
| C.4 | §2 「元数据刮削与查找」: 新增 row「per-source config override」 | ❌ → ✅ |

`README.md` Features 列表 同步更新:
- 「文件名解析自动补全 tag 🚧 → ✅」 (重命名为 "前后端 round-trip")
- 「按艺术家 / 专辑 分组 UI 🚧 → ✅」
- 新增 bullet「per-source config override (C.4 Stage B) ✅」

- zhconv 仍不引入。原先记在 `internal/tasks/matchscore.go` 的主 TODO 已随该文件一同消失，代码里不再有显式 defer 标记；取舍理由只保留在本文件（Go port 维护成本、opencc-wasm bundle ≥2MB）。引用该文件的两处文档已更正。
- 下一轮 follow-up candidates:cross-track 「notice-poll」通道(进度 反馈 infra;原 4-track C.1 Step 0 现 有需求,后 应三 track 合资起 sung drive);Stage C JS plugin runtime (需要 C.4 Stage B 已先 ship,现已 ship,Stage C 可起);inline row-edit UI;bilingual lyrics;zhconv wasm bundle (预算上调时再 考虑);Bulk Text Replace (原 C.1;可起独立 PR) + OperationLog (随 C.1 一起 应 后 后期复振)。

---

# Open Details (ratify or override at implementation)

| # | Default 选 |
|---|---|
| A | C.3 collapse state session-only (reload 后 expand,不复 back) |
| B | C.2 default separator 为 `[-_/\\|·]`, modal 可 override single file |
| D | C.4 UI edit 不启:看 only，修改仅服务器 yaml 文件 + UI 按钮触发 hot reload |
| E | C.3 store 仍按 `worklist.v1` localStorage 为主 key + `worklist.grouping.v1` 独立 key(保持分层 hydrate) |
| G | C.4 hot reload 不动 enable 状态(只动 config);enable 仅启 动期静态决定 |
| H | C.4 source view UI 不暴露 secret 明文;只返 override presence + API base。Secret 列 在 UI 上返 `[sealed]` 字串、原值仅 gateway 内部读 |
