// Package plugin provides a gRPC-backed adapter that implements the local
// TagSource interface by calling a remote gRPC TagSource service.
package plugin

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"

	pb "go-music-tag/api/proto/tagplugin"
)

// DialOptions configures how a gRPC client connects to a remote plugin.
//
// SECURITY (P1.5 issue F): until this commit, every gRPC connection from
// gateway/worker used insecure.NewCredentials(). The previous code never
// had a TLS toggle, so a deployment that exposed plugin ports on a
// non-trusted network would have every Song/FetchLyric body in clear text
// and was open to MITM tampering. UseTLS=true makes the dial use TLS;
// CAFile (optional) overrides the system root pool with a private CA.
type DialOptions struct {
	// UseTLS turns TLS on for the gRPC dial. Default false preserves the
	// P1 behaviour (suitable for trusted docker-compose networks) but
	// flip to true for any production-style cluster.
	UseTLS bool
	// CAFile is a PEM-encoded CA bundle. Only read when UseTLS is true.
	// Empty means: trust the system's root CAs.
	CAFile string
}

// Credentials resolves to a TransportCredentials based on UseTLS/CAFile.
// Any misconfiguration is surfaced at dial time (so mis-set CAFile won't
// silently fall back to insecure).
func (o DialOptions) Credentials() (credentials.TransportCredentials, error) {
	if !o.UseTLS {
		return insecure.NewCredentials(), nil
	}
	pool, err := x509.SystemCertPool()
	if err != nil || pool == nil {
		pool = x509.NewCertPool()
	}
	if o.CAFile != "" {
		pem, err := os.ReadFile(o.CAFile)
		if err != nil {
			return nil, fmt.Errorf("read CA file %q: %w", o.CAFile, err)
		}
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("CA file %q contains no PEM certificates", o.CAFile)
		}
	}
	return credentials.NewTLS(&tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}), nil
}

// ─── gRPC TagSource adapter ────────────────────────────────────────────────

// GRPCTagSource wraps a remote gRPC TagSource service so it satisfies our
// local plugin.TagSource interface.
type GRPCTagSource struct {
	name        string
	displayName string
	addr        string
	opts        DialOptions
	conn        *grpc.ClientConn
	client      pb.TagSourceClient
	mu          sync.Mutex
	info        *pb.PluginInfoResponse // cached
}

// NewGRPCTagSource creates a gRPC-backed tag source with the supplied
// dial options. The connection is created lazily on first call.
func NewGRPCTagSource(addr string, opts DialOptions) *GRPCTagSource {
	return &GRPCTagSource{addr: addr, opts: opts}
}

func (g *GRPCTagSource) ensureConn() error {
	var info *pb.PluginInfoResponse

	g.mu.Lock()
	if g.conn != nil && g.conn.GetState() != connectivity.Shutdown {
		g.mu.Unlock()
		return nil
	}
	g.mu.Unlock()

	creds, err := g.opts.Credentials()
	if err != nil {
		return fmt.Errorf("grpc creds for %s: %w", g.addr, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, err := grpc.DialContext(ctx, g.addr,
		grpc.WithTransportCredentials(creds),
		grpc.WithBlock(),
	)
	if err != nil {
		return fmt.Errorf("grpc dial %s: %w", g.addr, err)
	}
	client := pb.NewTagSourceClient(conn)

	// Fetch plugin info.
	info, err = client.GetPluginInfo(context.Background(), &pb.PluginInfoRequest{})
	if err != nil {
		conn.Close()
		return fmt.Errorf("grpc get plugin info: %w", err)
	}

	g.mu.Lock()
	g.conn = conn
	g.client = client
	g.info = info
	g.name = info.Name
	g.displayName = info.DisplayName
	g.mu.Unlock()

	// Register with the global registry AFTER releasing g.mu (avoid lock ordering).
	RegisterTagSource(g)
	return nil
}

func (g *GRPCTagSource) Name() string {
	if g.name != "" {
		return g.name
	}
	_ = g.ensureConn()
	return g.name
}

func (g *GRPCTagSource) DisplayName() string        { return g.displayName }
func (g *GRPCTagSource) SupportsSearch() bool        { return g.info.SupportsSearch }
func (g *GRPCTagSource) SupportsLyric() bool         { return g.info.SupportsLyric }
// SupportsId3 answers whether the underlying plugin can answer FetchID3ByTitle
// calls. Cached at first GetPluginInfo() round-trip (same as the other
// Supports* gates), so we never re-dial just to evaluate this. The nil-guard
// is a defense against early-callers that read the field before ensureConn()
// has populated `g.info` from the gRPC handshake.
func (g *GRPCTagSource) SupportsId3() bool           { return g.info != nil && g.info.SupportsId3 }

func (g *GRPCTagSource) Search(ctx context.Context, query string, page, limit int) (*SearchResult, error) {
	if err := g.ensureConn(); err != nil {
		return nil, err
	}
	resp, err := g.client.Search(ctx, &pb.SearchRequest{
		Query: query,
		Page:  int32(page),
		Limit: int32(limit),
	})
	if err != nil {
		return nil, err
	}
	songs := make([]Song, len(resp.Songs))
	for i, s := range resp.Songs {
		songs[i] = pbToSong(s)
	}
	return &SearchResult{Songs: songs, HasMore: resp.HasMore}, nil
}

func (g *GRPCTagSource) FetchID3ByTitle(ctx context.Context, title string) ([]Song, error) {
	if err := g.ensureConn(); err != nil {
		return nil, err
	}
	resp, err := g.client.FetchId3ByTitle(ctx, &pb.FetchId3Request{Title: title})
	if err != nil {
		return nil, err
	}
	songs := make([]Song, len(resp.Songs))
	for i, s := range resp.Songs {
		songs[i] = pbToSong(s)
	}
	return songs, nil
}

func (g *GRPCTagSource) FetchLyric(ctx context.Context, songID string) (string, error) {
	if err := g.ensureConn(); err != nil {
		return "", err
	}
	resp, err := g.client.FetchLyric(ctx, &pb.FetchLyricRequest{SongId: songID})
	if err != nil {
		return "", err
	}
	return resp.Lyric, nil
}

// ─── gRPC DownloadSource adapter ───────────────────────────────────────────

// GRPCDownloadSource wraps a remote gRPC DownloadSource service.
type GRPCDownloadSource struct {
	name        string
	displayName string
	addr        string
	opts        DialOptions
	conn        *grpc.ClientConn
	client      pb.DownloadSourceClient
	mu          sync.Mutex
}

// NewGRPCDownloadSource creates a gRPC-backed download source.
func NewGRPCDownloadSource(addr string, opts DialOptions) *GRPCDownloadSource {
	return &GRPCDownloadSource{addr: addr, opts: opts}
}

func (g *GRPCDownloadSource) ensureConn() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.conn != nil && g.conn.GetState() != connectivity.Shutdown {
		return nil
	}
	creds, err := g.opts.Credentials()
	if err != nil {
		return fmt.Errorf("grpc creds for %s: %w", g.addr, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, err := grpc.DialContext(ctx, g.addr,
		grpc.WithTransportCredentials(creds),
		grpc.WithBlock(),
	)
	if err != nil {
		return fmt.Errorf("grpc dial %s: %w", g.addr, err)
	}
	g.conn = conn
	g.client = pb.NewDownloadSourceClient(conn)

	info, err := g.client.GetPluginInfo(context.Background(), &pb.DownloadPluginInfoRequest{})
	if err != nil {
		conn.Close()
		g.conn = nil
		return fmt.Errorf("grpc get plugin info: %w", err)
	}
	g.name = info.Name
	g.displayName = info.DisplayName
	RegisterDownloadSource(g)
	return nil
}

func (g *GRPCDownloadSource) Name() string       { return g.name }
func (g *GRPCDownloadSource) DisplayName() string { return g.displayName }

func (g *GRPCDownloadSource) Search(ctx context.Context, query string, max int) ([]DownloadItem, error) {
	if err := g.ensureConn(); err != nil {
		return nil, err
	}
	resp, err := g.client.Search(ctx, &pb.DownloadSearchRequest{
		Query:      query,
		MaxResults: int32(max),
	})
	if err != nil {
		return nil, err
	}
	items := make([]DownloadItem, len(resp.Items))
	for i, it := range resp.Items {
		items[i] = DownloadItem{
			ID:        it.Id,
			Title:     it.Title,
			Duration:  it.Duration,
			URL:       it.Url,
			Channel:   it.Channel,
			Thumbnail: it.Thumbnail,
		}
	}
	return items, nil
}

func (g *GRPCDownloadSource) Download(ctx context.Context, videoID, dir string) (*DownloadResult, error) {
	if err := g.ensureConn(); err != nil {
		return nil, err
	}
	resp, err := g.client.Download(ctx, &pb.DownloadRequest{
		VideoId:     videoID,
		DownloadDir: dir,
	})
	if err != nil {
		return nil, err
	}
	return &DownloadResult{
		Success:  resp.Success,
		FilePath: resp.FilePath,
		FileName: resp.FileName,
		Error:    resp.Error,
	}, nil
}

// pbToSong converts a protobuf Song to our domain type.
func pbToSong(s *pb.Song) Song {
	return Song{
		ID:       s.Id,
		Name:     s.Name,
		Artist:   s.Artist,
		ArtistID: s.ArtistId,
		Album:    s.Album,
		AlbumID:  s.AlbumId,
		AlbumImg: s.AlbumImg,
		Year:     s.Year,
		Source:   s.Source,
		Genre:    s.Genre,
		Mid:      s.Mid,
		Cover:    s.Cover,
		Score:    s.Score,
	}
}
