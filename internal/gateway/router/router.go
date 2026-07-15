package router

import (
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

	// Healthcheck (also used by Docker healthcheck)
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
		authed.POST("/music_id3/", handler.MusicID3)
		authed.POST("/update_id3/", handler.UpdateID3)
		authed.POST("/batch_update_id3/", handler.BatchUpdateID3)
		authed.POST("/batch_auto_update_id3/", handler.BatchAutoUpdateID3)
		authed.POST("/fetch_id3_by_title/", handler.FetchID3ByTitle)
		authed.POST("/fetch_lyric/", handler.FetchLyric)
		authed.POST("/tidy_folder/", handler.TidyFolder)
		authed.POST("/upload_image/", handler.UploadImage)
		authed.POST("/youtube_search/", handler.YoutubeSearch)
		authed.POST("/youtube_download/", handler.YoutubeDownload)
		authed.POST("/search_music/", handler.SearchMusic)
		// GET /api/sources/ — Stage A of docs/plugable-plugins.md: exposes
		// every registered tag- + download-source so the frontend can drive
		// its source picker dynamically. Replaces the legacy hardcoded
		// `SEARCH_SOURCES` constant on the frontend and the `sourcesDefault`
		// constant in handler/tag.go.
		authed.GET("/sources/", handler.ListSources)
		authed.GET("/clear_celery/", handler.ClearAsyncTasks)
		authed.GET("/active_queue/", handler.ActiveQueue)
		authed.GET("/task1/", handler.TaskScan)
		authed.GET("/task2/", handler.TaskClear)
		authed.GET("/full_scan_folder/", handler.FullScanFolder)
		// Task record list
		authed.GET("/record/", handler.ListTaskRecords)
	}
}
