// Package kuwo_test exercises the gRPC wire path for the kuwo plugin. The
// kuwo-specific unit tests cover parseKuwoJSON + signature + outbound HTTP;
// this file pins the gRPC + proto layer that those tests bypass.
package kuwo

import (
	"context"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/status"

	"go-music-tag/api/proto/tagplugin"
	"go-music-tag/internal/plugin/integrationtest"
)

// TestGRPC_GetPluginInfo_OverWire pins the proto + Register + wire layer for
// the kuwo plugin. A proto regenerate that drops or renumbers any of
// Name/DisplayName/SupportsX will be caught here even though the production
// code is unchanged.
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
		t.Errorf("PluginInfo.SupportsSearch false; kuwo advertises Search")
	}
	if !resp.GetSupportsLyric() {
		t.Errorf("PluginInfo.SupportsLyric false; kuwo advertises lyric fetch")
	}
}

// TestGRPC_Search_UpstreamFailureYieldsErrOverWire pins the kuwo
// plugin's propagated-error wire contract: upstream HTTP failure
// surfaces as a non-OK gRPC status with no body, so the gateway's
// SearchMusic handler can log per-source skips instead of silently
// dropping them.
// for the full rationale.
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
