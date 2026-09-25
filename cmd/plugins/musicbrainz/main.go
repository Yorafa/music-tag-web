// MusicBrainz plugin server entry point.
//
// Usage:
//
//	MUSICBRAINZ_PORT=50056 go run ./cmd/plugins/musicbrainz
package main

import (
	"log"
	"net"
	"os"

	pb "go-music-tag/api/proto/tagplugin"
	"go-music-tag/internal/plugin"
	"go-music-tag/internal/plugin/musicbrainz"
)

func main() {
	port := os.Getenv("MUSICBRAINZ_PORT")
	if port == "" {
		port = "50056"
	}
	lis, err := net.Listen("tcp", ":"+port)
	if err != nil {
		log.Fatalf("musicbrainz listen: %v", err)
	}
	srv := plugin.NewGRPCServer()
	pb.RegisterTagSourceServer(srv, musicbrainz.NewServer())
	plugin.StartHealthServer(os.Getenv("MUSICBRAINZ_HEALTH_PORT"))

	log.Printf("[musicbrainz] gRPC server listening on :%s", port)
	if err := srv.Serve(lis); err != nil {
		log.Fatalf("musicbrainz serve: %v", err)
	}
}
