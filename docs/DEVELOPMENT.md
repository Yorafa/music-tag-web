# 本地开发与验证

> 面向 contributor：本地改完代码后如何验证、如何跑 pre-flight gate、再走 docker compose 全栈热部署。
>
> 命令以**仓库当前**盘上文件为准。带 ⚠️ 的段落是容易踩的坑，不是待办。

## 0. 一次性环境准备

```bash
node -v            # Node ≥ 22（Vite 7 的下限。package.json 里没有 engines 字段，靠这条约定）
go version         # Go 1.25+
docker compose version   # compose v2

# 拉依赖（前端一次性；后端用 go modules，缓存后不必重拉）
( cd frontend && npm install )
go mod download    # go.mod 在仓库根，不需要 cd 子目录
```

**Go 需要 CGO。** 依赖 `github.com/mattn/go-sqlite3`，`CGO_ENABLED=0` 编出来的 binary 会在启动时崩在 `Binary was compiled with 'CGO_ENABLED=0'`。`Dockerfile.worker` 因此显式设了 `CGO_ENABLED=1`；本机 `go build` / `go test` 同理（装了 gcc 就是默认开的）。

**fpcalc（chromaprint）不在 PATH 上时，声纹相关的测试会自己 skip**，不会失败——所以「测试全绿」不等于「声纹路径被测过」。要覆盖到就在本机装 `chromaprint`。

## 1. 后端（Go）

仓库根有 `Makefile`，能自动发现 `cmd/plugins/*` 下全部插件（当前 8 个，含 `youtube`）：

```bash
make build        # gateway + worker + 所有插件 → bin/（bin/ 已 gitignore）
make plugins      # 只编插件
make run-gateway  # go run ./cmd/gateway
make run-worker
make run-netease  # 7 个 tag 源插件各有 run-<name>（youtube 没有，用 go run ./cmd/plugins/youtube）
make proto-regen  # 改了 api/proto/*.proto 之后重新生成 .pb.go
```

> `make proto` 是更早的 target，输出目录不可靠（`Makefile` 里 `proto-regen` 的注释记录了它当年双写两个目录的问题）。**用 `proto-regen`。**

`Makefile` 没有的、或需要更细控制时走裸命令：

```bash
# 静态检查
go vet ./...

# 编译
go build ./cmd/gateway/ ./cmd/worker/
go build -o /tmp/plugin ./cmd/plugins/youtube/

# 测试（-count=1 禁用结果缓存，改完代码要重跑）
go test -count=1 ./...

# 单个包 / 单个测试
go test -count=1 ./internal/tasks/
go test -count=1 -run TestPrune -v ./internal/tasks/
```

仓库有完整的 `_test.go` 测试套件（`internal/tasks`、`internal/plugin/*`、`internal/gateway/*`、`internal/netguard`、`internal/utils`、`internal/ytdlp` 等）。**`go test ./...` 必须全绿**——CI 虽然只跑镜像构建，但测试是本地 gate 的一部分。

单独跑某个 plugin 便于把 log 隔离到 host：

```bash
NETEASE_PORT=50051 go run ./cmd/plugins/netease
```

## 2. 前端（React + Vite）

```bash
cd frontend

npm run typecheck   # tsc -b
npm run dev         # HMR，默认 http://localhost:5173
npm run build       # tsc -b && vite build → ../static/dist/
npm run lint
npm run lint:fix    # eslint --fix
npm test            # = vitest run
npm run test:watch
```

> ⚠️ **不要用 `npx tsc --noEmit` 代替 `npm run typecheck`。** 本目录的 `tsconfig.json` 是 solution 配置（`"files": []` + `references`），直接对它跑 `tsc` 会检查 **0 个文件**并静默返回 0——一个永远绿的假信号。必须走 `tsc -b` 才会跟随 references 覆盖 app / node / test 三个 project（含 `*.test.ts`）。

前端更细的配置说明见 [`frontend/README.md`](../frontend/README.md)。

## 3. 一键 pre-flight gate

全绿标准（与 `.agents/skills/music-tag-web-dev/SKILL.md` 里的 checklist 一致）：

```bash
# Go
gofmt -l internal/ cmd/          # 必须无输出
go build ./...
go vet ./...
go test -count=1 ./...

# 前端
( cd frontend && npm run typecheck )
( cd frontend && npm run lint )
( cd frontend && npm test )

# compose 文件本身
docker compose config --quiet
```

## 4. 全栈热部署

```bash
# 仅改某一 service 时，定向 rebuild（其他容器不停）
docker compose up -d --build gateway
docker compose up -d --build worker
docker compose up -d --build netease      # 任意 plugin 同理

# 改 Dockerfile / `.env`：全栈 rebuild
docker compose up -d --build

# 实时 log
docker compose logs -f gateway
docker compose logs -f worker
docker compose logs -f netease
```

镜像体积、构建分层与共享层为什么这么设计，见 [`docs/ARCHITECTURE.md`](ARCHITECTURE.md)。

## 5. host 跑 gateway + plugin（不走容器，便于 in-process 调试）

```bash
# 终端 1：host 跑 gateway，方向 localhost 上 plugin
GATEWAY_PORT=8001 \
PLUGIN_NETEASE_ADDR=localhost:50051 \
JWT_SECRET="$(openssl rand -base64 48)" \
ADMIN_USERS=test:test \
CORS_ALLOWED_ORIGINS=http://localhost:5173 \
go run ./cmd/gateway

# 终端 2：host 跑 plugin
NETEASE_PORT=50051 go run ./cmd/plugins/netease

# 终端 3：curl 验证
curl http://localhost:8001/api/sources/     # 8 条：7 个 tag source + youtube
curl http://localhost:8001/api/token/ -X POST -H 'Content-Type: application/json' \
  -d '{"username":"test","password":"test"}'    # → data.access
```

JWT 请求头前缀是 `Authorization: JWT <token>`（不是 `Bearer`）；gateway 的 `middleware/auth.go::JWTAuth` 两种都认。

## 6. 开发期易踩的坑

| 现象 | 原因 |
|---|---|
| `npm run typecheck` 假绿 | 用了 `npx tsc --noEmit`，solution 配置下检查 0 个文件。见上 |
| `go test` 报 `Binary was compiled with 'CGO_ENABLED=0'` | 本机 CGO 关了；`go-sqlite3` 需要 cgo |
| 声纹 / 查重相关测试全部 skip | 本机没有 `fpcalc`（chromaprint）。这是设计上的 skip，不是失败 |
| `go test` 偶发 `no such table: music_folder` | 用自己写的 `gorm.Open(sqlite.Open(":memory:"))` 开的测试库。`:memory:` 是**每连接**一个库，第二个池化连接会拿到空库。用 `internal/tasks/testdb.go` 的 `openTestDB(t)`，它把连接池限到 1 |
| plugin 启动后 gateway 仍报插件不可用 | `PLUGIN_<NAME>_ADDR` 要指向 plugin 实际监听的 host:port；容器内与 host 上的 50051 不是同一个地址 |
| 改了 proto 但行为没变 | 重新生成：`make proto-regen`，不是 `make proto` |
