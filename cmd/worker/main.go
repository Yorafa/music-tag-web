// Worker binary: initializes DB + gRPC plugin clients + starts asynq server.
//
// Equivalent to running the Python Celery worker:
//
//	celery -A applications.task.tasks worker --loglevel=info --concurrency=4
//
// Usage:
//
//	REDIS_ADDR=localhost:6379 DB_DSN=/app/data/db.sqlite3 \
//	  GRPC_USE_TLS=1 GRPC_TLS_CA_FILE=/etc/music-tag/ca.pem \
//	  go run ./cmd/worker
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/hibiken/asynq"
	"gorm.io/gorm"

	"go-music-tag/internal/audit"
	"go-music-tag/internal/config"
	"go-music-tag/internal/db"
	"go-music-tag/internal/events"
	"go-music-tag/internal/plugin"
	"go-music-tag/internal/queue"
	"go-music-tag/internal/tasks"
)

func main() {
	concurrency := flag.Int("concurrency", 10, "asynq worker concurrency")
	queues := flag.String("queues", "critical,default,low", "comma-separated queue priority list")
	flag.Parse()

	cfg := config.LoadAtBoot()

	// 1) Connect to database (sqlite or mysql based on DB_DRIVER env).
	dbDriver := cfg.DBDriver
	if dbDriver == "sqlite3" {
		dbDriver = "sqlite"
	}
	gormDB, err := db.Open(db.Config{
		Driver: dbDriver,
		DSN:    cfg.DBDSN,
	})
	if err != nil {
		log.Fatalf("[worker] open DB (%s) failed: %v", dbDriver, err)
	}
	if err := db.AutoMigrate(gormDB); err != nil {
		log.Fatalf("[worker] auto-migrate failed: %v", err)
	}
	audit.SetDB(gormDB)
	log.Printf("[worker] DB ready (driver=%s)", dbDriver)

	// 2) Build gRPC plugin client registry. Each source name maps to a
	//    separate gRPC server (cmd/plugins/<name>/main.go). Plugin addresses
	//    are wired via PLUGIN_<NAME>_ADDR env vars consumed by config.LoadAtBoot().
	//    Music sources (netease/kugou/kuwo/migu/qmusic/musicbrainz) register
	//    as TagSource; youtube registers as DownloadSource.
	//
	//    SECURITY (P1.5 issue F): dialOpts is constructed from cfg.GRPCUseTLS
	//    / cfg.GRPCCAFile. With UseTLS=false the registry still works
	//    against insecure credentials (preserving docker-compose dev), but
	//    config.LoadAtBoot() will already have WARN'd loudly unless
	//    ALLOW_INSECURE_DEFAULTS=1.
	dialOpts := plugin.DialOptions{UseTLS: cfg.GRPCUseTLS, CAFile: cfg.GRPCCAFile}
	if cfg.GRPCUseTLS {
		log.Printf("[worker] gRPC plugins: TLS enabled (CA=%q)", cfg.GRPCCAFile)
	} else {
		log.Printf("[worker] WARNING: gRPC plugins connect insecurely; " +
			"set GRPC_USE_TLS=1 for production")
	}
	// IMPORTANT: do NOT call RegisterTagSource / RegisterDownloadSource with a
	// cold GRPC adapter. Register* holds the registry mutex then calls p.Name(),
	// and GRPC*.Name() dials + re-enters Register* / Get* → self-deadlock on
	// RWMutex (worker never reached "all 6 task handlers registered" / asynq
	// Run, so download:generic tasks sat forever in Redis pending).
	//
	// Match gateway: construct adapter, call Name() to lazy-dial + self-register
	// after the handshake. Failures leave the source unregistered (logged).
	downloadSources := map[string]bool{"youtube": true}
	registered := 0
	for src, addr := range cfg.PluginGRPCAddrs {
		if downloadSources[src] {
			ds := plugin.NewGRPCDownloadSource(addr, dialOpts)
			if name := ds.Name(); name == "" {
				log.Printf("[worker] WARNING: download plugin %s unreachable at %s", src, addr)
				continue
			}
		} else {
			ts := plugin.NewGRPCTagSource(addr, dialOpts)
			if name := ts.Name(); name == "" {
				log.Printf("[worker] WARNING: tag plugin %s unreachable at %s", src, addr)
				continue
			}
		}
		registered++
	}
	log.Printf("[worker] gRPC plugin registry initialized with %d sources (tag=%v download=%v)",
		registered, plugin.ListTagSources(), plugin.ListDownloadSources())

	// 3) Wire event bus (Redis pub/sub on the same REDIS_ADDR as asynq).
	//    Used by TidyFolderHandler to publish FileMoved events so the
	//    gateway can invalidate its PathCache. nil-safe — handlers
	//    default to no-publish if Bus is unset.
	bus := events.NewRedisBus(cfg.RedisAddr)
	defer func() {
		if err := bus.Close(); err != nil {
			log.Printf("[worker] bus close err: %v", err)
		}
	}()
	log.Printf("[worker] event bus ready (addr=%s)", cfg.RedisAddr)

	// 4) Wire handlers.
	mux := asynq.NewServeMux()
	wireTaskHandlers(mux, taskHandlerDeps{
		DB:        gormDB,
		DBDriver:  dbDriver,
		MusicRoot: cfg.MusicDir,
		Bus:       bus,
	})

	// 4) Start asynq server.
	asynqCfg := queue.ServerConfig()
	asynqCfg.Concurrency = *concurrency
	asynqCfg.Queues = parseQueues(*queues)
	srv := asynq.NewServer(queue.ServerOpts(), asynqCfg)

	// 5) Graceful shutdown: trap SIGTERM/SIGINT, give in-flight 10s, then stop.
	ctx, cancel := context.WithCancel(context.Background())
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		s := <-sigCh
		log.Printf("[worker] received %v, initiating graceful shutdown (10s)", s)
		cancel()
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer shutdownCancel()
		_ = shutdownCtx
		srv.Shutdown()
	}()

	log.Printf("[worker] starting asynq, concurrency=%d queues=%v", *concurrency, *queues)
	if err := srv.Run(mux); err != nil {
		log.Fatalf("[worker] asynq terminated with error: %v", err)
	}
	log.Printf("[worker] stopped cleanly")
	_ = ctx
	fmt.Println("bye")
}

func parseQueues(s string) map[string]int {
	m := make(map[string]int)
	cur := 10
	for _, raw := range strings.Split(s, ",") {
		name := strings.TrimSpace(raw)
		if name == "" {
			continue
		}
		m[name] = cur
		cur -= 2
		if cur < 1 {
			cur = 1
		}
	}
	return m
}

// taskHandlerDeps is everything the handler constructors need. Bundling it
// keeps wireTaskHandlers callable from a test with nils, which is what lets
// TestWireTaskHandlers resolve every type against a real ServeMux.
type taskHandlerDeps struct {
	DB        *gorm.DB
	DBDriver  string
	MusicRoot string
	Bus       events.Bus
}

// wireTaskHandlers registers every task type this worker consumes.
//
// The registrations live in a slice rather than a run of bare calls so the
// startup log can name the types it consumed. A hand-written "all N task
// handlers" drifts the moment one is added or removed — and it had: the log
// claimed 8 while 7 were wired. A claimed count also cannot answer the only
// question that matters at boot, "is the type the gateway enqueues actually
// consumed here?", which is the question this codebase got wrong twice
// (TypeApplyParsedFilenames, and the tag:batch_auto chain).
func wireTaskHandlers(mux *asynq.ServeMux, d taskHandlerDeps) {
	gormDB, dbDriver, bus, musicRoot := d.DB, d.DBDriver, d.Bus, d.MusicRoot
	registrations := []struct {
		typename string
		wire     func()
	}{
		{tasks.TypeFullScanFolder, func() {
			tasks.NewFullScanMux(mux, &tasks.FullScanHandler{DB: gormDB, MusicRoot: musicRoot})
		}},
		{tasks.TypeUpdateScanFolder, func() {
			tasks.NewUpdateScanMux(mux, &tasks.UpdateScanHandler{DB: gormDB, MusicRoot: musicRoot})
		}},
		{tasks.TypeTidyFolder, func() {
			tasks.NewTidyFolderMux(mux, &tasks.TidyFolderHandler{
				DB:        gormDB,
				MusicRoot: musicRoot,
				Bus:       bus,
			})
		}},
		// Unified download handler — all download enqueues go through
		// download:generic; payload.Source dispatches to the matching branch.
		// The youtube/yt-dlp exec lives in the youtube plugin (reached over
		// gRPC via plugin.GetDownloadSource); the worker only orchestrates
		// the cache/library copy + DB records, so this image needs no
		// python / yt-dlp / ffmpeg.
		{tasks.TypeDownloadGeneric, func() {
			tasks.NewDownloadGenericMux(mux, tasks.NewDownloadHandler(gormDB, musicRoot))
		}},
		{tasks.TypeClearMusic, func() {
			tasks.NewClearMusicMux(mux, &tasks.ClearMusicHandler{DB: gormDB, DBDriver: dbDriver})
		}},
		{tasks.TypePruneEmptyFolders, func() {
			tasks.NewPruneEmptyFoldersMux(mux, &tasks.PruneEmptyFoldersHandler{
				DB:        gormDB,
				MusicRoot: musicRoot,
			})
		}},
		// C.2 filename-parse bulk-apply worker (tag:apply_parsed_filenames).
		// HandleApplyParsedFilenames is a dependency-free function over the
		// payload, so it wires as a HandlerFunc. This registration was missing
		// until round-11: the gateway enqueued TypeApplyParsedFilenames but no
		// consumer existed, so apply tasks retried (MaxRetry=3) and landed in
		// asynq's archived dead-letter queue — the parsed names were never
		// written to tags. See docs/plans/Unfinished-Features.md § C.2.
		{tasks.TypeApplyParsedFilenames, func() {
			tasks.NewApplyParsedFilenamesMux(mux, tasks.HandlerFunc(tasks.HandleApplyParsedFilenames))
		}},
	}
	for _, r := range registrations {
		r.wire()
	}
	names := make([]string, 0, len(registrations))
	for _, r := range registrations {
		names = append(names, r.typename)
	}
	log.Printf("[worker] all %d task handlers registered: %s",
		len(registrations), strings.Join(names, ", "))
}
