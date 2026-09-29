package plugin

import (
	"fmt"
	"net/url"
	"strings"
)

// ValidateAPIBase checks a runtime-supplied upstream base URL before a
// plugin adopts it.
//
// The four target plugins (kg / kuwo / migu / qmusic) each expose
// SetAPIBase, which assigns the value straight into a package-level var
// that every subsequent request is built from. With no validation, an
// `api_base: http://attacker/` in a sources override file would silently
// downgrade the plugin's connection to plaintext AND redirect every
// search / audio-URL resolution through a host of the caller's choosing —
// a full interception of both query and response.
//
// Rules:
//   - the URL must parse and carry a scheme;
//   - the scheme must be https.
//
// http is rejected rather than downgraded-with-a-warning on purpose. These
// are third-party music APIs, not internal mirrors; there is no deployment
// where reaching them over plaintext is the intended configuration, and
// silently accepting it is exactly the failure this guards against. An
// operator who genuinely needs http can point at a local reverse proxy
// that terminates https.
//
// A host of the form "api.example.com" (no scheme) is also rejected: the
// old callers concatenated this value directly onto a path, so a schemeless
// value would have produced a relative request. Requiring the explicit
// scheme surfaces that mistake at the point of configuration rather than as
// a confusing transport error later.
func ValidateAPIBase(raw string) error {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return fmt.Errorf("plugin: api base is empty")
	}
	u, err := url.Parse(trimmed)
	if err != nil {
		return fmt.Errorf("plugin: api base %q is not a valid URL: %w", raw, err)
	}
	if u.Scheme == "" {
		return fmt.Errorf("plugin: api base %q has no scheme (want https://host/...)", raw)
	}
	if u.Scheme != "https" {
		return fmt.Errorf("plugin: api base %q uses scheme %q; only https is allowed", raw, u.Scheme)
	}
	if u.Host == "" {
		return fmt.Errorf("plugin: api base %q has no host", raw)
	}
	return nil
}
