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
	"sync/atomic"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/status"

	pb "go-music-tag/api/proto/tagplugin"
)

// clientKeepaliveParams forces the gRPC client to ping its plugin server
// every 30s with a 10s ping-ack timeout, even when no streams are open
// (PermitWithoutStream). This is the configuration that prevents the
// "long-lived channel goes Idle → next RPC hits Unavailable+EOF after
// the plugin container restarts / network blips" symptom that users had
// surfaced through [SearchMusic] log lines. Without PermitWithoutStream
// the pings stop the moment the channel has no active streams, which is
// the precise painpoint of stale-connection EOF on first-call-after-idle.
var clientKeepaliveParams = keepalive.ClientParameters{
	Time:                30 * time.Second,
	Timeout:             10 * time.Second,
	PermitWithoutStream: true,
}

// DialOptions configures how a gRPC client connects to a remote plugin.
//
// SECURITY: every gRPC connection from gateway/worker previously used
// insecure.NewCredentials() unconditionally. UseTLS=true makes the dial
// use TLS; CAFile (optional) overrides the system root pool with a
// private CA. A deployment that exposes plugin ports on a non-trusted
// network should set UseTLS=true so Song/FetchLyric bodies are not in
// clear text and the channel is not open to MITM tampering.
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
	addr string
	opts DialOptions

	mu     sync.Mutex
	conn   *grpc.ClientConn
	client pb.TagSourceClient

	// info is read by Supports* / Name / DisplayName on request goroutines
	// while ensureConn writes it, so it is atomic rather than a plain field
	// (REVIEW.md P2-11). The old code wrote it under g.mu but read it with
	// no lock at all — a genuine data race on the reconnect path, where a
	// Shutdown-then-dial overlaps in-flight requests.
	info atomic.Pointer[pb.PluginInfoResponse]
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
		grpc.WithKeepaliveParams(clientKeepaliveParams),
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
	g.mu.Unlock()
	// Store the handshake result atomically. Name/DisplayName/Supports* read
	// it without taking g.mu, which is why it cannot be a plain field.
	g.info.Store(info)

	// Register only on first successful handshake. Re-dials after a
	// Shutdown/EOF must NOT re-register — RegisterTagSource panics on
	// duplicate names (same contract as RegisterDownloadSource).
	if _, err := GetTagSource(info.Name); err != nil {
		RegisterTagSource(g)
	}
	return nil
}

// pluginInfo returns the cached handshake result, or nil before the first
// successful GetPluginInfo. All accessors go through here.
func (g *GRPCTagSource) pluginInfo() *pb.PluginInfoResponse { return g.info.Load() }

func (g *GRPCTagSource) Name() string {
	if info := g.pluginInfo(); info != nil {
		return info.Name
	}
	_ = g.ensureConn()
	if info := g.pluginInfo(); info != nil {
		return info.Name
	}
	return ""
}

func (g *GRPCTagSource) DisplayName() string {
	if info := g.pluginInfo(); info != nil {
		return info.DisplayName
	}
	return ""
}

// The Supports* gates all nil-guard (REVIEW.md P2-11). SupportsSearch and
// SupportsLyric used to dereference g.info directly while their siblings
// checked for nil; that only stayed safe because of an implicit invariant —
// "nothing calls these before a successful handshake". Nothing enforced it,
// and Name() above can return from a *failed* ensureConn with no info
// cached, so the nil case is reachable.
func (g *GRPCTagSource) SupportsSearch() bool {
	info := g.pluginInfo()
	return info != nil && info.SupportsSearch
}

func (g *GRPCTagSource) SupportsLyric() bool {
	info := g.pluginInfo()
	return info != nil && info.SupportsLyric
}

// SupportsId3 answers whether the underlying plugin can answer FetchID3ByTitle
// calls. Cached at first GetPluginInfo() round-trip (same as the other
// Supports* gates), so we never re-dial just to evaluate this. The nil-guard
// is a defense against early-callers that read the field before ensureConn()
// has populated the handshake result.
func (g *GRPCTagSource) SupportsId3() bool {
	info := g.pluginInfo()
	return info != nil && info.SupportsId3
}

// SupportsAudioURL mirrors plugin.PluginInfoResponse.supports_audio_url
// (gate set by the first GetPluginInfo round-trip). Cached, so this is a
// pure memory read after init.
func (g *GRPCTagSource) SupportsAudioURL() bool {
	info := g.pluginInfo()
	return info != nil && info.SupportsAudioUrl
}

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

// GetAudioURL proxies the gRPC rpc to the upstream plugin. The ("", nil)
// contract semantics are documented on plugin.TagSource (interface
// docstring is the source of truth — don't restate them here).
//
// gRPC-specific note: pb.UnimplementedTagSourceServer.GetAudioURL answers
// with codes.Unimplemented for plugins that haven't overridden this rpc
// yet. We collapse that status to ("", nil) here so callers don't need to
// re-implement gRPC status decoding at every site.
func (g *GRPCTagSource) GetAudioURL(ctx context.Context, songID string) (string, error) {
	if err := g.ensureConn(); err != nil {
		return "", err
	}
	resp, err := g.client.GetAudioURL(ctx, &pb.GetAudioRequest{Id: songID})
	if err != nil {
		if status.Code(err) == codes.Unimplemented {
			return "", nil
		}
		return "", err
	}
	return resp.Url, nil
}

// ─── gRPC DownloadSource adapter ───────────────────────────────────────────

// GRPCDownloadSource wraps a remote gRPC DownloadSource service.
type GRPCDownloadSource struct {
	addr string
	opts DialOptions

	mu     sync.Mutex
	conn   *grpc.ClientConn
	client pb.DownloadSourceClient

	// Same reasoning as GRPCTagSource.info (REVIEW.md P2-11): written by
	// ensureConn under mu, read by Name/DisplayName without it.
	info atomic.Pointer[pb.DownloadPluginInfoResponse]
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
		grpc.WithKeepaliveParams(clientKeepaliveParams),
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
	g.info.Store(info)
	// Register only on first successful handshake. Re-dials after a
	// Shutdown/EOF must NOT re-register — RegisterDownloadSource panics
	// on duplicate names (same contract as RegisterTagSource).
	if _, err := GetDownloadSource(info.Name); err != nil {
		RegisterDownloadSource(g)
	}
	return nil
}

// Name matches GRPCTagSource: lazy-dial on first read so gateway boot
// (`ds.Name()` in cmd/gateway) actually registers the download plugin.
// The previous pure field-read never called ensureConn, so a cold boot
// while youtube was briefly unreachable left download sources: [] forever
// until process restart — and even a healthy youtube never registered
// because Name() never dialed.
func (g *GRPCDownloadSource) Name() string {
	if info := g.info.Load(); info != nil {
		return info.Name
	}
	_ = g.ensureConn()
	if info := g.info.Load(); info != nil {
		return info.Name
	}
	return ""
}

func (g *GRPCDownloadSource) DisplayName() string {
	if info := g.info.Load(); info != nil {
		return info.DisplayName
	}
	_ = g.ensureConn()
	if info := g.info.Load(); info != nil {
		return info.DisplayName
	}
	return ""
}

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

func (g *GRPCDownloadSource) Download(ctx context.Context, videoID, dir string, opts DownloadOptions) (*DownloadResult, error) {
	if err := g.ensureConn(); err != nil {
		return nil, err
	}
	resp, err := g.client.Download(ctx, &pb.DownloadRequest{
		VideoId:      videoID,
		DownloadDir:  dir,
		Format:       opts.Format,
		OutputFormat: opts.OutputFormat,
		Quality:      opts.Quality,
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
		Duration: s.Duration,
	}
}
