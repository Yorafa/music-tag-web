// YouTube gRPC DownloadSource plugin server.
//
// Usage:
//
//	YOUTUBE_PORT=50058 go run ./cmd/plugins/youtube
//
// The plugin runs in its own container (see docker-compose.yml `youtube:`
// service). The gateway dials it via the PLUGIN_YOUTUBE_ADDR env var
// and registers it under plugin name "youtube", surfaced in /api/sources/
// alongside the TagSources.
package main

import (
	"log"
	"net"
	"os"

	"google.golang.org/grpc"

	pb "go-music-tag/api/proto/tagplugin"
	youtube "go-music-tag/internal/plugin/youtube"
)

func main() {
	port := os.Getenv("YOUTUBE_PORT")
	if port == "" {
		port = "50058"
	}

	lis, err := net.Listen("tcp", ":"+port)
	if err != nil {
		log.Fatalf("failed to listen: %v", err)
	}

	srv, err := youtube.NewServer()
	if err != nil {
		log.Fatalf("failed to create server: %v", err)
	}

	grpcServer := grpc.NewServer()
	pb.RegisterDownloadSourceServer(grpcServer, srv)

	log.Printf("[youtube] gRPC DownloadSource listening on :%s", port)
	if err := grpcServer.Serve(lis); err != nil {
		log.Fatalf("failed to serve: %v", err)
	}
}
