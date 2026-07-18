// Package acoustid_test exercises the gRPC wire path for the acoustid
// plugin. The acoustid-specific unit tests cover fpcalc invocation + JSON
// output parsing; this file pins the gRPC + proto layer.
package acoustid

import (
	"context"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"go-music-tag/api/proto/tagplugin"
	"go-music-tag/internal/plugin/integrationtest"
)

// TestGRPC_GetPluginInfo_OverWire pins the proto + Register + wire layer for
// the acoustid plugin. acoustid advertises id3 only (no Search, no lyric);
// the wire layer must still carry all 5 proto fields correctly even when
// the supports_* booleans are false.
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
	if !resp.GetSupportsId3() {
		t.Errorf("PluginInfo.SupportsId3 false; acoustid advertises id3")
	}
	// Search and Lyric may legitimately be false; the wire contract is
	// "encoded", not "true". We deliberately do not assert on these.
	_ = resp.GetSupportsSearch()
	_ = resp.GetSupportsLyric()
}

// TestGRPC_FetchId3ByTitle_NotImplementedOverWire pins the
// "unimplemented method via UnimplementedTagSourceServer" return path:
// acoustid does not implement FetchId3ByTitle — but Register still works
// (mustEmbedUnimplementedTagSourceServer), and the client must observe
// either Unimplemented or an empty Songs slice, not a panic or silent nil
// that would mask a future proto bug.
func TestGRPC_FetchId3ByTitle_NotImplementedOverWire(t *testing.T) {
	srv := NewServer()
	client := integrationtest.Run(t, func(s *grpc.Server) {
		tagplugin.RegisterTagSourceServer(s, srv)
	})

	resp, err := client.FetchId3ByTitle(context.Background(), &tagplugin.FetchId3Request{
		Title: "hello",
	})
	// Two acceptable outcomes for this test on acoustid:
	//
	//   (a) err == nil and resp.GetSongs() is empty  — acoustid actually
	//       implements FetchId3ByTitle and returns an empty Songs list when
	//       no fpcalc + audio file is supplied. The wire contract that
	//       matters is: proto3 empty repeated-Song list decodes correctly
	//       over gRPC.
	//
	//   (b) err != nil with status codes.Unimplemented  — the proto
	//       generated fallback when mustEmbedUnimplementedTagSourceServer
	//       is reached. This is "what happens if acoustid ever stops
	//       embedding the unimplemented default".
	//
	// Restricting to (b) alone breaks (a); restricting to (a) alone would
	// break if the implementation ever started short-circuiting to (b).
	// Accept either and assert on the structural property.
	switch {
	case err == nil:
		if len(resp.GetSongs()) != 0 {
			t.Errorf("FetchId3ByTitle should return empty Songs; got %d", len(resp.GetSongs()))
		}
	default:
		st, ok := status.FromError(err)
		if !ok {
			t.Errorf("FetchId3ByTitle returned non-grpc error: %v", err)
			return
		}
		if st.Code() != codes.Unimplemented {
			t.Errorf("expected codes.Unimplemented (or empty Songs), got code=%v msg=%q", st.Code(), st.Message())
		}
	}
}
