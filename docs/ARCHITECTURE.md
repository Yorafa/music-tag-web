# 架构

> 本文是 README「当前架构」一节的完整版。README 只留一张速览表 + 指向这里。

## 分层

| 层 | 实现 |
|---|---|
| HTTP 服务 | Go 1.25 + gin（gateway：API + React SPA 静态 + `/media/*` Range 流） |
| 异步任务 | asynq + Redis（worker） |
| 音乐源 | **gRPC 微服务**：7 个元数据源（netease / kugou / kuwo / migu / qmusic / musicbrainz / acoustid）+ 1 个下载源（youtube）——各自独立进程 |
| Tag I/O | `bogem/id3v2` + `dhowden/tag`（纯 Go 库，无 Python FFI） |
| 前端 | React 19 + Vite 7 + TypeScript + Tailwind 4 + shadcn/ui + Zustand |
| 鉴权 | JWT in-memory + bcrypt；fail-closed 默认值检测 |
| 加密 | gRPC TLS 可选（env `GRPC_USE_TLS=1` + 可选 `GRPC_TLS_CA_FILE`） |
| 部署 | 单条 `docker compose up -d --build` 拉起 gateway + worker + 8 gRPC plugin + redis，共 11 个容器 |
| Docker image | gateway 131 MB、worker 126 MB、6 个按曲名搜索的插件各 21.5 MB、acoustid 插件 115 MB、youtube 插件 199 MB（yt-dlp + ffmpeg） |

## 容器拓扑

一次 `docker compose up -d --build` 会创建 **11 个容器**：

```
gateway  worker  redis
netease  kugou  kuwo  migu  qmusic  musicbrainz  acoustid  youtube
```

另有 `fpcalc-base` 是 `scale: 0` 的**构建用服务**：镜像会构建，但不创建容器。nginx 早期是独立服务，v2 起已合并进 gateway，不再有 `nginx` service 或 `nginx.conf`。

插件里 7 个是**音乐元数据源**（走 `TagSource`），第 8 个是 **youtube 下载插件**（走 `DownloadSource`，所以不计入上面那个 7 元数据源列表）。

## 镜像体积：为什么是 560 MB

gateway / worker / acoustid / youtube 四个镜像共用一个 `fpcalc-base`（102 MB）基础镜像，其中 88 MB 是 chromaprint 包顺带拉进来的 ffmpeg 解码库。**那 88 MB 在磁盘上只存一份**，所以四个镜像不需要各自重复付一遍。

youtube 插件比其它三个大的那 ~30 MB，**并不是 ffmpeg 程序**（二进制本身只有 294 KB），而是 `apk add ffmpeg` 额外拖进来的 42 个包：

- `libavfilter`（4.4 MB）、`libavformat`、`libpostproc`
- `libswscale` / `libswresample` / `libavdevice`
- 一整条 video / 滤镜 / 硬件加速依赖：sdl2、vulkan-loader、libplacebo、shaderc、spirv-tools、glslang、harfbuzz、fontconfig、vidstab、alsa-lib、libpulse

yt-dlp 的 `--extract-audio` 只用到其中的音频解复用 + 重编码部分，video 侧的库用不上，但 Alpine 没有拆得更细的 ffmpeg 包可以单独去掉。这就是 youtube 选 `fpcalc-base` 而不是裸 alpine 的原因：从裸 alpine 起会**再存一份**全部 88 MB。

另外，ffmpeg 的四个核心库（libavcodec / libavformat / libavutil / libswresample）**删不掉**——`--extract-audio` 虽然只解码音频，ffmpeg 二进制硬链接了这四个，去掉任何一个都会让它拒绝启动。`libx265` 单独就 19 MB，而这条路径从不调用它，但 libavcodec.so NEEDS 它。

yt-dlp 本身用 pip 装并钉死版本（`YTDLP_VERSION` build arg，默认 `2026.8.19`），不用 Alpine 打包的冻结版——后者会破坏 YouTube 签名提取，且几乎每周坏一次。

## 相关文档

- [`docs/plugable-plugins.md`](plugable-plugins.md) —— 插件架构设计草图（Stage A 已落地 / B–D 未排期）
- [`SECURITY.md`](../SECURITY.md) —— 威胁模型与 runbook
- [`docs/DEPLOYMENT.md`](DEPLOYMENT.md) —— 部署与故障排查
- [`docs/DEVELOPMENT.md`](DEVELOPMENT.md) —— 本地开发与验证
- [`docs/FEATURE-COVERAGE.md`](FEATURE-COVERAGE.md) —— 功能覆盖矩阵
