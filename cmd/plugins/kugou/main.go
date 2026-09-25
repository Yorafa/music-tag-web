// KuGou music gRPC plugin server.
//
// Usage:
//
//	KUGOU_PORT=50052 go run ./cmd/plugins/kugou
package main

import (
	"fmt"
	"log"
	"net"
	"os"

	pb "go-music-tag/api/proto/tagplugin"
	"go-music-tag/internal/plugin"
	kugou "go-music-tag/internal/plugin/kg"
)

func main() {
	port := os.Getenv("KUGOU_PORT")
	if port == "" {
		port = "50052"
	}

	lis, err := net.Listen("tcp", ":"+port)
	if err != nil {
		log.Fatalf("failed to listen: %v", err)
	}

	grpcServer := plugin.NewGRPCServer()
	pb.RegisterTagSourceServer(grpcServer, kugou.NewServer())

	plugin.StartHealthServer(os.Getenv("KUGOU_HEALTH_PORT"))

	log.Printf("[kugou] gRPC server listening on :%s", port)
	fmt.Printf("[kugou] gRPC server listening on :%s\n", port)
	if err := grpcServer.Serve(lis); err != nil {
		log.Fatalf("failed to serve: %v", err)
	}
}
