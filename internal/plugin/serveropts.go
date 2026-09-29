package plugin

import (
	"log"
	"net/http"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/keepalive"
)

// Server-side gRPC defaults shared by all eight plugin binaries.
// //
// Why this exists: the client (internal/plugin/grpc_adapter.go) sets
// keepalive.ClientParameters{Time: 30s, PermitWithoutStream: true}, but
// every plugin main called a bare grpc.NewServer(). gRPC servers default
// to EnforcementPolicy.MinTime = 5 minutes and PermitWithoutStream =
// false, so a well-behaved 30s client ping is treated as a protocol
// violation: the server sends GOAWAY with ENHANCE_YOUR_CALM and drops
// the connection.
//
// That is the likely real cause of the "long-lived channel goes Idle →
// first call returns Unavailable + EOF" symptom the adapter comments
// describe. Adding client-side keepalive made it worse, not better: the
// fix belongs on the server.
//
// Centralised in one function so the eight mains cannot drift — a plugin
// added later gets the correct policy by calling the same constructor.
const (
	// serverKeepaliveMinTime is the minimum client-ping spacing we will
	// tolerate. 10s sits comfortably above the client's 30s cadence while
	// staying far below the 5-minute default that caused the disconnects.
	serverKeepaliveMinTime = 10 * time.Second

	// serverKeepaliveMaxIdle lets a quiet plugin connection be reaped by
	// the client without the server forcing GOAWAY underneath it.
	serverKeepaliveMaxIdle = 5 * time.Minute

	// maxRecvMsgSize caps a single inbound message. Plugin RPCs carry
	// search results and short metadata; nothing legitimately approaches
	// 4 MiB. Without a cap, a malformed or hostile peer can allocate
	// arbitrarily per request.
	maxRecvMsgSize = 4 << 20

	// maxSendMsgSize mirrors the receive cap so a pathological upstream
	// response cannot be buffered without bound on the way out.
	maxSendMsgSize = 16 << 20
)

// NewGRPCServer returns a *grpc.Server configured with the shared
// keepalive and message-size policy. Use this in every plugin main
// instead of calling grpc.NewServer() directly:
//
//	srv := plugin.NewGRPCServer()
func NewGRPCServer(opts ...grpc.ServerOption) *grpc.Server {
	base := []grpc.ServerOption{
		// The load-bearing fix: without this the server kills the
		// client's 30s keepalive pings.
		grpc.KeepaliveEnforcementPolicy(keepalive.EnforcementPolicy{
			MinTime:             serverKeepaliveMinTime,
			PermitWithoutStream: true,
		}),
		// Advise clients to ping at the cadence we actually accept, and
		// re-ACK pings more eagerly than the 2-minute default so a busy
		// long-lived scrape stream is not torn down mid-call.
		grpc.KeepaliveParams(keepalive.ServerParameters{
			Time:    30 * time.Second,
			Timeout: 10 * time.Second,
		}),
		grpc.MaxRecvMsgSize(maxRecvMsgSize),
		grpc.MaxSendMsgSize(maxSendMsgSize),
	}
	return grpc.NewServer(append(base, opts...)...)
}

// StartHealthServer exposes a plain-HTTP 200 on /healthz for the lifetime
// of the process.
//
// Plugin containers have no healthcheck, so compose reports them "up" the
// instant the process exists — which is *before* Serve() is accepting, and
// with no signal at all if the plugin then panics on a bad config. The
// gateway dials each plugin once with a 5s blocking dial at boot; any
// plugin that misses that window is absent from the registry for the rest
// of the process lifetime, and ListTagSources() only reports registered
// keys, so nothing ever retries.
//
// A real /healthz plus `depends_on: service_healthy` gives compose a
// condition to wait on, so gateway/worker start after their plugins are
// genuinely serving.
//
// The listener is best-effort: if the health port is taken or unwritable
// the plugin still serves gRPC, because losing the health probe must not
// take the plugin down. Pass "" to disable.
//
// gRPC has no equivalent built-in probe, so this deliberately stays a
// separate HTTP listener rather than a gRPC health service — the health
// check has to be answerable without a gRPC client in the container.
func StartHealthServer(port string) {
	if port == "" {
		return
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok\n"))
	})
	srv := &http.Server{
		Addr:              ":" + port,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("[plugin] health server on :%s stopped: %v", port, err)
		}
	}()
	log.Printf("[plugin] health server listening on :%s/healthz", port)
}
