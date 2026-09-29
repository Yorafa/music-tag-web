# 部署指南

> 本文是 README 部署一节的完整版。README 只留最短的 5 步路径 + 指向这里。
>
> 只想快速评估、不想编辑 `.env`、不想自己生成 JWT → 直接看 [一键部署](#lazy)。
> 需要明确控制 secrets / CORS / gRPC TLS → 看 [Pre-flight](#preflight)。

**不需要在 host 上先构建前端。** React SPA 由 `Dockerfile.gateway` 的 `frontend` stage 在镜像内构建（`npm run build` → 产物 `COPY` 进 `/app/static/dist`），compose 里**故意没有** `./static` 卷挂载——挂上去反而会用 host 的空目录遮住镜像里那份，让 `/` 返回 "SPA not built"。`docker compose up -d --build` 一步就够；host 上残留的 `./static/dist` 不参与运行。**修改前端后**重跑 `docker compose up -d --build gateway` 即可；只有在**不用 Docker** 直接跑二进制时，才需要手动 `npm run build` 并把 `STATIC_DIR` 指向仓库根 `./static`。

<a id="lazy"></a>
## 一键部署（懒人模式 · 适合评估 / 家庭自用）

> 适合第一次部署 / NAS 评测 /「我不想手生 config」场景。仅 5 条 bash，所有 secret 在 gateway 首次启动时由 crypto/rand 生成、bcrypt-hash 后写到 `./data/.bootstrap-creds`（host 路径），重启后保持。

```bash
# ① clone & cd（替换为你的 fork URL）
git clone https://github.com/Yorafa/music-tag-web.git
cd music-tag-web

# ② 首次运行必要：创建 ./music ./data，并从模板生成 compose 副本
#    （副本不进版本库：里面会写进你的端口和口令）
mkdir -p ./music ./data
cp docker-compose.yml.template docker-compose.yml

# ③ 拉起全栈（自动生成 admin 账号、JWT secret、webhook token）
docker compose up -d --build

# ④ 等约 30 s 后查看首启口令
docker compose logs gateway | grep -A 6 FIRST-BOOT
#   admin user:                  admin
#   admin password (PLAINTEXT):  ← 记录这一行 ←
#   JWT_SECRET (base64, 48B):    ...
#   WEBHOOK_INTERNAL_TOKEN:      ...
#   persisted to:                /app/data/.bootstrap-creds

# ⑤ 打开浏览器
xdg-open http://localhost:9150/admin     # macOS 用 open；Windows 用 start
```

**首次启动的工作流程：**

- `docker-compose.yml.template`（拷成 `docker-compose.yml` 后使用）把 `JWT_SECRET` / `ADMIN_USERS` / `WEBHOOK_INTERNAL_TOKEN` 默认设成空 → gateway 看到空 → `ensureBootstrap()` 读取 `/app/data/.bootstrap-creds`，文件不存在则用 crypto/rand 生成三个 secret + bcrypt 一个 admin 密码，写不出就 FATAL 返回。
- 明文 admin 密码只出现一次：位于 gateway startup log 的 `FIRST-BOOT auto-bootstrap` banner；之后仅 `.bootstrap-creds` 里的 bcrypt hash 用于身份验证，**重启不会再打印**。
- 转发丢给同一个 `./music` 和 `./data` bind mount，静默重启后密钥保持。要轮换就删掉 `./data/.bootstrap-creds` 再重启。
- 懒人模式被**任何一项**显式配置绕过：`.env` 里 `JWT_SECRET` / `ADMIN_USERS` / `WEBHOOK_INTERNAL_TOKEN` 任一项填了真实值（非空、非 `__REPLACE_ME__` 这类 sentinel），该项就不再自动生成。哨兵值仍走自动填充，所以「只改了一项」不会把占位凭据带进生产。

**退出懒人模式 / 显式控制 secrets：** `cp .env.example .env` 后填入明确值，参考下面 Pre-flight。

<a id="preflight"></a>
## Pre-flight · boilerplate 检查

进入 deploy 之前**必须**读完 `.env.example` 顶部的 `⛔ PRE-FLIGHT CHECKLIST ⛔`。三项 `__REPLACE_ME__` 占位符（`JWT_SECRET`、`ADMIN_USERS`、`WEBHOOK_INTERNAL_TOKEN`）未替换会被 gateway `config.Load()` 在启动时 `log.Fatalf` 拒掉（除非显式设置 `ALLOW_INSECURE_DEFAULTS=1`，**仅 dev 可用**）。`docker-compose.yml.template`（先 `cp` 成 `docker-compose.yml`）、`.env.example`、所有 Dockerfile 都在仓库根，不需要再 cd 进子目录。

> ⚠️ **从更早版本升级再看本节**（全新部署跳过）。新 compose 默认路径改到仓库根，裸 `git pull && docker compose up -d` 会让 sqlite 重建、`./music` / `./data` 目录不存在而启动失败。这几步在任何时机都会成功——`{.env,data,music}` 即使 git 已不再跟踪，仍写在 NAS 上。
>
> 若你的曲库实际并不在仓库根 `./music` 下（例如 Synology `/volume1/music`、SMB `/mnt/nas/music`、NFS 等外部 mount，或你此前的旧 fork 用过宿主机 bind），**不要**靠 `mv` 把海量文件搬到 `./music`；直接在仓库根 `ln -s /volume1/music ./music` 软链即可，`docker compose up` 仍能穿过软链。
>
> 下面用 `mv -n`（POSIX no-clobber）：拒绝覆盖已有 backup；re-run 时两次 mv 都 no-op，状态可观察而不是静默覆盖。
>
> ```bash
> # 旧 fork 里如果你之前手动迁出过 `.env` / `data` / `music`，现在这些文件已经直接放在仓库根下了。
> # 升级只需：把任何之前手动建过的 `.env.bak` 拷回根 `.env`。全新部署直接 `cp .env.example .env` 即可。
> [ -f ./.env.bak ] && cp ./.env.bak ./.env  # 仅当你此前手动建过 `.env.bak` 时
> ```
>
> 之后 `docker compose up -d --build`。仓库根 `./music` / `./data` 即被 bind 进容器（不再走 `${MUSIC_DIR}` / `${DATA_DIR}` env 值插值），搬完后语义不变。

## 1. 克隆 + env

```bash
git clone https://github.com/Yorafa/music-tag-web.git
cd music-tag-web
cp docker-compose.yml.template docker-compose.yml   # 上一节没做的话
cp .env.example .env           # 与 docker-compose.yml 同目录，Compose 才能读到
# 编辑 .env，填入必填项
```

`JWT_SECRET` 和 `ADMIN_USERS` 是必填（不设 / 占位 → gateway 启动 Fail-closed 或登录拒绝）；强烈建议同时配置 `CORS_ALLOWED_ORIGINS`、`GRPC_USE_TLS`、`GATEWAY_PORT`。`MUSIC_DIR` / `DATA_DIR` 不再从 `.env` 注入（已硬编码到 `docker-compose.yml.template` 的 `./music` / `./data` host bind mount），若需指向外部 mount 直接 `ln -s` 进仓库根即可。模板里每项都有详细注释。

## 2. Volume pre-flight + 构建 + 启动全栈

`docker-compose.yml`（由 `.template` 拷来）的默认值 `./music`、`./data` 都是**相对 compose 文件路径**，首次运行必须存在；否则 `docker compose up` 会报 `volume source not found`。NAS 用户使用 SMB / NFS 挂载，先在宿主机准备好路径。

```bash
mkdir -p ./music ./data         # 首次需要（相对仓库根路径）
# 容器以 uid/gid 10001 非 root 运行；bind mount 的属主来自宿主机，镜像里改不了，
# 所以这两个目录必须交给 10001。从旧版本（root 运行）升级时这一步是必须的，否则 gateway 会 FATAL。
sudo chown -R 10001:10001 ./music ./data
docker compose up -d --build    # 后续只要不加 plugin / 不改 .env，重启即可跳过 --build
```

> 不想改宿主机目录属主？也可以在 compose 里给 `gateway` / `worker` 加 `user: "${UID}:${GID}"`（用你自己在宿主机上的 uid），但那样容器内就是你的 uid 而不是 10001 了。
>
> `audio-cache` 是 named volume，首次创建时 Docker 会从镜像里的 `/tmp/audio_cache` 目录（含属主）拷贝内容，不需要任何额外操作。它同时挂给 worker 和 youtube 插件——两边的 `AUDIO_CACHE_DIR` 必须一致，否则下载的文件插件写在一个卷、gateway 在另一个卷里找。
>
> 这个卷里存的是试听和「加库」时下载的音频，**不在曲库内，删掉不影响曲库文件**（之后重新试听同一首会再下一次）。worker 每 30 分钟检查一次，超过 `AUDIO_CACHE_MAX_MB`（默认 2048）就从最旧的文件开始删；`AUDIO_CACHE_MIN_AGE_MIN`（默认 30）内写入的文件不删——正在播放的那首和刚下载、正准备复制进曲库的那次下载都在这个窗口里。两个值 gateway 也要有同样的配置：设置页显示的就是它们，手动清理用的也是同一个保留窗口。也可以在「设置 → 通用设置 → 下载缓存」里看当前占用并手动清理；不想让 worker 自动删就把 `AUDIO_CACHE_MAX_MB` 设成 `0`。

不用 Docker 时才需要手动构建前端：`cd frontend && npm install && npm run build`，产物落盘到仓库根 `./static/dist/`，再把 `STATIC_DIR` 指向仓库根 `./static`。

首次启动会自动构建 gateway（含 API + 静态 SPA + `/media/*` 音乐流）、worker、8 个 gRPC 插件、redis，共 11 个容器。

## 3. 浏览器访问

`http://localhost:9150/admin`（gateway 直接对外服务 `${GATEWAY_PORT:-9150}`，容器内监听 8001；同时还提供 `/api/*` 接口、`/media/*` 音乐流式、以及 `/` 的 React SPA 静态产物）。

> 默认账号由 `ADMIN_USERS` 决定。**懒人模式下**首次启动后 admin 账号是 auto-bootstrap 生成的强随机口令（见 `docker compose logs gateway`）；**手动部署**下，未指定 `ADMIN_USERS` 会导致 login 拒绝（`ALLOW_INSECURE_DEFAULTS=1` 仅作为 dev 模式下的 `admin/admin` 退路）。

## 故障排查

| 现象 | 第一步 |
|---|---|
| gateway `FATAL: JWT_SECRET is unset or equal to the placeholder` | 仓库根 `.env` 补 `JWT_SECRET=$(openssl rand -base64 48)`（懒人模式则检查 `./data/.bootstrap-creds` 是否存在且可写） |
| gateway `FATAL: cannot persist bootstrap creds to /app/data/.bootstrap-creds` | `./data` 不存在或不可写；`mkdir -p ./data` 并确保非 root 用户可写。也可以绕开：`cp .env.example .env` 然后填真值 |
| 看不到 `FIRST-BOOT auto-bootstrap` banner | 大约需要 25–40 s：gateway 在做 DB init、dial 所有 plugin、bbolt 加载。不要 `docker compose logs gateway`（一次性截取），改用 `docker compose logs -f gateway` 跟随。重启后该 banner 不会再出现 |
| `docker compose up` 报 `volume source not found` | 先 `mkdir -p ./music ./data` |
| `/` 返回 "SPA not built" 或 404 | 多半是给 compose 挂了 `./static`，用空目录遮住了镜像里烘好的 bundle。删掉那个挂载后 rebuild |
| gateway healthcheck 一直 unhealthy | `docker compose logs gateway` + `docker compose logs redis` 看联通 |
| AcoustID 搜索没结果 | 先看 `docker compose logs acoustid`：fpcalc 缺失、文件打不开、API key 失效都会打日志。fpcalc 缺 → `docker compose build acoustid`（`Dockerfile.plugin` 只为 `PLUGIN=acoustid` 装 `chromaprint`）；文件打不开 → 检查 compose 里 acoustid 的 `./music:/app/media:ro` 挂载；key 失效 → 在 `.env` 设 `ACOUSTID_API_KEY`（[acoustid.org/login](https://acoustid.org/login) 注册）。未设时用官方公共测试 key，官方声明会在数日后过期 |

完整 operator 视角的安全默认值与 runbook 见 [`SECURITY.md`](../SECURITY.md)；架构与镜像体积见 [`docs/ARCHITECTURE.md`](ARCHITECTURE.md)。
