// SafeHTTPGet — fetch helper for callers that need bytes from a
// remote URL protected by netguard.Guard. Wraps http.Client with:
//
//   (1) initial URL validation (scheme + resolved IPs)
//   (2) per-redirect URL validation (re-validates Location at every hop)
//   (3) a hard response-body cap (maxBytes)
//
// Content validation (image.DecodeConfig, JSON safety, etc.) stays with
// the caller — netguard intentionally only knows about shapes a remote
// proxy could abuse (URL, redirect chain, response size).
//
// Test seam: Guard.Transport defaults to http.DefaultTransport. Override
// it in unit tests to inject a fake RoundTripper pointing at httptest
// servers without driving the real network stack.
package netguard

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"
)

// SafeHTTPGet fetches rawURL with SSRF + redirect-pivot protection. The
// body is read up to maxBytes (caller-supplied; pick a value appropriate
// to the expected payload).
func (g *Guard) SafeHTTPGet(ctx context.Context, rawURL string, maxBytes int64) ([]byte, error) {
	if g == nil {
		return nil, fmt.Errorf("netguard: nil guard")
	}
	if err := g.Validate(ctx, rawURL); err != nil {
		return nil, fmt.Errorf("netguard: validate: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("netguard: build request: %w", err)
	}
	client := &http.Client{
		Timeout:   10 * time.Second,
		Transport: g.Transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			// Re-validate every redirect target so a public → private
			// pivot is rejected at hop time, not just at initial dial.
			if err := g.Validate(req.Context(), req.URL.String()); err != nil {
				return err
			}
			return nil
		},
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("netguard: do: %w", err)
	}
	defer resp.Body.Close()
	buf, _ := io.ReadAll(io.LimitReader(resp.Body, maxBytes))
	if len(buf) == 0 {
		return nil, fmt.Errorf("netguard: empty response body")
	}
	return buf, nil
}
