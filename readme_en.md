![](img_6.jpg)

# 🚀 Music Tag Web

[简体中文](README.md) | [English](readme_en.md)

<a href="https://hellogithub.com/repository/d1919a26b74b40f19240da9f2ee3f7a3" target="_blank"><img src="https://abroad.hellogithub.com/v1/widgets/recommend.svg?rid=d1919a26b74b40f19240da9f2ee3f7a3&claim_uid=JQPHiFh3t5mqG1M" alt="Featured｜HelloGitHub" style="width: 250px; height: 54px;" width="250" height="54" /></a>
![star](https://atomgit.com/xhongc/music-tag-web/star/badge.svg)

Music Tag Web is a web-based music metadata editor that can edit song title, album, artist, lyrics, cover art, and more.
It supports FLAC, APE, WAV, AIFF, WV, TTA, MP3, M4A, OGG, MPC, OPUS, WMA, DSF, MP4 and other audio formats.

<div class="column" align="middle">
    <a href="https://go.dev/dl/"><img src="https://img.shields.io/badge/Go-1.23-00ADD8.svg" alt=""></a>
   <img src="https://img.shields.io/github/stars/xhongc/music-tag-web?color=informational&label=Stars">
  <img src="https://img.shields.io/badge/V2-backend-Go%20%2B%20gRPC-blueviolet?style=plastic" alt="V2 stack" />
  <img src="https://img.shields.io/badge/platform-amd64/arm64-pink?style=plastic" alt="docker-platform" />
</div>

> ⚠️ The source repo now tracks the **Go backend** (`cmd/` + `internal/` at project root, with seven music-source gRPC plugins). The Docker Hub image `xhongc/music_tag_web:latest` is the legacy **Python/Django build** — kept for existing users but no longer maintained. New deployments should use the V2 source-build flow below.

# 🎉 Features

Why a web version? When using Navidrome, my music library is stored on a remote server. Desktop tools like MusicTag and mp3tag cannot satisfy remote editing needs. I need a sidecar app that can run on the server and edit online music tags directly.

- View, edit, and modify metadata for most audio formats
- Batch auto-tagging (scraping) support
- Acoustic fingerprint recognition (identify songs even without metadata)
- Music file organization by artist/album or custom multi-level grouping
- File sorting by filename, size, and modified time
- Batch conversion between Traditional Chinese and Simplified Chinese metadata
- Filename parsing/unpacking to fill missing metadata
- Batch text replacement for metadata cleanup
- Audio format conversion via ffmpeg
- Whole-track cutting/splitting support
- Multiple metadata sources
- Lyric translation support
- Operation log display
- Export album cover files and upload custom album covers
- Mobile-friendly UI with phone access support
- Xiaomi XiaoAI local/NAS playback support
- Cloud drive music playback
- Playback statistics with bar/line chart visualization

# 🦀 Project Demo

Demo account: `admin/admin`

[【音乐标签Web｜Music Tag Web】](http://117.72.222.188:8002/#/)

# 💯 How to Use

[【User Guide】](https://xiers-organization.gitbook.io/music-tag-web/)

[【User Guide V2】](https://xiers-organization.gitbook.io/music-tag-web-v2/)

Use the V2 guide for V2 deployment.

## V1 Deployment

The image is available on Docker Hub.

### 1. Pull image

```bash
docker pull xhongc/music_tag_web:latest
```

### 2. Run container

```bash
docker run -d -p 8001:8001 -v /path/to/your/music:/app/media -v /path/to/your/config:/app/data --restart=always xhongc/music_tag_web:latest
```

Or deploy with Portainer Stacks (docker compose):

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

Set `/path/to/your/music` to your music directory and `/path/to/your/config` to your config directory.

3. Visit `127.0.0.1:8001/admin`.
Default account/password: `admin/admin`. Change the default password after login.

![img_7.png](img_7.png)

## V2 Deployment (recommended — Go + gRPC plugins)

> The V1 Docker Hub image no longer matches this repository (the image is still Django; the repo source has moved to Go). All new deployments should build from source.

> 🪜 **Upgrading from a previous revision? Skip this if you are fresh-installing.** The new compose defaults resolve relative to the repo root, so a bare `git pull && docker compose up -d` rebuilds an empty sqlite and points nginx at a non-existent directory. The four lines below work at any point — `gobackend/{.env,data,music}` still exist on disk even after the new layout goes live; they are just no longer git-tracked.
>
> Skip line 3 (`mv gobackend/music`) if your *old* `.env` set `MUSIC_DIR=` to an external bind mount (Synology `/volume1/music`, SMB/NFS at `/mnt/nas/music`, etc.) — in that case edit `./env` so `MUSIC_DIR=` continues to point at the same external path.
>
> If `./env`, `./data`, or `./music` already exist at the root (because something already ran `cp .env.example .env` first, or `docker compose up` provisioned an empty bind), back them up before `mv` to avoid silent overwrite:
> ```bash
> mv gobackend/.env  ./.env   # back up first if ./.env already exists
> mv gobackend/data  ./data   # back up first if ./data already exists
> mv gobackend/music ./music  # only if you were using ./gobackend/music as a local bind; skip for NAS mounts
> rmdir gobackend             # safe: rmdir itself refuses non-empty
> ```
> Then `docker compose up -d --build`. `${MUSIC_DIR}` and `${DATA_DIR}` are still `./music` / `./data` so semantics don't change.

### 1. Clone and prepare env

```bash
git clone https://github.com/xhongc/music-tag-web.git
cd music-tag-web                 # .env + Makefile + Dockerfiles all live at the repo root
cp .env.example .env             # copy next to the compose file
# then edit .env and fill in the required values
```

> ⚠️ **Pre-flight: read the boilerplate.** Before starting step 2, open `.env.example` and read the `⛔ PRE-FLIGHT CHECKLIST ⛔` block at the top. The three `__REPLACE_ME__` sentinels (`JWT_SECRET`, `ADMIN_USERS`, `WEBHOOK_INTERNAL_TOKEN`) will be rejected at startup by `config.Load()` with a `log.Fatalf` unless `ALLOW_INSECURE_DEFAULTS=1` is set (in which case it only logs a WARNING).

`JWT_SECRET` and `ADMIN_USERS` are **required** (the gateway either fails
to start or refuses every login without them). Recommended:
`CORS_ALLOWED_ORIGINS`, `GRPC_USE_TLS`, and the volume/port variables
`MUSIC_DIR`, `DATA_DIR`, `NGINX_PORT`.

> Each variable in `.env.example` has an inline comment; full operator
> guidance lives in `SECURITY.md` and `P1.5.md`.

### 2. Volume pre-flight + bring up the full stack

The compose defaults `./music` and `./data` are **relative to the compose file**, so the host directories must exist before `docker compose up`; otherwise you get `volume source not found`. NAS operators with SMB/NFS mounts — prepare the host paths first.

```bash
mkdir -p ./music ./data         # first run only (relative to the repo root)
docker compose up -d --build    # --build only when plugins or env change
```

The first run compiles: gateway + worker + 7 gRPC music-source plugins (netease / kugou / kuwo / migu / qmusic / musicbrainz / acoustid) + redis + nginx.

### 3. Open the UI

`http://localhost:9150/admin` — the outer nginx listens on `NGINX_PORT`; the gateway itself listens on `8001`.

> Login credentials come from `ADMIN_USERS`. The `admin/admin` fallback only applies when `ALLOW_INSECURE_DEFAULTS=1` (dev only). Production deployments must remove that env var and set a real password.

# 📷 User Interface (V2)

![img_13.png](img_13.png)
![img_15.png](img_15.png)
![img_16.png](img_16.png)
![img_17.png](img_17.png)
![img_18.png](img_18.png)
![img_19.png](img_19.png)
![img_12.png](img_12.png)

# 💬 Contact

If you have suggestions or feature requests, please star the project first and then open an issue.
If your issue is not seen in time, you can join the group chats (or add WeChat: `charlesnowed`, note: `Music Tag`, and I can invite you to the group).

<div>
<img  src="/img0302.jpg" width="250">  &nbsp;
</div>

## Channels

[t.me/music_tag_web](https://t.me/music_tag_web)

[MusicTag Telegram Group](https://t.me/+oTffyBoNALM3Yzll)

QQ Group: 55893996 (Group 1 full)

QQ Group: 79502786 (Group 2)

# 💸 Sponsor

If music-tag-web helps you, you can support the author.
Your support helps keep updates coming. Thank you.

[➡ 爱发电](https://afdian.com/a/music-tag-web)

# 🌟 Star History

[![Star History Chart](https://api.star-history.com/svg?repos=xhongc/music-tag-web&type=Date)](https://star-history.com/#xhongc/music-tag-web&Date)

# Disclaimer

Any commercial use is prohibited, including but not limited to selling, monetizing, tipping, or profit-making use.
This project is for personal technical research and learning only, and does not provide music downloads.

This project is distributed under GPL V3.0. The terms below supplement GPL V3.0. If there is any conflict, the terms below prevail.

Definitions used in this agreement:
- “This project” means `music-tag-web`
- “User” means anyone who uses this project
- “Official music platforms” means the built-in sources, including but not limited to Kuwo, NetEase Cloud Music, QQ Music, Migu, Kugou, etc.
- “Copyrighted data” includes but is not limited to images, audio, names, lyrics, and other copyrighted content

Data used by this project is fetched from publicly accessible servers of official music platforms, then filtered and merged for display. Therefore, this project does not guarantee data accuracy.

Using this project may generate copyrighted data. This project does not own such data. To avoid infringement, users must delete copyrighted data generated during usage within 24 hours.

Some resources used in this project (including but not limited to fonts and images) come from the Internet. If there is infringement, please contact us for removal.

Any direct, indirect, special, incidental, or consequential damages resulting from use or inability to use this project are the sole responsibility of the user, including but not limited to loss of goodwill, work stoppage, computer failure, and other business losses.

This project is completely free and open source on GitHub for technical learning and communication. It does not guarantee compliance with local laws in every jurisdiction. Using this project in violation of local laws is prohibited, and all related legal responsibilities are borne by the user.

By using this project, you acknowledge and accept the terms above.
