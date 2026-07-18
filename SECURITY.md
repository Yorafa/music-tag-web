# 安全指南

本文档说明 Go 后端如何默认强制执行硬化基线，以及运维方在生产环境必须配置哪些变量。

---

## TL;DR（速查）

正式上线前必须设置：

```
JWT_SECRET=$(openssl rand -base64 48)
ADMIN_USERS='alice:$(openssl rand -base64 32),bob:$(openssl rand -base64 32)'
CORS_ALLOWED_ORIGINS='https://music.example.com'
GRPC_USE_TLS=1
GRPC_TLS_CA_FILE=/etc/music-tag/ca.pem   # 可选；不设置则使用系统根证书
```

在**任何**非本地部署中，**不要**设置 `ALLOW_INSECURE_DEFAULTS`。

## 各控制项的实际作用

### JWT_SECRET

- P1 之前：环境变量未设置时，默认值为 `"change-me-in-production"`。
- 现在：`config.Load()` 在 `JWT_SECRET` 为空或等于该占位符时会拒绝启动（`log.Fatalf`），除非显式设置了 `ALLOW_INSECURE_DEFAULTS=1`。`Login` / `Refresh` / `Verify` 在该值为占位符时也会显式拒绝，避免 dev 默认值泄露 token。

> 注：`ADMIN_SUBSONIC_TOKENS` 已在 Subsonic REST（`/rest/…`）随 P2.0 退役时一并移除。Subsonic 盐值 token 鉴权不再受支持——仍需要 Subsonic 客户端兼容性的运维方，应另行部署一个独立的 Navidrome / Funkwhale 实例，前端鉴权仍走 JWT。

### ADMIN_USERS

- P1 之前：无论环境变量是否设置，都硬编码 `admin:admin` fallback。
- 现在：若 `ADMIN_USERS` 未设置且 `ALLOW_INSECURE_DEFAULTS!=1`，`handler.Login` 对所有凭据返回 401——Fail-closed。

### CORS_ALLOWED_ORIGINS

- P1 之前：`Access-Control-Allow-Origin` 回显任何调用方，配合 `Access-Control-Allow-Credentials: true`。
- 现在：只有白名单中的 origin 才会被授予响应头，且附带 `Vary: Origin` 以保证缓存正确。空列表 = 完全不允许跨域（浏览器直接拒绝）。

### GRPC_USE_TLS

- P1 之前：每个 gRPC dial 都使用 `insecure.NewCredentials()`。
- 现在：设置 `GRPC_USE_TLS=1` 后 dial 走 TLS。可选的 `GRPC_TLS_CA_FILE` 把你的私有 CA 加入信任池。当 `ALLOW_INSECURE_DEFAULTS!=1` 且 TLS 关闭时，启动期会输出一条明显 WARNING（保证 docker-compose dev 兼容而不会拒启动）。

### SSRF 防护

`fetchRemoteBytes`（`POST /api/update_id3/` 中的封面下载）现在统一经过 `internal/netguard.Guard`：

- 只接受 `http` / `https`（`file://`、`gopher://`、`javascript:` 全部拒绝）
- host 必须能解析
- 所有解析出的 IP 必须满足 `isPublic`（拒绝 loopback、v4/v6 私有、link-local、multicast，以及 AWS 元数据地址 `169.254.169.254`）

> **DNS rebinding 不在本次 v1 缓解范围内。** 推荐的补充缓解：在 gateway 之前套一层 HTTP proxy，让它在 dial 时重新解析并拦截 RFC1918 网段；或者覆写 `netguard.Guard.Resolver` 让它在 dial 时锁定已解析 IP。

### 路径遍历防护

`internal/utils.SafeJoin(root, p)` 只在解析出的路径真实位于 `root` 之下时才返回。所有涉及文件读写的 handler（`/api/file_list`、`/api/music_id3`、`/api/update_id3`、`/api/batch_update_id3`）都通过这个工具把用户传入的 path 强制根生于 `MUSIC_DIR`；`"."`、`".."`、指向外部的绝对路径都返回 `Failure(c, "路径不安全")`。

> **例外：`/media/*filepath` 静态文件服务**。v2 起不再有 nginx，`/media/<file>` 直接由 `internal/gateway/router.Setup()` 通过 `http.Dir("/app/media")` 服务浏览器 audio element。`http.Dir` 调用 `os.Open` 而会路径正规化，实际上拒絕了 `..` 越出 /app/media，与原 nginx `alias /app/media/` 行为等价；只会跟从由运维手动建在 /app/media **里面** 的符号链接（这是谋划性的）。该路径不为用户输入服务，表面上与 `api/**` 应用的 SafeJoin 不挂勾——这是有意识的，前端 audio element 不会以 `..` 拼接 `/media/`。

### yt-dlp 参数注入

`POST /api/youtube_download/` 接受一个 `extra_audio_format` 提示参数。`internal/tasks/yt_dlp_validate.go` 对三个字段做清洗：

- `format` — 正则 `^[a-zA-Z0-9_./+<>:=]{1,64}$`，拒绝 `-` 前缀
- `output_format` — 闭合枚举 `mp3 | m4a | ogg | vorbis | wav | ""`
- `quality` — `^[0-9]{1,4}$`

Gateway（`handler.YoutubeDownload`）与 worker（`tasks.YouTubeDownloadHandler.ProcessTask`）两侧**都**重新校验；纵深防御可以同时挡住任务 payload 重放与 DB 篡改。

---

## 威胁模型检查（修复后）

| 攻击面 | 状态 |
|---|---|
| enemy.com 跨域请求 | 被 CORS 白名单拦截 |
| 前端 → 后端 SSRF 到 AWS metadata | netguard 拒绝 169.254 |
| 前端 → 后端 SSRF 到内网 Redis（私网 10/8） | netguard 拒绝 |
| 前端 `/api/file_list` 用 `../` 文件遍历 | SafeJoin 拒绝 |
| 前端 `/api/update_id3` 路径注入 | SafeJoin 拒绝，并改写 rename target |
| worker `exec.CommandContext(yt-dlp)` 携带 `--exec=…` | yt_dl sanitize 拒绝 |
| `/api/token/` 用 `admin:admin` 未鉴权访问 | dev 模式外 fail-closed |
| 用占位 secret 伪造 JWT | `Login` 拒绝签发；`Verify` 拒绝验签 |
| gRPC MITM 中间人攻击 `Search` / `FetchID3ByTitle` 响应 | TLS handshake 阶段 flag |
| DNS rebinding（SSRF 在 dial 时解析到私网） | 范围外（见 SSRF 说明） |
| 插件客户端的 mTLS | 范围外（当前仅服务端 TLS） |

---

## 运维 runbook

### 启用 gRPC plugins 的 TLS

1. 生成（或复用）一个私有 CA，以及每个 plugin 对应的服务端证书。
2. 把 CA bundle 挂载进 gateway 与 worker 容器的同一路径（例：`/etc/music-tag/ca.pem`）。
3. 在 compose env 中设置 `GRPC_USE_TLS=1` 与 `GRPC_TLS_CA_FILE=/etc/music-tag/ca.pem`。被同一个 `ca.pem` 信任，是**每个** plugin 服务端 TLS 证书的共同前提。
4. 验证：

   ```bash
   openssl s_client -connect plugin-host:50051 -CAfile ca.pem
   ```

   每个 plugin 会打印 `[gateway] gRPC plugins: TLS enabled (CA=…)`。

### 残留 WARNING 的解释

`config.Load()` 可能打印看起来很严重但其实并非致命的 message：

- `"WARNING: ADMIN_USERS unset"` — login 将返回 401（想迁移老 Subsonic 兼容 payload 的调用方，应另外部署一个 bridge server 配合 gateway）。
- `"WARNING: gRPC plugins connect via insecure credentials"` — 运维方必须显式通过 `ALLOW_INSECURE_DEFAULTS=1` 才能看到这一条；否则更靠前的占位 JWT_SECRET 检查就已经 fatal。

只有 FATAL log line 会真正停掉 binary：

```
[config] FATAL: JWT_SECRET is unset or equal to the placeholder;
refusing to start. Set JWT_SECRET to a strong value (e.g.
`openssl rand -base64 48`) or set ALLOW_INSECURE_DEFAULTS=1
for explicit local dev.
```

---

## Reliability hardening（v2.2+ 补充）

> 本节记录的是**可靠性 + fan-out 抗噪路径**——不是 security threat mitigation。完整威胁模型见上方 `## 威胁模型检查（修复后）`。本节面向运维排查与前端 silent-fail 诊断。

### gRPC 客户端 keepalive（plugin 重启 / 容器 blip 后 fan-out 不再命中 EOF）

`internal/plugin/grpc_adapter.go::clientKeepaliveParams` 已在两条 `grpc.DialContext`（`GRPCTagSource.ensureConn` + `GRPCDownloadSource.ensureConn`）挂载：

- Time：`30s` —— 默认间隔发 PING
- Timeout：`10s` —— 等待 PING ACK 超时
- **`PermitWithoutStream: true`** —— channel 在 **no active stream** 阶段也保持 ping。这是修复的关键：默认 keepalive 在 idle 期会自动停止，反而把"长时间空闲 + 偶发业务请求"的场景让死链检测失灵。

修复前症状（运维典型诊断入口）：`docker compose logs gateway | grep -E 'rpc error: code = Unavailable desc = error reading from server: EOF'`，常出现在 plugin 容器重启 / 宿主网络抖动后**下一次业务 RPC**。

修复后：channel 自带死链探测；plugin 容器启回后第一次 RPC 写入即经 TCP 层 RST → gateway 自动重拨。

诊断锚点（残留不确定时）：

```bash
# 谁、在哪一次 EOF？配 ACL 'docker events' 看 plugin container restart 时间。
docker compose logs gateway | grep -E '\[\w+\] plugin "\w+".*EOF'
```

### Plugin silent-fail（dirty upstream → fan-out 仍继续）

`kuwo` 与 `qmusic` 在某些 dirty upstream 响应路径**主动返回 0 条 + nil error**，让单 source 失败**不阻塞**其他 source 的 fan-out。这是 gateway `internal/gateway/handler/tag.go::SearchMusic` 的容错模型——一个不稳的 source 不应让整个搜索结果空掉。

具体行为：

| Plugin | 触发 | 行为 |
|---|---|---|
| `internal/plugin/kuwo/server.go::doSearch` | 上游返回非合法 JSON（如 HTML 错误页 / partial JSON） | `log.Printf` 一行含 ≤80 字节响应头预览 → `return nil, false, nil` |
| `internal/plugin/qmusic/server.go` | QQ 上游 `"list"` 为 `null` / `[]` / 数字 `0` / `-1` 等 empty-result 哨兵 | 跳过 typed unmarshal → 走 loose 兼容 → 可能返 0 条 + nil |

诊断锚点：

```bash
# 一个 enabled source 长期 "无结果" 但前端没法分辨
# "真的无匹配" 与 "plugin silent fail" —— 第一动作是 grep 下面这行。
docker compose logs gateway | grep -E '\[SearchMusic\] plugin "[^"]+"'
```

silent-fail 是**设计选择**（保 fan-out 全局可用），不是 bug。如果某个 source 长期出现 silent-fail log（例如一个月累计 ≥ 1000 条），通常是该源上游接口变化 / 限流 / IP 被封，在 `docs/plugable-plugins.md` Stage B 后可通过 per-source YAML 覆写 baseURL 暂避。
