package router

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"go-music-tag/internal/config"
	"go-music-tag/internal/gateway/handler"
	"go-music-tag/internal/gateway/middleware"
)

// Setup configures all routes matching the Python backend's URL structure.
// `gormDB` may be nil — when nil, JWT login falls back to env-driven
// (ADMIN_USERS) credentials and any DB-backed handler returns the
// configured error envelope.
func Setup(r *gin.Engine, cfg *config.Config, gormDB *gorm.DB) {
	// --- Public ---
	// CORS now reads cfg.CORSAllowedOrigins rather than reflecting every
	// Origin (P1.5 issue F). Empty whitelist => browsers silently deny.
	r.Use(middleware.CORS(cfg))

	// Healthcheck (also used by Docker healthcheck). Keep this before the
	// SPA fallback so /admin/login/ stays a lightweight 200 probe and is
	// not rewritten into index.html.
	r.GET("/admin/login/", func(c *gin.Context) { c.Status(200) })

	// JWT endpoints (public)
	r.POST("/api/token/", handler.Login)
	r.POST("/api/token/refresh/", handler.RefreshToken)
	r.POST("/api/token/verify/", handler.VerifyToken)

	// Internal webhooks are mounted under /api/webhooks/* below alongside
	// the JWT-protected routes (same /api prefix, different middleware
	// stack — BodyLimit + WebhookAuth vs BodyLimit + JWTAuth). We keep the
	// same prefix so reverse proxies can apply a single rate-limit policy.

	// --- /api routes ---
	// P1.5 issue F (M4): body limit on /api (8 MiB by default) keeps
	// a single oversized upload from OOM-ing the gateway. The webhook
	// and JWT routes use the same limit so a future endpoint cannot
	// accidentally bypass it.
	api := r.Group("/api")
	api.Use(middleware.BodyLimit(0))

	// Internal webhooks — gated on a shared secret. The body-limit
	// applies first so the token-check itself cannot be used as a memory
	// DoS vector by sending an unbounded payload before auth.
	api.POST("/webhooks/file_moved/", middleware.WebhookAuth(cfg), handler.FileMovedWebhook)

	// --- Authenticated ---
	authed := api.Group("")
	authed.Use(middleware.JWTAuth(cfg))
	{
		// Task endpoints — action-based routing like DRF @action
		// Python: /api/<action>/
		authed.POST("/file_list/", handler.FileList)
		// Server-side tag read for hydrate / openEditor (batch-add path).
		// Frontend posts {file_path, file_name}; handler SafeJoins under MUSIC_DIR.
		authed.POST("/music_id3/", handler.MusicID3)
		authed.POST("/update_id3/", handler.UpdateID3)
		authed.POST("/batch_update_id3/", handler.BatchUpdateID3)
		authed.POST("/batch_auto_update_id3/", handler.BatchAutoUpdateID3)
		authed.POST("/fetch_id3_by_title/", handler.FetchID3ByTitle)
		authed.POST("/fetch_lyric/", handler.FetchLyric)
		authed.POST("/tidy_folder/", handler.TidyFolder)
		authed.POST("/upload_image/", handler.UploadImage)
		// /api/search_music/ is the unified search endpoint for all
		// registered tag- and download-source plugins (youtube search
		// is already handled inside SearchMusic via DownloadSource.Search
		// — no separate /youtube_search/ route exists anymore).
		authed.POST("/search_music/", handler.SearchMusic)
		// /api/download/ is the unified download endpoint for any
		// registered DownloadSource. The client supplies `source` in
		// the request body; the handler routes to the matching plugin.
		authed.POST("/download/", handler.Download)
		// /api/stream/ proxies audio playback for two dispatch paths:
		//   (a) DownloadSource (e.g. youtube): ServeFile from the per-source
		//     cache dir, with enqueue + 10s long-poll on miss. Add
		//     ?as_attachment=1 to force a `Content-Disposition: attachment`
		//     header so the browser saves the file to Downloads/ (the
		//     row-level "下载到浏览器" button path).
		//   (b) TagSource (e.g. netease): proxies the upstream audio URL
		//     resolved via the plugin's GetAudioURL RPC, with Range /
		//     User-Agent / Cookie passthrough.
		// GET/HEAD /api/stream/ — HEAD is used by waitForStreamReady as a
		// cheap warm-cache probe (no Range / no body). Same handler.
		authed.GET("/stream/", handler.StreamAudio)
		authed.HEAD("/stream/", handler.StreamAudio)
		// GET /api/sources/ — Stage A of docs/plugable-plugins.md: exposes
		// every registered tag- + download-source so the frontend can drive
		// its source picker dynamically. Replaces the legacy hardcoded
		// `SEARCH_SOURCES` constant on the frontend and the `sourcesDefault`
		// constant in handler/tag.go.
		authed.GET("/sources/", handler.ListSources)
		// POST /api/sources/refresh/ — Stage B of plugable-plugins: reloads
		// data/sources/*.yaml and applies overrides to all registered
		// tag-source plugins (SetSecret + SetAPIBase). Admin-only by JWT
		// chain; on a self-host single-user setup the authed user is
		// implicitly an admin.
		authed.POST("/sources/refresh/", handler.RefreshSourceOverrides)
		// GET /api/sources/override/ — read-only view of the last-applied
		// overrides with secrets redacted (presence-only). Used by the
		// SettingsModal "Sources" tab.
		authed.GET("/sources/override/", handler.GetSourceOverride)
		// C.2 Filename Parse preview/apply round-trip:
		//   POST /preview  → returns token + per-row (artist/title/status).
		//   POST /apply   → consumes token, enqueues TypeApplyParsedFilenames.
		// Token TTL is enforced inside cache.DefaultPreviewCache.Load;
		// expired/unknown tokens surface as 401 "preview_expired" so
		// the modal can show a clean "re-preview" prompt.
		authed.POST("/tag/preview_parse_filenames/", handler.PreviewParseFilenames)
		authed.POST("/tag/apply_parsed_filenames/", handler.ApplyParsedFilenames)
		authed.GET("/clear_celery/", handler.ClearAsyncTasks)
		authed.GET("/active_queue/", handler.ActiveQueue)
		authed.GET("/task1/", handler.TaskScan)
		authed.GET("/task2/", handler.TaskClear)
		authed.GET("/full_scan_folder/", handler.FullScanFolder)
		// Task record list
		authed.GET("/record/", handler.ListTaskRecords)
		// Operation history audit log endpoints
		authed.GET("/operation_logs/", handler.ListOperationLogs)
		authed.POST("/operation_logs/clear/", handler.ClearOperationLogs)
	}

	// --- Static media + SPA (public) ---
	//
	// /media/* is intentionally NOT JWT-gated:
	//   - Browser <audio src="/media/..."> cannot attach Authorization.
	//   - SECURITY.md documents http.Dir(MUSIC_DIR) as the Range server
	//     for local library playback and browser-side id3Reader Range
	//     fetches (id3Reader still *may* send JWT; it is ignored here).
	// http.Dir normalizes ".." so path traversal out of MusicDir fails.
	mediaRoot := cfg.MusicDir
	if mediaRoot == "" {
		mediaRoot = "/app/media"
	}
	r.StaticFS("/media", http.Dir(mediaRoot))

	// Vite production assets. The SPA is BAKED INTO THE IMAGE by
	// Dockerfile.gateway's `frontend` stage (`npm run build`, then
	// COPY --from=frontend /app/static/dist) — there is deliberately
	// NO ./static bind mount in docker-compose.yml, because a host
	// mount would shadow the baked bundle with an empty directory and
	// make `/` return "SPA not built".
	//
	// staticRoot stays overridable via STATIC_DIR only for local `go
	// run` / non-container use, where the operator builds the frontend
	// by hand into ./static. vite's base is '/static/dist/', so
	// index.html and the hashed assets are served from /static/dist/*.
	staticRoot := os.Getenv("STATIC_DIR")
	if staticRoot == "" {
		staticRoot = "/app/static"
	}
	r.StaticFS("/static", http.Dir(staticRoot))

	spaIndex := filepath.Join(staticRoot, "dist", "index.html")
	serveSPA := func(c *gin.Context) {
		if _, err := os.Stat(spaIndex); err != nil {
			c.String(http.StatusNotFound, "SPA not built: missing %s (run: cd frontend && npm run build)", spaIndex)
			return
		}
		// ServeFile re-reads disk each request so a host-side rebuild of
		// static/dist is picked up without restarting the gateway.
		http.ServeFile(c.Writer, c.Request, spaIndex)
	}

	// Canonical UI entry points used by README / health docs.
	// NOTE: do NOT register `/admin/*path` — it conflicts with the
	// existing `/admin/login/` healthcheck segment in gin's radix tree.
	// SPA deep links under /admin/... fall through to NoRoute below.
	r.GET("/", serveSPA)
	r.GET("/admin", serveSPA)

	// Client-side routing fallback. Never swallow /api/* misses — those
	// should stay JSON 404s so axios interceptors keep working.
	// Also never rewrite /media/* or /static/* misses into the SPA:
	// gin's StaticFS delegates missing files to NoRoute; id3Reader and
	// <audio> need a real 404, not index.html, when a path is absent.
	r.NoRoute(func(c *gin.Context) {
		path := c.Request.URL.Path
		if strings.HasPrefix(path, "/api/") || path == "/api" {
			c.JSON(http.StatusNotFound, gin.H{"detail": "not found"})
			return
		}
		if strings.HasPrefix(path, "/media/") || path == "/media" ||
			strings.HasPrefix(path, "/static/") || path == "/static" {
			c.Status(http.StatusNotFound)
			return
		}
		// Unmatched non-API paths (e.g. SPA deep links under /admin/...).
		serveSPA(c)
	})
}
