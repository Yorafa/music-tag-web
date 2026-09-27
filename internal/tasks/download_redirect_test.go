package tasks

import (
	"context"
	"net"
	"net/http"
	"testing"
)

// The audio-download client caps redirects at 5 hops AND re-validates every
// redirect target with downloadGuard (REVIEW.md P1-2). Before the per-hop
// check, a plugin-supplied public URL could 302 the worker to
// http://169.254.169.254/ or an RFC 1918 host and the hop-count cap alone
// would follow it — the SSRF guard only ran on the initial URL. These specs
// exercise the downloadCheckRedirect policy directly so the assertion does not
// depend on a live redirect round-trip.

// runDownloadRedirect drives downloadCheckRedirect for a single hop to
// targetURL, installing a resolver that maps every host to ip.
func runDownloadRedirect(t *testing.T, targetURL string, ip net.IP) error {
	t.Helper()
	orig := downloadGuard.Resolver
	downloadGuard.Resolver = func(_ context.Context, _ string) ([]net.IP, error) {
		return []net.IP{ip}, nil
	}
	t.Cleanup(func() { downloadGuard.Resolver = orig })

	req, err := http.NewRequest(http.MethodGet, targetURL, nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	return downloadCheckRedirect(req, []*http.Request{})
}

func TestDownloadRedirect_RejectsPrivateHop(t *testing.T) {
	if err := runDownloadRedirect(t, "http://evil.test/pivot", net.ParseIP("169.254.169.254")); err == nil {
		t.Fatal("expected redirect to a link-local address to be rejected")
	}
}

func TestDownloadRedirect_RejectsRFC1918Hop(t *testing.T) {
	if err := runDownloadRedirect(t, "http://internal.test/", net.ParseIP("10.0.0.5")); err == nil {
		t.Fatal("expected redirect to an RFC 1918 address to be rejected")
	}
}

func TestDownloadRedirect_AllowsPublicHop(t *testing.T) {
	if err := runDownloadRedirect(t, "http://cdn.test/audio.mp3", net.ParseIP("8.8.8.8")); err != nil {
		t.Fatalf("expected redirect to a public address to be allowed, got %v", err)
	}
}

func TestDownloadRedirect_RejectsTooManyHops(t *testing.T) {
	req, err := http.NewRequest(http.MethodGet, "http://cdn.test/audio.mp3", nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	via := make([]*http.Request, 5)
	if err := downloadCheckRedirect(req, via); err == nil {
		t.Fatal("expected the 5-hop cap to reject the sixth redirect")
	}
}
