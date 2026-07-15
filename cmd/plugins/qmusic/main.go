// QQ音乐 plugin server entry point.
//
// Usage:
//
//	QMUSIC_PORT=50055 go run ./cmd/plugins/qmusic
package main

import (
	"log"
	"net"
	"os"

	"google.golang.org/grpc"

	pb "go-music-tag/api/proto/tagplugin"
	"go-music-tag/internal/plugin/qmusic"
)

func main() {
	port := os.Getenv("QMUSIC_PORT")
	if port == "" {
		port = "50055"
	}
	lis, err := net.Listen("tcp", ":"+port)
	if err != nil {
		log.Fatalf("qmusic listen: %v", err)
	}
	srv := grpc.NewServer()
	pb.RegisterTagSourceServer(srv, qmusic.NewServer())
	log.Printf("[qmusic] gRPC server listening on :%s", port)
	if err := srv.Serve(lis); err != nil {
		log.Fatalf("qmusic serve: %v", err)
	}
}
