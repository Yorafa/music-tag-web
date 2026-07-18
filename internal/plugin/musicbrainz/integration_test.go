// Package musicbrainz_test exercises the gRPC wire path for the musicbrainz
// plugin. The plugin-specific unit tests cover outbound HTTP, the throttle,
// and the XML response parsing; this file pins the gRPC + proto layer.
package musicbrainz

import (
	"context"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/status"

	"go-music-tag/api/proto/tagplugin"
	"go-music-tag/internal/plugin/integrationtest"
)

// resetThrottle is defined in server_test.go (same-package test file).
// The reason THIS test calls it: musicbrainz owns a process-global throttle
// to respect MusicBrainz's 1 req/s rate-limit on real upstream calls; if
// any prior test in the binary warmed the lastCall timestamp, our stub
// Transport would be short-circuited at the throttle gate inside
// internal/plugin/musicbrainz/server.go BEFORE reaching client.Do.
//
// Companion contract: resetThrottle zeros both lastCall and lastCallMu.

// TestGRPC_GetPluginInfo_OverWire pins the proto + Register + wire layer for
// the musicbrainz plugin. musicbrainz advertises Search only (no lyric);
// the wire layer must still carry all 5 proto fields correctly.
func TestGRPC_GetPluginInfo_OverWire(t *testing.T) {
	srv := NewServer()
	client := integrationtest.Run(t, func(s *grpc.Server) {
		tagplugin.RegisterTagSourceServer(s, srv)
	})

	resp, err := client.GetPluginInfo(context.Background(), &tagplugin.PluginInfoRequest{})
	if err != nil {
		st, _ := status.FromError(err)
		t.Fatalf("GetPluginInfo: code=%v msg=%v raw=%v", st.Code(), st.Message(), err)
	}
	if resp.GetName() == "" {
		t.Errorf("PluginInfo.Name empty; want non-empty")
	}
	if resp.GetDisplayName() == "" {
		t.Errorf("PluginInfo.DisplayName empty; want non-empty")
	}
	if !resp.GetSupportsSearch() {
		t.Errorf("PluginInfo.SupportsSearch false; musicbrainz advertises Search")
	}
}

// TestGRPC_Search_UpstreamFailureYieldsErrOverWire pins the musicbrainz
// propagated-error wire contract: upstream HTTP failure surfaces as a
// non-OK gRPC status with no body, so the gateway's SearchMusic handler
// can log per-source skips instead of silently dropping them. The
// resetThrottleForIntegrationTest() at
// the start is mandatory — see that function's comment for the reasoning.
func TestGRPC_Search_UpstreamFailureYieldsErrOverWire(t *testing.T) {
	resetThrottle()
	srv := NewServer()
	srv.client.Transport = integrationtest.ErrorRoundTripper{
		Err: integrationtest.ErrString("stub: net failure"),
	}
	client := integrationtest.Run(t, func(s *grpc.Server) {
		tagplugin.RegisterTagSourceServer(s, srv)
	})

	resp, err := client.Search(context.Background(), &tagplugin.SearchRequest{
		Query: "hello", Page: 1, Limit: 10,
	})
	if err == nil {
		t.Fatalf("Search over-wire returned nil err; want gRPC err propagated from plugin (resp=%+v)", resp)
	}
	if resp != nil {
		t.Errorf("Search over-wire returned non-nil resp; want nil: %+v", resp)
	}
}
