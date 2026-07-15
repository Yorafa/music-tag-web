package netguard

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"testing"
)

// ─── test helpers ──────────────────────────────────────────────────────────

// fakeHostIP maps a hostname to a fixed IP set. Tests use this to simulate
// "public" vs "private" resolutions without involving real DNS.
func fakeHostIP(m map[string][]net.IP) ResolverFunc {
	return func(ctx context.Context, host string) ([]net.IP, error) {
		if ips, ok := m[host]; ok {
			return ips, nil
		}
		return nil, errors.New("no fake entry for " + host)
	}
}

type fakeRoundTrip struct {
	handler func(req *http.Request) *http.Response
}

func (f *fakeRoundTrip) RoundTrip(req *http.Request) (*http.Response, error) {
	if r := f.handler(req); r != nil {
		return r, nil
	}
	return nil, errors.New("fakeRoundTrip: handler returned nil")
}

func newResponse(code int, body string, headers http.Header) *http.Response {
	if headers == nil {
		headers = http.Header{}
	}
	return &http.Response{
		StatusCode:    code,
		Body:          io.NopCloser(bytes.NewReader([]byte(body))),
		Header:        headers,
		ContentLength: int64(len(body)),
	}
}

// ─── tests ────────────────────────────────────────────────────────────────

func TestSafeHTTPGet_HappyPathReturnsBody(t *testing.T) {
	g := &Guard{
		Resolver:  fakeHostIP(map[string][]net.IP{"public.test": {net.ParseIP("8.8.8.8")}}),
		Transport: &fakeRoundTrip{handler: func(*http.Request) *http.Response { return newResponse(200, "hello", nil) }},
	}
	body, err := g.SafeHTTPGet(context.Background(), "http://public.test/cover.jpg", 1<<20)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if string(body) != "hello" {
		t.Errorf("body=%q (want hello)", string(body))
	}
}

func TestSafeHTTPGet_RejectsPrivateInitialURL(t *testing.T) {
	g := &Guard{
		Resolver:  fakeHostIP(map[string][]net.IP{"private.test": {net.ParseIP("10.0.0.1")}}),
		Transport: &fakeRoundTrip{handler: func(*http.Request) *http.Response { return newResponse(200, "should not see", nil) }},
	}
	_, err := g.SafeHTTPGet(context.Background(), "http://private.test/x", 1<<20)
	if err == nil {
		t.Fatal("expected rejection of private initial URL")
	}
}

func TestSafeHTTPGet_RejectsRedirectToPrivate(t *testing.T) {
	// public.test resolves to 8.8.8.8, redirect target evil.test to 127.0.0.1.
	g := &Guard{
		Resolver: fakeHostIP(map[string][]net.IP{
			"public.test": {net.ParseIP("8.8.8.8")},
			"evil.test":   {net.ParseIP("127.0.0.1")},
		}),
		Transport: &fakeRoundTrip{handler: func(req *http.Request) *http.Response {
			if req.Host == "public.test" {
				return newResponse(302, "", http.Header{"Location": []string{"http://evil.test/"}})
			}
			return newResponse(200, "should not see", nil)
		}},
	}
	_, err := g.SafeHTTPGet(context.Background(), "http://public.test/cover.jpg", 1<<20)
	if err == nil {
		t.Fatal("expected private-redirect rejection")
	}
}

func TestSafeHTTPGet_AllowsRedirectToAnotherPublicHost(t *testing.T) {
	g := &Guard{
		Resolver: fakeHostIP(map[string][]net.IP{
			"public.test": {net.ParseIP("8.8.8.8")},
			"also.test":   {net.ParseIP("1.1.1.1")},
		}),
		Transport: &fakeRoundTrip{handler: func(req *http.Request) *http.Response {
			if req.Host == "public.test" {
				return newResponse(302, "", http.Header{"Location": []string{"http://also.test/"}})
			}
			return newResponse(200, "final", nil)
		}},
	}
	body, err := g.SafeHTTPGet(context.Background(), "http://public.test/x", 1<<20)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if string(body) != "final" {
		t.Errorf("body=%q (want final)", string(body))
	}
}

func TestSafeHTTPGet_TruncatesOversizedBody(t *testing.T) {
	const body = "0123456789ABCDEFGHIJ" // 20 bytes
	const cap int64 = 5
	g := &Guard{
		Resolver:  fakeHostIP(map[string][]net.IP{"public.test": {net.ParseIP("8.8.8.8")}}),
		Transport: &fakeRoundTrip{handler: func(*http.Request) *http.Response { return newResponse(200, body, nil) }},
	}
	got, err := g.SafeHTTPGet(context.Background(), "http://public.test/x", cap)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if len(got) != int(cap) {
		t.Errorf("len=%d (want %d)", len(got), cap)
	}
}

func TestSafeHTTPGet_RejectsEmptyBody(t *testing.T) {
	g := &Guard{
		Resolver:  fakeHostIP(map[string][]net.IP{"public.test": {net.ParseIP("8.8.8.8")}}),
		Transport: &fakeRoundTrip{handler: func(*http.Request) *http.Response { return newResponse(200, "", nil) }},
	}
	if _, err := g.SafeHTTPGet(context.Background(), "http://public.test/x", 1<<20); err == nil {
		t.Error("empty body should be rejected")
	}
}

func TestSafeHTTPGet_NilGuardReturnsError(t *testing.T) {
	var g *Guard
	if _, err := g.SafeHTTPGet(context.Background(), "http://x/", 1<<20); err == nil {
		t.Error("nil guard should produce an error, not panic")
	}
}

func TestSafeHTTPGet_RoundTripHandlerReturningNilErrors(t *testing.T) {
	// The fakeRoundTrip's handler returns nil → fakeRoundTrip returns an
	// error → client.Do returns it → SafeHTTPGet wraps it. This pins the
	// guardrail that a RoundTrip-level failure (transport returns no
	// response, e.g. real TLS handshake failure) doesn't silently produce
	// an empty-body success.
	g := &Guard{
		Resolver:  fakeHostIP(map[string][]net.IP{"public.test": {net.ParseIP("8.8.8.8")}}),
		Transport: &fakeRoundTrip{handler: func(*http.Request) *http.Response { return nil }},
	}
	_, err := g.SafeHTTPGet(context.Background(), "http://public.test/x", 1<<20)
	if err == nil {
		t.Fatal("RoundTrip returning an error should propagate through SafeHTTPGet")
	}
}
