# Security Guide

This document explains how the Go backend enforces a hardened baseline by
default and what every operator must set in production. It maps directly
to the audit in `P1.5.md#F`.

---

## TL;DR

Before shipping to production, set:

```
JWT_SECRET=$(openssl rand -base64 48)
ADMIN_USERS='alice:$(openssl rand -base64 32),bob:$(openssl rand -base64 32)'
CORS_ALLOWED_ORIGINS='https://music.example.com'
GRPC_USE_TLS=1
GRPC_TLS_CA_FILE=/etc/music-tag/ca.pem   # optional; omit to use system roots
```

Do **not** set `ALLOW_INSECURE_DEFAULTS` in any non-local deployment.

## What each control does

### JWT_SECRET

- Pre-P1: defaulted to `"change-me-in-production"` if env was unset.
- Now: `config.Load()` refuses to start (log.Fatalf) when JWT_SECRET is
  empty or equal to that placeholder, unless `ALLOW_INSECURE_DEFAULTS=1`
  is set. Login/Refresh/Verify also reject explicitly when the value is
  the placeholder, so dev defaults don't leak tokens.

> Note: `ADMIN_SUBSONIC_TOKENS` was removed when Subsonic REST (`/rest/…`)
> was retired in P2.0. Subsonic salted-token auth is no longer supported —
> operators that still need Subsonic client compatibility should run a
> dedicated Navidrome/Funkwhale alongside, gated by the JWT frontend.

### ADMIN_USERS

- Pre-P1: had a hardcoded `admin:admin` fallback regardless of env.
- Now: if `ADMIN_USERS` is unset and `ALLOW_INSECURE_DEFAULTS!=1`,
  `handler.Login` returns 401 for every credential — fail-closed.

### CORS_ALLOWED_ORIGINS

- Pre-P1: `Access-Control-Allow-Origin` echoed any caller, paired with
  `Access-Control-Allow-Credentials: true`.
- Now: only listed origins get the headers + `Vary: Origin` so caches
  stay correct. Empty list = no cross-origin at all (browsers refuse).

### GRPC_USE_TLS

- Pre-P1: every gRPC dial used `insecure.NewCredentials()`.
- Now: set `GRPC_USE_TLS=1` and the dial uses TLS. Optional
  `GRPC_TLS_CA_FILE` adds your private CA to the trust pool. When
  `ALLOW_INSECURE_DEFAULTS!=1` and TLS is off, a loud warning is
  printed at startup (the binary still runs for backward compat with
  docker-compose dev).

### SSRF defence

`fetchRemoteBytes` (cover image download in `POST /api/update_id3/`)
now runs the URL through `internal/netguard.Guard`:

- only `http` / `https` accepted (`file://`, `gopher://`, `javascript:`
  all rejected)
- host must resolve
- every resolved IP must satisfy `isPublic` (rejects loopback, v4/v6
  private, link-local, multicast, AWS metadata `169.254.169.254`)

> DNS rebinding is *not* in the v1 mitigation scope. Recommended
> supplementary defences: front the gateway with an HTTP proxy that
> re-resolves + blocks RFC1918 ranges, or override
> `netguard.Guard.Resolver` to pin resolved IPs at dial time.

### Path traversal defence

`internal/utils.SafeJoin(root, p)` returns the resolved path only if it
actually stays under `root`. All file-touching handlers (`/api/file_list`,
`/api/music_id3`, `/api/update_id3`, `/api/batch_update_id3`) now root
user-supplied paths under `MUSIC_DIR` via this helper; `"."`, `".."`,
absolute paths pointing elsewhere all return `Failure(c, "路径不安全")`.

### yt-dlp parameter injection

`POST /api/youtube_download/` accepts an `extra_audio_format` hint.
`internal/tasks/yt_dlp_validate.go` sanitises three fields:

- `format` — regex `^[a-zA-Z0-9_./+<>:=]{1,64}$`, refuses `-`-prefixed
- `output_format` — closed enum `mp3 | m4a | ogg | vorbis | wav | "" `
- `quality` — `^[0-9]{1,4}$`

Both the gateway (`handler.YoutubeDownload`) and the worker
(`tasks.YouTubeDownloadHandler.ProcessTask`) revalidate; defence-in-depth
protects against task payload replay and DB tampering.

---

## Threat-model check (post-fix)

| Vector | Status |
|---|---|
| Cross-origin from enemy.com | Blocked by CORS whitelist |
| Frontend→backend SSRF to AWS metadata | netguard rejects 169.254 |
| Frontend→backend SSRF to internal Redis (private 10/8) | netguard rejects |
| Frontend file traversal in `/api/file_list` via `../` | SafeJoin rejects |
| Frontend path injection in `/api/update_id3` | SafeJoin rejects, also rewrites rename target |
| Worker `exec.CommandContext(yt-dlp)` with `--exec=…` | yt_dl sanitize rejects |
| Unauthenticated `/api/token/` with `admin:admin` | fails-closed outside dev mode |
| Forged JWT with placeholder secret | `Login` rejects ref-issue; `Verify` rejects ref-check |
| gRPC MITM of `Search`/`FetchID3ByTitle` responses | flag in TLS handshake |
| DNS rebinding (SSRF that resolves private at dial time) | out of scope (see SSRF notes) |
| mTLS for plugin clients | out of scope (server-side TLS only today) |

---

## Operator runbook

### Enabling TLS for gRPC plugins

1. Generate or reuse a private CA plus per-plugin server certs.
2. Mount the CA bundle into both gateway and worker containers at
   `/etc/music-tag/ca.pem`.
3. Set `GRPC_USE_TLS=1` and `GRPC_TLS_CA_FILE=/etc/music-tag/ca.pem`
   in the compose env. The same `ca.pem` must be trusted by every
   plugin server's TLS certificate.
4. Verify:

   ```bash
   openssl s_client -connect plugin-host:50051 -CAfile ca.pem
   ```

   Each plugin logs `[gateway] gRPC plugins: TLS enabled (CA=…)`.

### Vestigial warnings explained

`config.Load()` may print messages that look fatal but aren't:

- "WARNING: ADMIN_USERS unset" — login will return 401 (callers wanting
  to migrate older Subsonic-compatible payloads should run a dedicated
  bridge server alongside the gateway).
- "WARNING: gRPC plugins connect via insecure credentials" — operators
  must explicitly opt in via `ALLOW_INSECURE_DEFAULTS=1` to even see
  this; otherwise the placeholder-JWT check has already fatal'd.

Only the FATAL log line stops the binary:

```
[config] FATAL: JWT_SECRET is unset or equal to the placeholder;
refusing to start. Set JWT_SECRET to a strong value (e.g.
`openssl rand -base64 48`) or set ALLOW_INSECURE_DEFAULTS=1
for explicit local dev.
```
