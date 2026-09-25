// KuGou / Kuwo style plugin server entry point.
//
// Usage:
//
//	KUWO_PORT=50053 go run ./cmd/plugins/kuwo
package main

import (
	"log"
	"net"
	"os"

	pb "go-music-tag/api/proto/tagplugin"
	"go-music-tag/internal/plugin"
	"go-music-tag/internal/plugin/kuwo"
)

func main() {
	port := os.Getenv("KUWO_PORT")
	if port == "" {
		port = "50053"
	}
	lis, err := net.Listen("tcp", ":"+port)
	if err != nil {
		log.Fatalf("kuwo listen: %v", err)
	}
	srv := plugin.NewGRPCServer()
	pb.RegisterTagSourceServer(srv, kuwo.NewServer())
	plugin.StartHealthServer(os.Getenv("KUWO_HEALTH_PORT"))

	log.Printf("[kuwo] gRPC server listening on :%s", port)
	if err := srv.Serve(lis); err != nil {
		log.Fatalf("kuwo serve: %v", err)
	}
}
