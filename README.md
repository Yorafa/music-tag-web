![](img_6.jpg)

# 🚀 Music Tag Web | Self-hosted Docker 音乐刮削器 NAS无损曲库元数据批量管理工具
[简体中文](README.md) | [English](readme_en.md)

<a href="https://hellogithub.com/repository/d1919a26b74b40f19240da9f2ee3f7a3" target="_blank"><img src="https://abroad.hellogithub.com/v1/widgets/recommend.svg?rid=d1919a26b74b40f19240da9f2ee3f7a3&claim_uid=JQPHiFh3t5mqG1M" alt="Featured｜HelloGitHub" style="width: 250px; height: 54px;" width="250" height="54" /></a>
![star](https://atomgit.com/xhongc/music-tag-web/star/badge.svg)

## 项目简介
Music Tag Web 是一款**开源 self-hosted 自托管 Docker 音乐标签编辑器**，专为 NAS、远程影音服务器、Homelab 自建玩家打造，可在线编辑歌曲标题、专辑、艺术家、歌词、专辑封面等完整音频元数据，完美作为 Navidrome / Jellyfin 配套边车工具，替代本地 MP3Tag、MusicBrainz Picard。

支持 FLAC, APE, WAV, AIFF, WV, TTA, MP3, M4A, OGG, MPC, OPUS, WMA, DSF, MP4 全格式音频 ID3 标签批量编辑、刮削、修复整理，所有曲库文件本地存储不上传第三方，隐私安全，适配群晖、威联通、Linux 小主机 Docker 部署。
<div class="column" align="middle">
    <a href="https://go.dev/dl/"><img src="https://img.shields.io/badge/Go-1.23-00ADD8.svg" alt=""></a>
   <img src="https://img.shields.io/github/stars/xhongc/music-tag-web?color=informational&label=Stars">
  <img src="https://img.shields.io/badge/V2-backend-Go%20%2B%20gRPC-blueviolet?style=plastic" alt="V2 stack" />
  <img src="https://img.shields.io/badge/platform-amd64/arm64-pink?style=plastic" alt="docker-platform" />
</div>

> ⚠️ **仓库源码现为 Go 后端**（`cmd/` + `internal/`、含 7 个 gRPC 音乐源插件），Docker Hub 上的 `xhongc/music_tag_web:latest` 是 **Python/Django 老镜像**，仅供历史用户使用，不再维护，请改走下面的 V2 源码构建。

# 🎉 核心功能 Feature（Self-hosted Docker 专属优势）
为什么开发 Web 自托管版本？
很多自建 Navidrome / Jellyfin 的用户音乐文件存放在远程 NAS、Linux 服务器，本地 MP3Tag、MusicBrainz Picard 仅能操作本机文件，无法远程修改服务器无损曲库元数据。
Music Tag Web 采用 Docker 容器一键部署，作为影音服务配套边车应用，浏览器远程管理本地私有曲库，是 Homelab 影音爱好者刚需自托管音乐元数据工具。

- 全格式音频文件本地元数据查看、单条/批量编辑、修复 ID3 标签 — `internal/tag/reader.go` + `writer.go` + `handler.BatchUpdateID3` ✅
- 批量自动刮削音乐标签，自动匹配专辑信息、艺术家、歌词、封面 — 7 个 gRPC 源 fan-out，多 plugin `FetchId3ByTitle` ✅
- 内置音乐指纹识别，无标签、文件名混乱歌曲自动识别匹配元数据 — `internal/plugin/acoustid/server.go` (fpcalc shell-out) ✅
- 智能整理本地音乐文件，按艺术家、专辑自动分组，支持自定义多级曲库分类 — `FileBrowser.tsx` sort 已实现；per-artist/album grouping 仅有 sort，UI grouping 与自定义 multi-level 库分类尚未完成 🚧
- 多维度文件排序：文件名、文件大小、文件更新时间 — `FileBrowser.tsx` sort selector ✅
- 批量繁简转换，一键转换歌曲、专辑、艺术家标签简体/繁体 — `internal/tasks/matchscore.go:2` 显式 defer：P1 未引入 zhconv。`frontend/src/**` 无 opencc / HanziConvert 调用 ❌
- 文件名拆分解包，自动从文件名提取缺失歌曲、歌手、专辑信息补全标签 — `frontend/src/api/client.ts::parseFromFilename` 解析在前端；后端 batch write 回写通路未串 🚧
- 批量文本替换，清理曲库脏标签、乱码、多余特殊字符 — `TagEditor.tsx` 有 Replace 模态框（前端）；后端无对应的 bulk text-replace endpoint 🚧
- 集成 ffmpeg，支持无损音乐格式批量转换 — `Dockerfile.worker` 仅装 `yt-dlp`，无 ffmpeg 二进制；`internal/tasks/` 无 ffmpeg.go 任务 ❌
- 整轨 APE/FLAC/CUE 文件自动切割分轨并补全独立标签 — 全仓 grep `cuesheet|splitCue|shntool|cuebreakpoints` 零命中；worker 镜像无 shntool/cuetools ❌
- 多源音乐元数据接口，多渠道兜底刮削曲库信息 — `internal/plugin/registry.go` + 7 个 gRPC plugin ✅
- 内嵌歌词翻译，批量双语歌词写入音频文件 — 全部 `internal/plugin/**` + `frontend/src/**` 零翻译/双语调用；aspirational only ❌
- 完整操作日志记录，追溯标签修改记录 — `internal/db/models.go` 无 OperationLog 表，`frontend/**` 无 operationLog UI 表面；aspirational ❌
- 批量导出/自定义上传替换专辑封面 — 单条 cover 上传 ✅（`handler/file.go::UploadCover`）；批量 zip 导出未实现 🚧
- 全响应式移动端 UI，手机浏览器远程访问 NAS 曲库改标签 — `AppShell` + tailwind responsive utilities (`sm:` / `md:`) ✅
- 适配小爱同学本地曲库播放，直接读取 NAS 无损音乐文件 — `frontend/**` + `internal/**` 全仓 zero hits for `XiaoAI` / 小爱 / 米家 ❌
- 兼容各类私人网盘挂载曲库在线播放与标签编辑 — nginx 直出 `/media/{path}`，纯 bind-mount；应用代码无需逻辑（只是部署侧 volume 配置）✅
- 播放数据统计，柱形图、折线图可视化曲库播放记录 — `internal/db/models.go:89` 有 `AccessedDate` 列但 unused (注释 `// reserved for future playback tracking`)；`frontend/src/**` 0 hits for `BarChart` / `LineChart` / `recharts` ❌

完整状态表 + 每个 feature path:line 引用，见 [`docs/FEATURE-COVERAGE.md`](docs/FEATURE-COVERAGE.md)。

# 🦀 项目演示 Demo
在线演示地址（体验批量修改音乐标签、自托管Web端操作效果）
DEMO 地址账号密码为：admin/admin

[【音乐标签Web｜Music Tag Web 自托管Docker音乐元数据工具演示】](http://117.72.222.188:8002/#/)

# 💯 使用部署指南 How to Use
完整图文教程文档：
[【使用手册】](https://xiers-organization.gitbook.io/music-tag-web/)

[【使用手册V2（新版Docker部署推荐）】](https://xiers-organization.gitbook.io/music-tag-web-v2/)

> V2 为当前推荐部署方式，所有 NAS、Linux Homelab 用户优先使用手册 V2 部署！

## V1 旧版 Docker 容器部署方式
镜像已上传至 Docker Hub，支持 amd64 / arm64 架构群晖、威联通、树莓派设备一键安装：

### 1. 从Docker Hub拉取自托管音乐标签工具镜像
```bash
docker pull xhongc/music_tag_web:latest
```

### 2. 运行Docker容器镜像（挂载本地NAS音乐目录）
```bash
docker run -d -p 8001:8001 -v /path/to/your/music:/app/media -v /path/to/your/config:/app/data --restart=always xhongc/music_tag_web:latest
```
或者 使用 Portainer Stacks 可视化部署 docker compose（NAS用户常用）
   ![img_1.png](img_1.png)

```yaml
version: '3'

services:
  music-tag:
    image: xhongc/music_tag_web:latest
    container_name: music-tag-web
    ports:
      - "8001:8001"
    volumes:
      - /path/to/your/music:/app/media:rw
      - /path/to/your/config:/app/data
    command: /start
    restart: unless-stopped
```
> 重要说明：`/path/to/your/music` 替换为你的NAS/服务器本地音乐文件夹路径！`/path/to/your/config` 改为持久化配置文件路径！

3. 访问地址：127.0.0.1:8001/admin，默认账号密码 admin/admin，首次登录务必修改默认密码
![img_7.png](img_7.png)

## V2 推荐部署（当前主线 · Go + gRPC 插件架构）

> V1 镜像现在跟仓库源码不一致（镜像还是 Django，代码已经迁到 Go）。所有新部署请走源码构建。

> 🪜 **从老版本升级再看这一步**（全新部署跳过）。新 compose 默认路径已改到仓库根，裸 `git pull && docker compose up -d` 会让 sqlite 重建、`./music` / `./data` 目录不存在而启动失败。这 4 行 mv 在任何时机都会成功 — `gobackend/{.env,data,music}` 即使在 git 不再跟踪的情况下也仍写在 NAS 上。
>
> 若你的老 `.env` 把 `MUSIC_DIR` 指向 NAS 外部 mount（Synology `/volume1/music`、SMB `/mnt/nas/music`、NFS 等），或你之前用了其他宿主机 bind mount、或直接 `ln -s /volume1/music /path/gobackend/music` 软链 —— 跳过第 3 行；只要新 `./env` 里 `MUSIC_DIR=` 还指同一个外部路径即可。
>
> 下面 bash 块前后两段都用 `mv -n`（POSIX no-clobber，Alpine/BusyBox 1.21+ 都支持）：第一段拒绝覆盖已有 backup，第二段拒绝覆盖已有 root 文件；re-run 时（ `./env.bak` 和 `./env` 同时存在）两次 mv 都 no-op，状态可观察而不是静默覆盖。`rmdir` 本身非空拒绝。
> ```bash
> [ -f ./.env ] && mv -n ./.env ./.env.bak; mv -n gobackend/.env ./.env
> [ -d ./data ] && mv -n ./data ./data.bak; mv -n gobackend/data ./data
> [ -d ./music ] && mv -n ./music ./music.bak; mv -n gobackend/music ./music   # 仅当你之前用 ./gobackend/music 当本地 bind 时才需要；外部 mount / 软链 / FUSE 跳过
> rmdir gobackend   # 非空拒绝
> ```
> 之后 `docker compose up -d --build`。`${MUSIC_DIR}` / `${DATA_DIR}` 默认仍是 `./music` / `./data`,搬完后语义不变。

### 1. 克隆并准备环境变量
```bash
git clone https://github.com/xhongc/music-tag-web.git
cd music-tag-web
```

> ⚠️ **Pre-flight: boilerplate 检查。** 进入 step 2 之前必须看完 `.env.example` 顶部的 `⛔ PRE-FLIGHT CHECKLIST ⛔`，其中三个 `__REPLACE_ME__`（`JWT_SECRET`、`ADMIN_USERS`、`WEBHOOK_INTERNAL_TOKEN`）未替换会被 gateway `config.Load()` 拦下，log.Fatalf 拒绝启动（与 `ALLOW_INSECURE_DEFAULTS` 匹配才仅 WARNING）。`docker-compose.yml`、`.env.example`、Dockerfiles 都在仓库根下，不需要再 cd 进子目录。

把 `.env.example` 复制成 `.env`（与 `docker-compose.yml` 同目录，这样 Compose 才能读到）：
```bash
cp .env.example .env
# 然后编辑 .env，填入必填项
```
`JWT_SECRET` 和 `ADMIN_USERS` 是必填（不设 / 设为占位符 → gateway 启动 Fail-closed 或登陆返回 401）；强烈推荐同时配置 `CORS_ALLOWED_ORIGINS`、`GRPC_USE_TLS`、`MUSIC_DIR`、`DATA_DIR`、`NGINX_PORT`。模板里每项都有详细注释。
> 详细运维与安全默认值：见 `SECURITY.md` 与 `P1.5.md`。

### 2. Volume pre-flight + 构建并启动全栈
`docker-compose.yml` 的默认值 `./music` 与 `./data` 是**相对 compose 文件路径**，首次运行必须存在；否则 `docker compose up` 会报 `volume source not found`。NAS 用户如果使用 SMB / NFS 挂载，先在宿主机准备好路径。

```bash
mkdir -p ./music ./data         # 首次需要（相对仓库根路径）
docker compose up -d --build    # 后续只要不加 plugin / 不改 .env，重启即可跳过 --build
```
首次启动会自动构建 gateway + worker + 7 个 gRPC 音乐源插件（netease / kugou / kuwo / migu / qmusic / musicbrainz / acoustid）+ redis + nginx。

### 3. 浏览器访问

`http://localhost:9150/admin`（外层 nginx 暴露在 `NGINX_PORT`，gateway 监听 8001）。

> 默认账号由 `ADMIN_USERS` 决定。如果你看到 "admin/admin" 是在 `ALLOW_INSECURE_DEFAULTS=1` 仅调试模式下，真正上线 前必删该环境变量并设置真实密码。

# 📷 V2 版本操作界面 User Interface
远程浏览器批量管理NAS音乐标签、刮削元数据、整理曲库完整界面展示
![img_13.png](img_13.png)
![img_15.png](img_15.png)
![img_16.png](img_16.png)
![img_17.png](img_17.png)
![img_18.png](img_18.png)
![img_19.png](img_19.png)
![img_12.png](img_12.png)

# 💬 交流与反馈 Contact me
如果你在 self-hosted Docker 部署、NAS目录挂载、Navidrome 联动、批量标签刮削过程中遇到报错、功能需求，欢迎先 Star 项目后提交 issues。
issue 回复延迟可加入社群交流部署踩坑、曲库整理技巧；也可添加作者微信：charlesnowed（备注：**Music Tag**），拉你进交流群。

<div>
<img  src="/img0616.jpg" width="250">  &nbsp;
</div>

## 官方发布&交流频道：
[t.me/music_tag_web](https://t.me/music_tag_web)

[MusicTag Web 自托管影音交流群](https://t.me/+oTffyBoNALM3Yzll)

QQ1群：55893996 （已满）

QQ2群：79502786（NAS/Docker影音自建玩家交流）

# 💸 赞助与支持
如果这款 self-hosted Docker 音乐标签工具帮你整理好了NAS无损曲库、解决Navidrome标签混乱问题，可以请作者喝杯咖啡。
您的支持是持续更新自托管功能、适配更多NAS设备的动力, 谢谢您! (｡･∀･)ﾉﾞ

[➡ 爱发电](https://ifdian.net/a/music-tag-web)

# 🌟 Star History
开源自托管音乐元数据工具项目增长趋势

[![Star History Chart](https://api.star-history.com/svg?repos=xhongc/music-tag-web&type=Date)](https://star-history.com/#xhongc/music-tag-web&Date)

<!--
self-hosted, music tag editor, docker music metadata tool, NAS flac tagger, navidrome sidecar tag editor, jellyfin batch id3 editor, homelab music manager, 自托管音乐标签工具, docker音乐元数据编辑器, 群晖音乐批量改标签, 威联通无损曲库整理, 替代mp3tag网页版, 音乐指纹识别刮削标签, 本地私有曲库管理
-->

# 免责声明
禁止任何形式的商业用途，包括但不仅限于售卖/打赏/获利，不得使用本代码进行任何形式的牟利/贩卖/传播，再次强调仅供个人私下研究学习技术使用，**本自托管项目仅编辑本地已有音乐文件元数据，不提供下载音乐本体！**
本项目仅以纯粹的技术目的去学习研究，如有侵犯到任何人的合法权利，请致信408737515@qq.com，我将在第一时间修改删除相关代码，谢谢！

本项目基于 GPL V3.0 许可证发行，以下协议是对于 GPL V3.0 的补充，如有冲突，以以下协议为准。

词语约定：本协议中的“本项目”指music-tag-web项目；“使用者”指签署本协议的使用者；“官方音乐平台”指对本项目内置的包括酷我、网易云、QQ音乐、咪咕、酷狗音乐、酷我音乐等音乐源的官方平台统称；“版权数据”指包括但不限于图像、音频、名字、歌词等在内的他人拥有所属版权的数据。

本项目的数据来源原理是从各官方音乐平台的公开服务器中拉取数据，经过对数据简单地筛选与合并后进行展示，因此本项目不对数据的准确性负责。 使用本项目的过程中可能会产生版权数据，对于这些版权数据，本项目不拥有它们的所有权，为了避免造成侵权，使用者务必在24小时内清除使用本项目的过程中所产生的版权数据。 本项目内的官方音乐平台别名为本项目内对官方音乐平台的一个称呼，不包含恶意，如果官方音乐平台觉得不妥，可联系本项目更改或移除。 本项目内使用的部分包括但不限于字体、图片等资源来源于互联网，如果出现侵权可联系本项目移除。 由于使用本项目产生的包括由于本协议或由于使用或无法使用本项目而引起的任何性质的任何直接、间接、特殊、偶然或结果性损害（包括但不限于因商誉损失、停工、计算机故障或故障引起的损害赔偿，或任何及所有其他商业损害或损失）由使用者负责。 本项目完全免费，仅供个人私下小范围研究交流学习 python 技术使用, 且开源发布于 GitHub 面向全世界人用作对技术的学习交流，本项目不对项目内的技术可能存在违反当地法律法规的行为作保证，禁止在违反当地法律法规的情况下使用本项目，对于使用者在明知或不知当地法律法规不允许的情况下使用本项目所造成的任何违法违规行为由使用者承担，本项目不承担由此造成的任何直接、间接、特殊、偶然或结果性责任。 若你使用了本项目，将代表你接受以上协议。
