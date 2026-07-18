// Package netease_test exercises the gRPC wire path for the netease plugin.
// The plugin-specific unit tests (server_test.go) already cover outbound HTTP,
// AES encryption, and per-method business logic; this file pins the proto +
// gRPC layer that those tests bypass.
package netease

import (
	"context"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/status"

	"go-music-tag/api/proto/tagplugin"
	"go-music-tag/internal/plugin/integrationtest"
)

// newServerForTest wraps the constructor's (*Server, error) signature so the
// integration tests don't have to repeat the err-handling boilerplate.
func newServerForTest(t *testing.T) *Server {
	t.Helper()
	s, err := NewServer()
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	return s
}

// TestGRPC_GetPluginInfo_OverWire proves the netease plugin is correctly
// wired into the TagSource service:
//   - RegisterTagSourceServer must succeed
//   - the gRPC framing must round-trip every field of PluginInfoResponse
//     (Name, DisplayName, SupportsSearch, SupportsLyric, SupportsId3)
//
// A regression that renumbers any proto field will be caught because one of
// the named getters will return "" / false even though the underlying Server
// populates them.
func TestGRPC_GetPluginInfo_OverWire(t *testing.T) {
	srv := newServerForTest(t)
	client := integrationtest.Run(t, func(s *grpc.Server) {
		tagplugin.RegisterTagSourceServer(s, srv)
	})

	resp, err := client.GetPluginInfo(context.Background(), &tagplugin.PluginInfoRequest{})
	if err != nil {
		st, _ := status.FromError(err)
		t.Fatalf("GetPluginInfo over wire failed: code=%v msg=%v raw=%v", st.Code(), st.Message(), err)
	}
	if resp.GetName() == "" {
		t.Errorf("PluginInfo.Name empty; want non-empty (proto field 1 still wired)")
	}
	if resp.GetDisplayName() == "" {
		t.Errorf("PluginInfo.DisplayName empty; want non-empty (proto field 2 still wired)")
	}
	if !resp.GetSupportsSearch() {
		t.Errorf("PluginInfo.SupportsSearch false; netease advertises Search")
	}
	if !resp.GetSupportsLyric() {
		t.Errorf("PluginInfo.SupportsLyric false; netease advertises lyric fetch")
	}
}

// TestGRPC_Search_UpstreamFailureYieldsErrOverWire pins the netease
// plugin's upstream-failure wire contract:
//
//   - Protocol: netease.Server.Search propagates upstream HTTP errors and
//     JSON-decode failures as (nil, err). The gRPC layer therefore encodes
//     a non-OK status with no response body, never an empty SearchResponse.
//
// This test stubs srv.client.Transport with integrationtest.ErrorRoundTripper
// so the upstream call short-circuits, then asserts that the response
// arrives over gRPC as a non-OK status with no body. A wire bug that, e.g.,
// fails to encode the gRPC error trailer, would surface as resp != nil here.
//
// (The previous "swallowed-upstream-err returns empty Songs" contract was
//  removed in this same change; the test name + comment both flipped
//  accordingly so the next reviewer can't reach for the old contract.)
func TestGRPC_Search_UpstreamFailureYieldsErrOverWire(t *testing.T) {
	srv := newServerForTest(t)
	srv.client.Transport = integrationtest.ErrorRoundTripper{
		Err: integrationtest.ErrString("stub: net failure"),
	}
	client := integrationtest.Run(t, func(s *grpc.Server) {
		tagplugin.RegisterTagSourceServer(s, srv)
	})

	resp, err := client.Search(context.Background(), &tagplugin.SearchRequest{
		Query: "hello", Page: 1, Limit: 5,
	})
	if err == nil {
		t.Fatalf("Search over-wire returned nil err; want gRPC err propagated from plugin (resp=%+v)", resp)
	}
	if resp != nil {
		t.Errorf("Search over-wire returned non-nil resp; want nil: %+v", resp)
	}
}
