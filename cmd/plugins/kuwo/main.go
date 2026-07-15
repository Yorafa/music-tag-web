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

	"google.golang.org/grpc"

	pb "go-music-tag/api/proto/tagplugin"
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
	srv := grpc.NewServer()
	pb.RegisterTagSourceServer(srv, kuwo.NewServer())
	log.Printf("[kuwo] gRPC server listening on :%s", port)
	if err := srv.Serve(lis); err != nil {
		log.Fatalf("kuwo serve: %v", err)
	}
}
