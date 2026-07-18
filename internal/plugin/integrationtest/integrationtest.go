// Package integrationtest provides a tiny, deliberately-uniform helper for
// thin grpc.NewServer + grpc.Dial integration tests across every music-tag
// plugin. The unit tests under <plugin>/server_test.go already exercise the
// Server struct directly; THIS package's purpose is to catch regressions in
// the *gRPC layer* — proto-field renumbering after regeneration, missing
// Register calls, transport framing, method-name drift — that the unit tests
// bypass entirely.
//
// Each plugin writes a single integration_test.go that calls integrationtest.Run
// with a RegisterTagSourceServer callback. The helper owns the bufconn
// listener, the grpc.Server, the dial, and the cleanup.
package integrationtest

import (
	"context"
	"net"
	"net/http"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	"go-music-tag/api/proto/tagplugin"
)

// NewListener returns a fresh bufconn listener with a 1 MiB max message size.
// Sized generously so plugins that ship larger JSON payloads (qmusic Search)
// won't be truncated by the in-memory transport.
func NewListener() *bufconn.Listener { return bufconn.Listen(1 << 20) }

// Dialer returns the bufconn dialer func for grpc.WithContextDialer.
func Dialer(l *bufconn.Listener) func(context.Context, string) (net.Conn, error) {
	return func(_ context.Context, _ string) (net.Conn, error) { return l.Dial() }
}

// Run stands up a grpc.Server, hands it to `register` for plugin-side wiring,
// serves on an in-memory bufconn listener, then dials back over that same
// listener and returns a tagplugin.TagSourceClient. Both the grpc.Server and
// the client connection are torn down via t.Cleanup.
//
// tests should NOT need to inspect dial errors or invoke Stop manually.
// The lazy-connect behavior of grpc.NewClient is safe here: bufconn's
// in-memory pipe is logically connected as soon as Listen() returns, so the
// first RPC's lazy dial succeeds even before the Serve goroutine has spun up.
func Run(t *testing.T, register func(*grpc.Server)) tagplugin.TagSourceClient {
	t.Helper()
	l := NewListener()
	s := grpc.NewServer()
	register(s)
	go func() { _ = s.Serve(l) }()
	t.Cleanup(func() { s.Stop(); _ = l.Close() })

	conn, err := grpc.NewClient(
		"passthrough://bufnet",
		grpc.WithContextDialer(Dialer(l)),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("grpc.NewClient: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return tagplugin.NewTagSourceClient(conn)
}

// ErrorRoundTripper is a minimal http.RoundTripper that always returns Err.
// Tests use it to simulate outbound plugin HTTP failures without doing a real
// network roundtrip — useful for proving that the gRPC layer correctly
// forwards the propagated-error contract: Server.Search returns
// status != OK with no body when the upstream transport drops.
type ErrorRoundTripper struct{ Err error }

func (e ErrorRoundTripper) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, e.Err
}

// ErrString is a typed error used by ErrorRoundTripper. It exists so tests
// don't import fmt/errors just to construct a stub failure.
type ErrString string

func (e ErrString) Error() string { return string(e) }
