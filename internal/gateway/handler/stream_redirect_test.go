package handler

import (
	"context"
	"net"
	"net/http"
	"testing"
)

// The streamUpstreamClient caps redirects at 5 hops AND re-validates every
// redirect target with streamGuard. Both are required: a hop-count cap
// alone still follows a plugin-supplied public URL that 302s the gateway to
// http://169.254.169.254/ or an RFC 1918 host, because the guard would
// only ever have run on the initial URL. These specs
// exercise the CheckRedirect closure directly so the assertion does not
// depend on a live redirect round-trip.

// checkRedirect drives streamUpstreamClient.CheckRedirect for a single hop to
// targetURL, installing a resolver that maps every host to ip.
func streamCheckRedirect(t *testing.T, targetURL string, ip net.IP) error {
	t.Helper()
	orig := streamGuard.Resolver
	streamGuard.Resolver = func(_ context.Context, _ string) ([]net.IP, error) {
		return []net.IP{ip}, nil
	}
	t.Cleanup(func() { streamGuard.Resolver = orig })

	req, err := http.NewRequest(http.MethodGet, targetURL, nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	return streamUpstreamClient.CheckRedirect(req, []*http.Request{})
}

func TestStreamUpstreamRedirect_RejectsPrivateHop(t *testing.T) {
	if err := streamCheckRedirect(t, "http://evil.test/pivot", net.ParseIP("169.254.169.254")); err == nil {
		t.Fatal("expected redirect to a link-local address to be rejected")
	}
}

func TestStreamUpstreamRedirect_RejectsRFC1918Hop(t *testing.T) {
	if err := streamCheckRedirect(t, "http://internal.test/", net.ParseIP("10.0.0.5")); err == nil {
		t.Fatal("expected redirect to an RFC 1918 address to be rejected")
	}
}

func TestStreamUpstreamRedirect_AllowsPublicHop(t *testing.T) {
	if err := streamCheckRedirect(t, "http://cdn.test/audio.mp3", net.ParseIP("8.8.8.8")); err != nil {
		t.Fatalf("expected redirect to a public address to be allowed, got %v", err)
	}
}

func TestStreamUpstreamRedirect_RejectsTooManyHops(t *testing.T) {
	req, err := http.NewRequest(http.MethodGet, "http://cdn.test/audio.mp3", nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	via := make([]*http.Request, 5)
	if err := streamUpstreamClient.CheckRedirect(req, via); err == nil {
		t.Fatal("expected the 5-hop cap to reject the sixth redirect")
	}
}
