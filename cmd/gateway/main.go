// Gateway is the main HTTP API server. It serves the same REST API shape
// as the original Python/Django backend so the React frontend works unchanged.
//
// Usage:
//
//	GATEWAY_PORT=8001 JWT_SECRET=... PLUGIN_NETEASE_ADDR=localhost:50051 \
//	  CORS_ALLOWED_ORIGINS=https://music.example.com GRPC_USE_TLS=1 \
//	  go run ./cmd/gateway
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/gin-gonic/gin"

	"go-music-tag/internal/cache"
	"go-music-tag/internal/config"
	"go-music-tag/internal/db"
	"go-music-tag/internal/events"
	"go-music-tag/internal/gateway/handler"
	"go-music-tag/internal/gateway/router"
	"go-music-tag/internal/plugin"
)

func main() {
	cfg := config.Load()
	log.Printf("[gateway] starting on %s:%s", cfg.Host, cfg.Port)

	dialOpts := plugin.DialOptions{UseTLS: cfg.GRPCUseTLS, CAFile: cfg.GRPCCAFile}
	if cfg.GRPCUseTLS {
		log.Printf("[gateway] gRPC plugins: TLS enabled (CA=%q)", cfg.GRPCCAFile)
	} else if cfg.AllowInsecureDevDefaults {
		log.Printf("[gateway] WARNING: gRPC plugins connect via insecure credentials "+
			"(only because ALLOW_INSECURE_DEFAULTS=1). Set GRPC_USE_TLS=1 for production.")
	} else {
		// Should be unreachable because config.Load() would have fatal'd
		// already on the placeholder JWT_SECRET path; defensive log.
		log.Printf("[gateway] WARNING: gRPC plugins are insecure; set GRPC_USE_TLS=1")
	}

	// Connect to remote gRPC plugins (if configured).
	for name, addr := range cfg.PluginGRPCAddrs {
		if name == "youtube" {
			ds := plugin.NewGRPCDownloadSource(addr, dialOpts)
			if pluginName := ds.Name(); pluginName == "" {
				log.Printf("[gateway] WARNING: download plugin %s unreachable at %s", name, addr)
			}
		} else {
			ts := plugin.NewGRPCTagSource(addr, dialOpts)
			if pluginName := ts.Name(); pluginName == "" {
				log.Printf("[gateway] WARNING: tag plugin %s unreachable at %s", name, addr)
			}
		}
	}

	// Print registered plugins.
	log.Printf("[gateway] tag sources: %v", plugin.ListTagSources())
	log.Printf("[gateway] download sources: %v", plugin.ListDownloadSources())

	// Wire event bus + PathCache. The subscriber goroutine drains
	// events.TopicFileMoved into the cache so a freshly renamed file is
	// not served from a stale entry on the next request.
	bus := events.NewRedisBus(cfg.RedisAddr)
	defer func() {
		if err := bus.Close(); err != nil {
			log.Printf("[gateway] bus close err: %v", err)
		}
	}()
	pathCache := cache.NewPathCache()
	handler.SetCache(pathCache)
	handler.SetBus(bus)

	// Subscriber lifetime is bounded by rootCtx; on SIGTERM the OS
	// handler cancels, drains in-flight events, and exits cleanly.
	rootCtx, rootCancel := context.WithCancel(context.Background())
	defer rootCancel()
	subCancel := handler.StartSubscriber(rootCtx)
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		s := <-sigCh
		log.Printf("[gateway] received %v, shutting down", s)
		subCancel()
		rootCancel()
	}()
	log.Printf("[gateway] path cache + event bus ready")

	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Logger(), gin.Recovery())

	// Open the DB once at boot for the handler layer (auto-migrate is best-
	// effort). Note: Subsonic /rest/ has been removed; only /api/* + /admin/login/
	// /api/token/ endpoints remain, none of which require DB at boot.
	gormDB, dbErr := db.Open(db.Config{Driver: cfg.DBDriver, DSN: cfg.DBDSN})
	if dbErr != nil {
		log.Printf("[gateway] DB unavailable — handlers run in env-only mode: %v", dbErr)
		gormDB = nil
	} else if err := db.AutoMigrate(gormDB); err != nil {
		log.Printf("[gateway] auto-migrate failed: %v", err)
	}

	router.Setup(r, cfg, gormDB)

	addr := fmt.Sprintf("%s:%s", cfg.Host, cfg.Port)
	log.Printf("[gateway] listening on %s", addr)
	log.Printf("[gateway] CORS allowed origins: %v (empty = no cross-origin)", cfg.CORSAllowedOrigins)
	if err := r.Run(addr); err != nil {
		log.Fatalf("[gateway] server error: %v", err)
	}
}
