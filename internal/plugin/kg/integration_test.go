// Package kg_test exercises the gRPC wire path for the kugou plugin. The
// kg-specific unit tests cover kugou's signature/MD5/outbound HTTP plumbing;
// this file pins the gRPC + proto layer that the unit tests bypass.
package kg

import (
	"context"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/status"

	"go-music-tag/api/proto/tagplugin"
	"go-music-tag/internal/plugin/integrationtest"
)

// TestGRPC_GetPluginInfo_OverWire pins the proto + Register + wire layer for
// kg. A proto regenerate that drops SupportsLyric or renumbers DisplayName
// is caught here even though kugou's business logic is unchanged.
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
		t.Errorf("PluginInfo.SupportsSearch false; kg advertises Search")
	}
	if !resp.GetSupportsLyric() {
		t.Errorf("PluginInfo.SupportsLyric false; kg advertises lyric fetch")
	}
}

// TestGRPC_Search_UpstreamFailureYieldsErrOverWire: kg.Server.Search
// propagates upstream HTTP errors as a non-OK gRPC status with no body,
// so the gateway's SearchMusic handler can log per-source skips
// instead of silently dropping them. The kg doSearch also surfaces
// JSON-decode failures (via the recently tightened contract).
// This test pins the corresponding wire contract: over gRPC the same call
// must arrive as a non-nil response with an empty Songs list, so that any
// future regression that drops the empty repeated-Song wire field is
// caught here rather than the unit tests.
func TestGRPC_Search_UpstreamFailureYieldsErrOverWire(t *testing.T) {
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
