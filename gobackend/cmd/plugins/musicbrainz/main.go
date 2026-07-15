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

	"google.golang.org/grpc"

	pb "go-music-tag/api/proto/tagplugin"
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
	srv := grpc.NewServer()
	pb.RegisterTagSourceServer(srv, musicbrainz.NewServer())
	log.Printf("[musicbrainz] gRPC server listening on :%s", port)
	if err := srv.Serve(lis); err != nil {
		log.Fatalf("musicbrainz serve: %v", err)
	}
}
