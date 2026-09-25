// AcoustID plugin server entry point.
//
// Usage:
//
//	ACOUSTID_PORT=50057 go run ./cmd/plugins/acoustid
//	FPCALC_BIN=/usr/local/bin/fpcalc    # 选填，未指定则 PATH 查找
package main

import (
	"log"
	"net"
	"os"

	pb "go-music-tag/api/proto/tagplugin"
	"go-music-tag/internal/plugin"
	"go-music-tag/internal/plugin/acoustid"
)

func main() {
	port := os.Getenv("ACOUSTID_PORT")
	if port == "" {
		port = "50057"
	}
	lis, err := net.Listen("tcp", ":"+port)
	if err != nil {
		log.Fatalf("acoustid listen: %v", err)
	}
	srv := plugin.NewGRPCServer()
	pb.RegisterTagSourceServer(srv, acoustid.NewServer())
	plugin.StartHealthServer(os.Getenv("ACOUSTID_HEALTH_PORT"))

	log.Printf("[acoustid] gRPC server listening on :%s (requires fpcalc on PATH or FPCALC_BIN)", port)
	if err := srv.Serve(lis); err != nil {
		log.Fatalf("acoustid serve: %v", err)
	}
}
