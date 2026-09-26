![](img_6.jpg)

# 🚀 Go Music Tag Web — Self-hosted Docker 音乐元数据批量编辑工具（Go + gRPC + React）
[简体中文](README.md) | [English](#)

<div class="column" align="middle">
    <a href="https://go.dev/dl/"><img src="https://img.shields.io/badge/Go-1.25-00ADD8.svg" alt="Go"></a>
    <a href="https://react.dev/"><img src="https://img.shields.io/badge/React-19-149eca.svg" alt="React"></a>
    <a href="https://grpc.io/"><img src="https://img.shields.io/badge/gRPC-8%20plugins-blueviolet?style=plastic" alt="gRPC plugins"></a>
    <img src="https://img.shields.io/badge/self--hosted-Docker-orange?style=plastic" alt="self-hosted">
    <img src="https://img.shields.io/badge/platform-amd64/arm64-pink?style=plastic" alt="docker-platform">
</div>

> ⚠️ **本项目 fork 自 [`xhongc/music-tag-web`](https://github.com/xhongc/music-tag-web)**——原 Python/Django 单体代码已**完整重构**为 Go 1.25 + gRPC 微服务插件 + React 19 SPA 架构。本仓库以 GPL V3 协议 fork-publish 独立维护；上游著作权与许可证全文保留在根目录 [`LICENSE`](LICENSE)。重构过程的 rationale 详见底部 [Acknowledgements](#acknowledgements)。

---

## 项目简介

**Go Music Tag Web** 是一款 self-hosted、Docker 化、面向 NAS / 远程影音服务器 / Homelab 自建玩家的批量音乐元数据编辑工具：浏览器远程编辑本地私有曲库的标题、专辑、艺术家、歌词、专辑封面等元数据，作为 [Navidrome](https://www.navidrome.org/) / [Jellyfin](https://jellyfin.org/) / [Funkwhale](https://funkwhale.audio/) 等自托管音乐服务器的 sidecar 服务。

支持 [FLAC / APE / WAV / AIFF / WV / TTA / MP3 / M4A / OGG / MPC / OPUS / WMA / DSF / MP4] 全部主流有损/无损格式；**所有音乐文件本地处理，不上传第三方**；适配群晖、威联通、unRAID、Linux 小主机、amd64 / arm64 架构。

后端 HTTP gateway 与后台 worker 拆为两个独立容器；7 个音乐源（netease / kugou / kuwo / migu / qmusic / musicbrainz / acoustid）**每个都是独立 gRPC 微服务进程**，独立部署、独立失败隔离。Tag I/O 走原生 Go 库（`bogem/id3v2` + `dhowden/tag`），无 FFI / 无 virtualenv / 无外部 binary 调用链。前端是 React SPA，状态走客户端 Zustand + `localStorage` 持久化。整套镜像约 560 MB。

## 架构速览

| 层 | 实现 |
|---|---|
| HTTP 服务 | Go 1.25 + gin（gateway：API + React SPA 静态 + `/media/*` Range 流） |
| 异步任务 | asynq + Redis（worker） |
| 音乐源 | **gRPC 微服务**：7 个元数据源 + 1 个下载源（youtube）——各自独立进程 |
| Tag I/O | `bogem/id3v2` + `dhowden/tag`（纯 Go 库，无 Python FFI） |
| 前端 | React 19 + Vite 7 + TypeScript + Tailwind 4 + shadcn/ui + Zustand |
| 鉴权 | JWT in-memory + bcrypt；fail-closed 默认值检测 |
| 部署 | 单条 `docker compose up -d --build` 拉起 gateway + worker + 8 gRPC plugin + redis，共 11 个容器 |

镜像体积为什么是 560 MB（88 MB ffmpeg 库如何在四个镜像间共用一次）、容器拓扑、插件进程模型 → **[`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md)**。

## 核心功能

> 标记：`✅` 已实现 · `🚧` 部分实现（已知 gap）· `❌` 暂未实现。
> **完整 status 表、每个 feature 的 `path` 引用与取舍理由见 [`docs/FEATURE-COVERAGE.md`](docs/FEATURE-COVERAGE.md)**——那是单一来源，本文只是速览。

| 领域 | 状态 | 一句话 |
|---|---|---|
| 标签编辑 | ✅ | 全格式 ID3 / Vorbis / APE 读写；单条实时编辑 + **选 N 首人工批量填值**（每字段带「不修改」勾选，留空即清空该标签）；歌词与封面 sidecar 自动落盘 |
| 批量清空标签 | ✅ | 取消勾选 + 留空 = 删掉该 tag（`Clear*` + `null` 线上契约），不是「不修改」 |
| 列编辑（per-row inline） | ❌ | 不做。剩下的只是开 spreadsheets，收益只在「每首要改的值都不同」这一种场景；人工批量填值已由上面的弹窗覆盖 |
| 元数据刮削 | ✅ | 7 个源 fan-out 聚合 + 去重 + 按匹配度排序；搜索源可动态开关；per-source YAML override 热重载 |
| 候选可信度 | ✅ | 声纹置信度标「声纹匹配 xx%」；纯文本来源只陈述事实（标题完全/部分匹配），没有分数就写「未按音频校验」——**不编数字** |
| 曲库查重 | ✅ | 只读跑四层漏斗（文件名 / SHA-256 / 声纹 / 元数据），标「重复 / 疑似 / 唯一」；只有内容级证据才算重复 |
| 一键删除重复文件 | ✅ | 仅内容级重复开放，互相指认时两边都不删；删除是移入 `DATA_DIR/.trash/<时间戳>/`，不是真删 |
| 歌词 | ✅ | 多源拉取，写回 lyrics tag + 同名 `.lrc` sidecar；双语歌词 ❌（不引入第三方翻译 API） |
| 封面 | ✅ | 远端拉取（带 SSRF 防护）+ 自定义上传；批量打包 zip 🚧 |
| 曲库 / 文件管理 | ✅ | 递归扫描、多维度排序状态已在 store（UI 未接线 🚧）、按艺术家/专辑分组 |
| 声纹索引与缓存 | ✅ | 索引自维护（有进展就排下一次，30s + `Unique` 去重）；子指纹带 duration 存进索引，重复查重零解码（400ms → 24ms），失效判据是 size **和** mtime |
| 「清理残留」 | ✅ | 逐行问内核文件在不在，只删确定的 ENOENT；刻意不做 `WHERE path NOT IN` 集合差集 |
| 文件名解析 | ✅ | preview 返回 token + 逐行 `{artist,title,status}`（10 分钟 TTL），apply 走 asynq 批量写；Go + TS 双引擎在 200+ NFC fixture 上 deep-equal |
| 下载 / 抓取 | ✅ | YouTube / B 站走 yt-dlp，参数 sanitize 防 `--exec=` 注入；5 个音乐源 download plugin |
| 操作日志 | ✅ | GORM `OperationLog` 持久化 + 侧边栏「操作审计」面板（多维过滤 / 模糊检索 / 变动详情 / 清空） |
| 文本清洗 / 编码 | ❌ | 无 bulk text-replace endpoint；未引入 opencc / zhconv（理由见 `docs/plans/Unfinished-Features.md`） |
| 整轨切割 / ffmpeg 转换 | ❌ | 无 CUE 解析 / 切轨代码；缺的是转换 task 与 UI，不是 binary（youtube 镜像已装 ffmpeg） |
| 播放统计 | ❌ | DB `AccessedDate` 列已 reserved 但未消费；无 Subsonic `/rest/` 上报 |
| 手机 UI | 🚧 | 响应式布局已做，设备专项适配未完成 |

## 快速开始

**不需要在 host 上构建前端**——React SPA 在 `Dockerfile.gateway` 的 frontend stage 里构建好并烘进镜像，compose 里故意没有 `./static` 挂载。

```bash
git clone https://github.com/Yorafa/music-tag-web.git
cd music-tag-web
mkdir -p ./music ./data
docker compose up -d --build

# 等约 30 s，取首启口令（明文密码只出现这一次）
docker compose logs gateway | grep -A 6 FIRST-BOOT
```

打开 `http://localhost:9150/admin`。所有 secret 由 gateway 首启时用 crypto/rand 生成并写进 `./data/.bootstrap-creds`。

容器以 uid/gid **10001** 非 root 运行，bind mount 的属主来自宿主机，所以首次部署要 `sudo chown -R 10001:10001 ./music ./data`。

要显式控制 secrets / CORS / gRPC TLS、从旧版本升级、或者遇到 `volume source not found` / `SPA not built` / AcoustID 无结果 → **[`docs/DEPLOYMENT.md`](docs/DEPLOYMENT.md)**。

## 安全默认值（摘要）

6 项 hardening 全部落地且各有测试兜底：SSRF 拒 loopback / 私有网段、路径遍历 containment、yt-dlp 参数 sanitize 三层防线、JWT / admin 默认值 fail-closed、CORS 白名单无反射、容器非 root。

完整威胁模型与 runbook → **[`SECURITY.md`](SECURITY.md)**。

---

## 📚 相关文档

| 文档 | 内容 |
|---|---|
| [`docs/DEPLOYMENT.md`](docs/DEPLOYMENT.md) | 部署：懒人模式 / 显式 `.env` / volume 与属主 / 旧版本升级 / 故障排查表 |
| [`docs/DEVELOPMENT.md`](docs/DEVELOPMENT.md) | 本地开发：环境准备、`Makefile`、pre-flight gate（全套命令）、热部署、host 调试、开发期易踩的坑 |
| [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md) | 架构：分层表、容器拓扑、镜像体积与共享层、插件进程模型 |
| [`docs/FEATURE-COVERAGE.md`](docs/FEATURE-COVERAGE.md) | **功能覆盖矩阵（单一来源）**：每个 feature 的 status、`path` 引用、未实现项的取舍理由 |
| [`SECURITY.md`](SECURITY.md) | 运维安全默认值 / 威胁模型 / runbook |
| [`docs/plugable-plugins.md`](docs/plugable-plugins.md) | 插件架构设计草图（Stage A 已落地 / B–D 未排期） |
| [`docs/plans/Unfinished-Features.md`](docs/plans/Unfinished-Features.md) | 未实现功能的取舍记录 |
| [`frontend/README.md`](frontend/README.md) | 前端 dev 启动 / Vitest / React Compiler 配置 |
| [`AGENTS.md`](AGENTS.md) | 仓库内协作工具相关说明（issue tracker / triage labels / domain docs） |
| [`REVIEW.md`](REVIEW.md) | 审计报告（本地，不入 git） |

---

## 🌟 Star History

[![Star History Chart](https://api.star-history.com/svg?repos=xhongc/music-tag-web&type=Date)](https://star-history.com/#xhongc/music-tag-web&Date)

<!--
self-hosted, music tag editor, docker music metadata tool, NAS flac tagger,
navidrome sidecar tag editor, jellyfin batch id3 editor, homelab music manager,
docker gRPC plugin music scraper, react tailwind shadcn music tag editor,
golang music library manager, self-hosted docker music tagger,
自托管音乐标签工具, docker 音乐元数据编辑器, 群晖音乐批量改标签,
威联通无损曲库整理, 替代 mp3tag 网页版, 音乐指纹识别刮削标签,
本地私有曲库管理, Go Music Tag Web
-->

---

## <a id="acknowledgements"></a>Acknowledgements · 上游与重构说明

本项目的 **upstream** 是 [`xhongc/music-tag-web`](https://github.com/xhongc/music-tag-web) ，最初由 *xhongc* 维护，原仓库以 **GPL V3** 协议开源。本仓库 fork 自该 upstream，并对整份代码做了**完整重写**：

| | upstream | 本仓库 |
|---|---|---|
| 后端 | Python 3 + Django + gunicorn + celery | Go 1.25 + gin + asynq + Redis |
| 音乐源 | 同进程 python module | 独立 gRPC 服务进程 |
| Tag I/O | `mutagen` (Python 库) | `bogem/id3v2` + `dhowden/tag`（纯 Go） |
| 前端 | Django template + Bootstrap jQuery | React 19 + Vite 7 + TypeScript + Tailwind 4 + shadcn/ui + Zustand |
| 部署 | 单 Python 容器 (~600 MB) | 多 service compose（gateway + worker + 8 gRPC plugin + redis），总 image 约 560 MB |
| 鉴权 | Django session | JWT in-memory + bcrypt |
| 默认配置保护 | 软默认值 | Fail-closed（占位 JWT_SECRET / 默认 admin 都被拒） |

**本仓库沿用 GPL V3 协议**——[`LICENSE`](LICENSE) 内容未做任何修改，上游著作权声明与许可证全文完整保留。任何对本仓库的使用、再分发、修改，都必须遵守 GPL V3 条款（即：同等开源 + 保留版权声明 + 注明修改）。

本仓库的修改记录以 git commit 历史为准；上游提供的功能 claim 保留溯源痕迹，README 的功能速览表与 [`docs/FEATURE-COVERAGE.md`](docs/FEATURE-COVERAGE.md) 的 row 一一对应。

**侵权投诉 / 版权诉求**：如收到版权方对歌词、封面、专辑元数据的诉求，将在 24 小时内按上游 LICENSE 与 GPL V3 条款要求清理数据。本仓库仅编辑本地已有音乐文件元数据，**不下载、不存储、不分发任何受版权保护的音频本体**。欲联系维护者请用 GitHub Issues / Pull Requests 公开流程，或邮件 [maintainer@your-domain.example]（占位，按需替换）。

---

## <a id="license"></a>License

GNU General Public License **v3.0** — 完整文本见 [`LICENSE`](LICENSE)。

```
Go Music Tag Web
Copyright (C) <year>  <name of author>

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
GNU General Public License for more details.

You should have received a copy of the GNU General Public License
along with this program.  If not, see <https://www.gnu.org/licenses/>.
```

---

## 免责声明 / Disclaimer

**禁止任何形式的商业用途**，包括但不限于售卖 / 打赏 / 获利 / 广告分成。本项目仅供个人私下研究学习技术使用，与原 upstream 仓库解耦后**独立维护**。

**本项目仅编辑本地已有音乐文件的元数据，不提供、不下载、不存储任何受版权保护的音频本体。** 仓库内置的 7 个音乐源 plugin（netease / kugou / kuwo / migu / qmusic / musicbrainz / acoustid）只向各官方平台的公开服务器发起查询请求，**不绕过任何身份验证、不缓存任何受版权保护的内容**；抓取到的元数据（曲名 / 歌手 / 专辑名 / 封面 URL / 歌词文本）属于上游各官方平台的版权数据，使用者应按上游 LICENSE 与各官方平台的服务条款在 24 小时内**自行清除**。

词语约定：

- “**本项目**” 指 **Go Music Tag Web**（本仓库）。
- “**原项目 / upstream**” 指 [`xhongc/music-tag-web`](https://github.com/xhongc/music-tag-web) ，即本仓库的 fork 源。
- “**使用者**” 指签署本协议 / 使用本项目的所有方。
- “**官方音乐平台**” 指对本项目内置的网易云音乐、酷狗音乐、酷我音乐、QQ 音乐、咪咕音乐、MusicBrainz 等音乐源的官方平台统称。
- “**版权数据**” 指包括但不限于图像、音频、名字、歌词等在内的他人拥有所属版权的数据。

由于使用本项目产生的、包括由于本协议或由于使用或无法使用本项目而引起的任何性质的任何直接、间接、特殊、偶然或结果性损害（包括但不限于因商誉损失、停工、计算机故障或故障引起的损害赔偿，或任何及所有其他商业损害或损失），**由使用者自行承担**。

本项目完全免费，仅供个人私下小范围研究交流学习使用，开源发布于 GitHub 面向全世界使用者作为技术学习交流。本项目不对项目内的技术可能存在违反当地法律法规的行为作保证；禁止在违反当地法律法规的情况下使用本项目；对于使用者在明知或不知当地法律法规不允许的情况下使用本项目所造成的任何违法违规行为，由使用者承担，本项目不承担由此造成的任何直接、间接、特殊、偶然或结果性责任。

**若你使用了本项目，将代表你接受以上协议。**
