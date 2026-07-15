// 咪咕 (Migu) plugin server entry point.
//
// Usage:
//
//	MIGU_PORT=50054 go run ./cmd/plugins/migu
package main

import (
	"log"
	"net"
	"os"

	"google.golang.org/grpc"

	pb "go-music-tag/api/proto/tagplugin"
	"go-music-tag/internal/plugin/migu"
)

func main() {
	port := os.Getenv("MIGU_PORT")
	if port == "" {
		port = "50054"
	}
	lis, err := net.Listen("tcp", ":"+port)
	if err != nil {
		log.Fatalf("migu listen: %v", err)
	}
	srv := grpc.NewServer()
	pb.RegisterTagSourceServer(srv, migu.NewServer())
	log.Printf("[migu] gRPC server listening on :%s", port)
	if err := srv.Serve(lis); err != nil {
		log.Fatalf("migu serve: %v", err)
	}
}
