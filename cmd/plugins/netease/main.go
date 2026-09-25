// NetEaseCloudMusic gRPC plugin server.
//
// Usage:
//
//	NETEASE_PORT=50051 go run ./cmd/plugins/netease
package main

import (
	"log"
	"net"
	"os"

	pb "go-music-tag/api/proto/tagplugin"
	"go-music-tag/internal/plugin"
	netease "go-music-tag/internal/plugin/netease"
)

func main() {
	port := os.Getenv("NETEASE_PORT")
	if port == "" {
		port = "50051"
	}

	lis, err := net.Listen("tcp", ":"+port)
	if err != nil {
		log.Fatalf("failed to listen: %v", err)
	}

	srv, err := netease.NewServer()
	if err != nil {
		log.Fatalf("failed to create server: %v", err)
	}

	grpcServer := plugin.NewGRPCServer()
	pb.RegisterTagSourceServer(grpcServer, srv)

	plugin.StartHealthServer(os.Getenv("NETEASE_HEALTH_PORT"))

	log.Printf("[netease] gRPC server listening on :%s", port)
	if err := grpcServer.Serve(lis); err != nil {
		log.Fatalf("failed to serve: %v", err)
	}
}
