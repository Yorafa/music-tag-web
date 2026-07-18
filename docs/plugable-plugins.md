# Pluggable Plugin Architecture — 设计文档

**Status**：Design（Stage A 待实施）
**Audience**：self-hosted 家庭影音中心用例，单一可信用户
**Date**：2025-06-29

> **Cross-ref**：本文评估的是**整张 L × P 矩阵**；目前真正落地的只有 Stage A（其 plumbing——`GET /api/sources/`、`useSourceStore`、`SettingsModal`、插件注册表 fan-out——在 [`docs/FEATURE-COVERAGE.md`](docs/FEATURE-COVERAGE.md) §2 中标为 ✅）。Stage B（per-source YAML 覆写）、Stage C（通过 `dop251/goja` 跑沙箱 JS plugin）、Stage D（runtime admin UI）属于 **future-feature pitches**——不在任何当前 README claim 集合内。`FEATURE-COVERAGE.md` 里 ❌ 的集合是另一份列表（README 今日宣称但代码里没做的项），与本表的 aspirational 项**概念上相邻**但**集合上不同**：本表讲"插件粒度未来长什么样"，矩阵讲"README 今天的宣称哪些没做"。按你问的问题选对应的文档，不要把两者混在一起 filing issue。§ Deferred/aspirational 列出的是**今天** README 已宣称但没实现的项。两者都 aspirational、都脱离当前 roadmap，但**集合层面是不同的**：

---

## 1. 目标与非目标

**目标**：让"music sources"（搜索 + 预览 + 查找）和"tag sources"（元数据 + 歌词）一套可插拔插件体系，新增一个 source 时**不动前端代码**；最终的目标是让用户根本不需要装 Go toolchain 也能写插件。

**非目标**：
- 公开 plugin marketplace 或托管 registry
- 对不可信第三方代码做严格 sandboxing（用户可信、单租户）
- 取代后端——对国内主流音乐平台来说纯前端不可行

## 2. 决定本设计方向的硬事实

2025-06 的 web 调研确认：

| Source | CORS 开放？ | 浏览器侧是否有可用稳定 lib？ | 结论 |
|---|---|---|---|
| 网易云 | ❌ | ❌（WEAPI AES 混淆） | 需要后端 |
| QQ 音乐 | ❌ | ❌（g_tk 签名 cookie） | 需要后端 |
| 酷狗 | ❌ | ❌ | 需要后端 |
| 酷我 | ❌ | ❌ | 需要后端 |
| 咪咕 | ❌ | ❌ | 需要后端 |
| MusicBrainz | ✅ | ✅ | 浏览器侧可行 |
| AcoustID | ✅ | ✅ | 浏览器侧可行 |
| YouTube | — | — | 仅 iframe 播放 |

**推论**：5 / 7 的 source 从结构上就阻断了"纯前端插件"路线。后端是**不可妥协**的；设计轴就从"插件化"变成"后端把它已经有的 plugins 暴露给前端时，到底多可插拔"。

## 3. 可定制性的 2 维空间

我们沿着两条轴评估：

- **L** = customizability level（用户能改什么）
- **P** = platform shape（插件代码住在哪里 / 在哪里跑）

|             | P1 纯前端 | P2 后端 signing-proxy | P3 编译期 go plugin | P4 Go `.so` plugin                     | P5 WASM serverless          |
| ----------- | ---------------- | ------------------------ | ------------------------ | ------------------------------------- | --------------------------- |
| **L0** 仅内置 | 🚫 CORS | ✅  baseline | ✅ 当前 | ✅ | ✅ |
| **L1** per-source API key / URL 覆写 | 🚫 | ✅ trivial | ✅ | ✅ | ✅ |
| **L2** JSON URL-template extractor | 🚫 | 🚫 5 个加密 source 失败 | ✅ | ✅ | ✅ |
| **L3** 用户自写 JS plugin | 🚫 | ✅ goja sandbox | ✅ | ✅ | ✅ |
| **L4** 用户自写 Go plugin | 🚫 | ✅ | ✅ | ⚠️ 仅 Linux 且依赖链噩梦 | ✅ |
| **L5** 用户自写 WASM plugin | 🚫 | ✅ | ✅ | ✅ | ⚠️ 需要 WASM 工具链 |

🚫 = 阻塞。✅ = 可行。⚠️ = 易碎 / 工程量大。

**建议路径**：L3 × P2（在 Go 后端里跑沙箱化 JS plugin），分阶段实现：

- **Stage A**（今日）：L0 + P2——给前端暴露现有的内置 plugins，使用 capability-aware 的动态 source 列表
- **Stage B**（后续）：L1——每 source 一份 config override 文件（`data/sources/*.yaml`）
- **Stage C**（后续）：L3——用户自写 JS plugin，在 `dop251/goja` runtime 里跑

本文档对 **Stage A** 做了完整描述。B / C 在末尾勾勒。

---

## 4. Stage A 的范围

### 4.1 为什么停在 Stage A

Stage A 故意停在"把内置 plugins 暴露给前端"这一步：

1. Stage A 切断前端（`SearchPanel.tsx`、`SourcePickerModal.tsx` 里硬编码的 source 字符串）和后端（`handler/tag.go` 里硬编码的 `sourcesDefault` 切片）之间的强耦合。
2. 一旦 Stage A 上线，**新增任何 source 仍然要写 Go 代码**，但**不再需要任何前端改动**——新 source 会自动出现，因为前端只渲染后端返回的那份列表。
3. Stage A 是 **Stage B**（per-source config）和 **Stage C**（用户自写 JS plugins）的底座，所以独立停在 Stage A 也已经回本。

### 4.2 存储策略（已锁定）

单一可信用户、self-host 下"源真相"的层次：

| 层 | 内容 | 生命周期 |
| --- | --- | --- |
| 后端注册表 | 已注册的 `TagSource` + `DownloadSource` 实例 | 进程，启动期 |
| 后端 `GET /sources` | 注册表的元信息并集 | 每次请求 |
| 前端 localstorage | `app.enabledSources: string[]`（当前启用的 source key 集合） | 浏览器本地 |
| 前端搜索请求 | 请求体内 `enabled_sources: string[]`；为 `null` 表示全部启用 | 每次请求 |
| 后端持久化 | **无** | — |

当 `localStorage["app.enabledSources"]` 缺失或为空时的**默认行为**：
- 视为**全部启用**（请求体不携带 filter）
- 后端 `SearchMusic` 对**每一个**注册的 `TagSource` 都 fan-out，覆盖掉 legacy 里硬编码的 `qmusic,netease,kugou,migu,musicbrainz`
- 这一步取代当前 `sourcesDefault` 的硬编码默认序

### 4.3 API 契约

**Endpoint**：`GET /api/sources/`

**鉴权**：放在既有的 `authed` Gin router group 下（与其他 endpoint 一致）

**Response**：JSON 数组，按顺序。每个元素：

```jsonc
{
  "name":         "netease",                       // plugin key（作为 identifier）
  "displayName":  "网易云音乐",                     // 人类可读 label，可含 CJK
  "kind":         "tag",                          // "tag" | "download"
  "searchable":   true,                           // SupportsSearch()
  "lyric":        true,                           // SupportsLyric()
  "preview":      false,                          // future: SupportsPreview()
  "defaultOn":    true                            // server advisory；前端尊重用户 toggle
}
```

**Search request**：既有的 `POST /api/search_music/` 请求体多了一个可选字段：

```
"sources": ["netease", "qmusic"]   // 可选；缺失 = 全部启用
```

**向后兼容**：省略 `sources` 的旧调用方不变；后端把省略视为"fan-out 到所有已注册 tag sources"。

---

## 5. 文件级变更清单

### 5.1 后端

| 文件 | 变更 | 行数（约） |
|---|---|---|
| `internal/gateway/handler/source.go` | **新增文件**：定义 `SourceInfo` 结构 + `ListSources(c)` handler，遍历 `plugin.ListTagSources()` + `plugin.ListDownloadSources()` | +60 |
| `internal/gateway/router/router.go` | 挂载 `authed.GET("/sources/", handler.ListSources)` | +1 |
| `internal/gateway/handler/tag.go` | 删除 `sourcesDefault` 常量；`SearchMusic` 接受可选 `sources []string` 字段；空 / 缺失 → fan-out 到所有注册项 | −15 / +10 |

后端总增量：约 75 行，**加法**，不改动 interface，不需要重生成 proto。

### 5.2 前端

| 文件 | 变更 | 行数（约） |
|---|---|---|
| `frontend/src/types/index.ts` | 新增 `SourceInfo` TypeScript 类型 | +12 |
| `frontend/src/api/client.ts` | 新增 `getSources(): Promise<SourceInfo[]>` API 调用 | +8 |
| `frontend/src/store/useSourceStore.ts` | **新增文件**：Zustand store，含 `sources`、`enabled: Set<string>`、`loadSources()`、`toggle(name)`；`persist` 写到 `localStorage["app.enabledSources"]` | +60 |
| `frontend/src/components/search/SearchPanel.tsx` | 删除硬编码 `SEARCH_SOURCES` / `SOURCE_COLORS` / `VALID_SOURCES`；挂 `useSourceStore.loadSources()`；把 `enabled` 集合透传进 `searchMusic()` 请求体 | −40 / +30 |
| `frontend/src/components/search/SourcePickerModal.tsx` | 硬编码 chip 列表换成从 store 读取；按 `kind` 字段分别渲染 tag 行 / download 行 | −20 / +20 |
| `frontend/src/components/settings/` （新建目录） | 一个极简的新页面：per-source toggle 行 + `searchable` / `lyric` 角标 | +50 |

前端总增量：约 200 行（大多数是表单记账）。

### 5.3 验证命令

```sh
# 后端
go build ./...                             # 应保持 clean
go vet ./...                               # 新 endpoint 校验通过
# 手动：curl http://localhost:8000/api/sources/ → 7 个 tag + 1 个 download plugin

# 前端
cd frontend && npx tsc --noEmit -p tsconfig.app.json      # 应保持 clean
cd frontend && npx eslint src/                           # 新代码过 lint
# 手动：浏览器打开，`SourcePickerModal` 挂载后 chip 数 == 后端 source 数
```

---

## 6. 验收标准

Stage A 落地后：

1. ✅ `GET /api/sources` 返回 ≥ 7 个 entry（5 个 tag source + acoustics + 1 个 download source）
2. ✅ 从 `localStorage` 的 `app.enabledSources` 删掉 `netease`，刷新后 `SourcePickerModal` 看不到"网易云音乐" chip
3. ✅ `app.enabledSources` 为空时，search fan-out 到**所有**注册 plugin（请求体不带 `sources`，后端默认 = 全 fan-out）
4. ✅ 硬编码 `sourcesDefault` 常量已从 `handler/tag.go` 中消失
5. ⚠️ 新增一个内置 plugin（如写 `internal/plugin/example/server.go` 并通过 `init()` 注册），重启 gateway 后**自动**出现在 `SourcePickerModal`——证明"加 source = frontend 零改动"目标成立

---

## 7. 失败模式与缓解

| 失败 | 缓解 |
|---|---|
| Plugin gRPC client 冷启动 → 第一次多源 search 慢（~5s） | 文档里写明；warm pool 推迟到 Stage C |
| 用户禁用了所有 source → search 返回空 | UX：空 state + "all sources disabled" 提示，跳到 Settings |
| localStorage 被清 → 所有 source 恢复默认（全启） | 可接受（self-host、单一用户）；在 Settings UI 文档化 |
| 未来 plugin 引入第三种 `kind`（"album-art"、"playlist"） | 后端 `kind` 是 `string`、不是枚举——wire-compatible |

---

## 8. 后续 stage

### Stage B — per-source config 覆写（L1）

`data/sources/<name>.yaml`：API key 覆写、region URL 覆写、default-on 标志。gateway 启动时加载，**覆盖** plugin 构造方法的值。self-host 用户手编辑。约 ½ 天工作量。

### Stage C — 用户自写 JS plugin（L3）

`data/source_plugins/<name>.js`，每个 user source 一份。JS 导出 `meta`、`search(ctx, q, page, limit)`、`fetchId3ByTitle(ctx, title)`、`fetchLyric(ctx, id)`。后端用 `github.com/dop251/goja` runtime + 注入的 `fetch` / `crypto` / `console` / `setTimeout` bridge → 通过 Go 的 http.Client 调用户代码（CORS 在 gateway 边界统一处理）。每调用 5s timeout。约 1-2 周工作量。

### Stage D — runtime admin UI for plugin lifecycle

Web UI 来启 / 停 / 改 / 测 plugin，无需重启。约 ½ 天工作量。

---

## 9. 未决问题（稍后再决定）

- **Stage C 安全预算**：可信家庭用户 → 无需严格沙箱；但我们仍然每个 plugin 加 5s timeout + 256MB 内存上限吗？倾向：是。
- **per-source logging**：Stage A 是否在 `GET /api/sources` 中暴露每个 source 的健康状态？倾向：推迟到 Stage D。
- **MusicBrainz / AcoustID 浏览器侧直达**：既然 CORS 开放，能否让它们在注册表层直接走前端、跳过这两个 source 的后端路径？倾向：否——保持架构一致性；之后再说。

---

## 10. Stage A 的 Definition of Done

- [x] 本文档编写 + review
- [ ] `docs/plugable-plugins.md` 已提交
- [ ] 后端 `handler/source.go` + router + tag.go 改动合并 → `go build` clean
- [ ] 前端 `useSourceStore.ts` + `SourcePickerModal` + `SearchPanel` + Settings 页面合并 → `tsc` clean
- [ ] Manual e2e：放一个 `sources/netease.js` 占位（Stage C 脚手架）后，前端**无需 rebuild**就在 UI 上显现为 disabled chip
- [ ] `sourcesDefault` 常量已删除
