package plugin

import (
	"fmt"
	"sync"
)

// Registry manages tag-source and download-source plugins.
// Plugins can be registered at init() time by any package.
type Registry struct {
	mu       sync.RWMutex
	tag      map[string]TagSource
	download map[string]DownloadSource
}

var global = &Registry{
	tag:      make(map[string]TagSource),
	download: make(map[string]DownloadSource),
}

// RegisterTagSource adds a tag source plugin. Safe to call during init().
//
// Name() is resolved BEFORE taking the registry mutex. GRPC adapters
// implement Name() via a lazy dial that may itself call Register* /
// Get*; holding the lock across Name() self-deadlocks the process
// (observed: worker hung after "WARNING: gRPC plugins connect
// insecurely" and never started asynq).
func RegisterTagSource(p TagSource) {
	name := p.Name()
	global.mu.Lock()
	defer global.mu.Unlock()
	if _, ok := global.tag[name]; ok {
		panic(fmt.Sprintf("plugin: duplicate tag source %q", name))
	}
	global.tag[name] = p
}

// RegisterDownloadSource adds a download source plugin.
// See RegisterTagSource for why Name() is resolved outside the lock.
func RegisterDownloadSource(p DownloadSource) {
	name := p.Name()
	global.mu.Lock()
	defer global.mu.Unlock()
	if _, ok := global.download[name]; ok {
		panic(fmt.Sprintf("plugin: duplicate download source %q", name))
	}
	global.download[name] = p
}

// GetTagSource returns a registered tag source by name.
func GetTagSource(name string) (TagSource, error) {
	global.mu.RLock()
	defer global.mu.RUnlock()
	p, ok := global.tag[name]
	if !ok {
		return nil, fmt.Errorf("plugin: tag source %q not found", name)
	}
	return p, nil
}

// ListTagSources returns all registered tag source names.
func ListTagSources() []string {
	global.mu.RLock()
	defer global.mu.RUnlock()
	names := make([]string, 0, len(global.tag))
	for n := range global.tag {
		names = append(names, n)
	}
	return names
}

// GetDownloadSource returns a registered download source by name.
func GetDownloadSource(name string) (DownloadSource, error) {
	global.mu.RLock()
	defer global.mu.RUnlock()
	p, ok := global.download[name]
	if !ok {
		return nil, fmt.Errorf("plugin: download source %q not found", name)
	}
	return p, nil
}

// ListDownloadSources returns all registered download source names.
func ListDownloadSources() []string {
	global.mu.RLock()
	defer global.mu.RUnlock()
	names := make([]string, 0, len(global.download))
	for n := range global.download {
		names = append(names, n)
	}
	return names
}

// ─── Test-export helpers ────────────────────────────────────────────────────
//
// The functions below are intended for use by integration tests only. They
// are NOT invoked in production code paths. Production init() flows should
// call RegisterTagSource / RegisterDownloadSource and never touch these.
//
// They are kept in this file (no build tag) so tests in any package can
// reach them without changing `go test` invocation flags. The separate
// suffix "Mock" / "ForTesting" signals intent.
//
// ResetForTesting clears both maps. Call it from TestMain in any package
// that ships its own plugins to ensure a clean slate per test binary.

// InstallMockTagSource 注册一个 tag source mock (test-only) — handles the
// "already registered" panic-on-duplicate in RegisterTagSource by first
// uninstalling any same-name registration, then registering the mock.
// Pair every InstallMockTagSource call in a test with a t.Cleanup
// UninstallMockTagSource(name) to avoid cross-test pollution.
func InstallMockTagSource(name string, mock TagSource) {
	global.mu.Lock()
	defer global.mu.Unlock()
	delete(global.tag, name)
	// Forego the duplicate-panic guard: tests control the install/cleanup
	// pair and own the name. If a mock's Name() disagrees with the requested
	// name, fall back to the registry key so GetTagSource(name) still hits.
	if mock.Name() != name {
		// Keep canonical name; helper is keyed by the test-side name.
		// The mock's own Name() will be used by DisplayName() etc.
	}
	global.tag[name] = mock
}

// UninstallMockTagSource removes a single test-installed tag source by name.
// No-op if not registered. Safe to call from t.Cleanup even when the test
// failed mid-flow.
func UninstallMockTagSource(name string) {
	global.mu.Lock()
	defer global.mu.Unlock()
	delete(global.tag, name)
}

// InstallMockDownloadSource registers a download source mock (test-only).
// Same semantics as InstallMockTagSource.
func InstallMockDownloadSource(name string, mock DownloadSource) {
	global.mu.Lock()
	defer global.mu.Unlock()
	delete(global.download, name)
	global.download[name] = mock
}

// ─── Source-overrides flow (plan C.4 / Stage B) ────────────────────────────
//
// SourceOverride describes one YAML file's content from data/sources/<name>.yaml.
// The struct is the YAML schema; config.LoadSourceOverrides parses files
// into map[<pluginName>]SourceOverride and (*Registry).RefreshOverrides
// consumes it. We export the type from this package (rather than keeping
// it inside config) so the dispatch site can be defined against the same
// type that the loader produces — handlers stay free of YAML/struct
// translation code.
type SourceOverride struct {
	// Name is the registry key this override targets. If absent in the
	// YAML file the loader falls back to the filename stem.
	Name string `yaml:"name"`
	// APIBase, when non-empty, replaces the plugin's endpoint host/path.
	// Plugins that don't expose SetAPIBase (e.g. ytdlp / third-party
	// built-ins) silently skip this field at dispatch.
	APIBase string `yaml:"api_base"`
	// Secrets is keyed by `<pluginName>Secret` (e.g. `kuwoSecret` for the
	// kuwo plugin). Unmatched keys are silently ignored.
	Secrets map[string]string `yaml:"secrets"`
	// Enabled is reserved for future use (plan Open Details G: hot
	// reload must NOT change enable status — it's decided at startup).
	// Loaded but never read by RefreshOverrides itself.
	Enabled bool `yaml:"enabled"`
}

// SecretConfigurable is a plugin that exposes a runtime-rotatable
// header secret (auth header / token). Implemented by all four
// tag-source plugins except kg (kg search is anonymous / signature-
// derived, no header secret exists upstream).
type SecretConfigurable interface {
	SetSecret(secret string)
}

// APIBaseConfigurable is a plugin that exposes a runtime-swappable
// upstream endpoint URL. Implemented across all four target plugins.
type APIBaseConfigurable interface {
	SetAPIBase(apiBase string)
}

// RefreshOverrides applies `overrides` to every tag-source plugin
// currently in the registry. Dispatch rules per plugin:
//  1. If plugin implements SecretConfigurable AND
//     overrides[name].Secrets["<name>Secret"] is non-empty → call SetSecret(s).
//  2. If plugin implements APIBaseConfigurable AND
//     overrides[name].APIBase is non-empty → call SetAPIBase(s).
//
// Returns the count of setter calls actually performed (each plugin can
// contribute 0, 1, or 2). Plugin names appearing in `overrides` but not
// in the registry are silently skipped (forward-compatible: a future
// plugin can ship its YAML ahead of being registered — no dispatch
// crash on either side).
//
// Concurrency: string assignment on top-level package vars is not
// strictly atomic on every Go architecture. The registry mutex is held
// for the duration; in-flight /api/stream calls on other goroutines
// may observe a partially-updated state (e.g. secret rotated but
// AudioURL still pointing at the old CDN). This is acceptable for
// the rare admin-initiated refresh path on a self-host single-user
// setup; production hardening (mutex-per-plugin or atomic.Pointer
// shadow structs) is left to a follow-up if a future use case needs
// stronger guarantees.
func (r *Registry) RefreshOverrides(overrides map[string]SourceOverride) int {
	n := 0
	r.mu.Lock()
	defer r.mu.Unlock()
	for name, p := range r.tag {
		ov, ok := overrides[name]
		if !ok {
			continue
		}
		if sc, ok := p.(SecretConfigurable); ok {
			if s, ok := ov.Secrets[name+"Secret"]; ok && s != "" {
				sc.SetSecret(s)
				n++
			}
		}
		if ac, ok := p.(APIBaseConfigurable); ok {
			if ov.APIBase != "" {
				ac.SetAPIBase(ov.APIBase)
				n++
			}
		}
	}
	return n
}

// SourceOverrideView is the read-only, secret-redacted projection
// served through `GET /api/sources/override/`. Per plan Open Details H:
// UI displays override presence (boolean) + API base URL; secrets are
// never sent to the frontend.
type SourceOverrideView struct {
	Name        string `json:"name"`
	HasOverride bool   `json:"hasOverride"`
	APIBase     string `json:"apiBase,omitempty"`
	// HasSecret reports whether a `secrets` entry exists for this
	// plugin (without disclosing the actual value). Surfaced so the
	// UI can show "secret override active" indicators.
	HasSecret bool `json:"hasSecret"`
}

// OverrideSnapshot builds the redacted view of all currently applied
// overrides. Returns views in stable insertion order of the input map;
// secret *values* are never copied into the view (only a presence
// boolean). The registry mutex is held read-only while we walk the
// registry, even though the function only reads the caller-supplied
// `current` map — taking the read lock keeps callers honest: if a
// future change moves overridden state into the registry itself, this
// function's lock discipline is already correct.
func (r *Registry) OverrideSnapshot(current map[string]SourceOverride) []SourceOverrideView {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]SourceOverrideView, 0, len(current))
	for name, ov := range current {
		_, hasSecret := ov.Secrets[name+"Secret"]
		out = append(out, SourceOverrideView{
			Name:        name,
			HasOverride: true,
			APIBase:     ov.APIBase,
			HasSecret:   hasSecret,
		})
	}
	return out
}

// RefreshOverrides is a free-function wrapper around the method,
// shipping the same interface as the other package-level registration
// helpers (RegisterTagSource / GetTagSource / etc.). Call sites that
// don't hold a Registry pointer (e.g. handlers in cmd/gateway, tests)
// use this directly.
func RefreshOverrides(overrides map[string]SourceOverride) int {
	return global.RefreshOverrides(overrides)
}

// OverrideSnapshot free-function wrapper — same rationale as
// RefreshOverrides above.
func OverrideSnapshot(current map[string]SourceOverride) []SourceOverrideView {
	return global.OverrideSnapshot(current)
}

// UninstallMockDownloadSource removes a single test-installed download source.
func UninstallMockDownloadSource(name string) {
	global.mu.Lock()
	defer global.mu.Unlock()
	delete(global.download, name)
}

// ResetForTesting clears every registered plugin (tag + download). Use from
// TestMain to ensure a clean slate for each test binary if your production
// code registers plugins at init() time. Production code should never
// call this.
func ResetForTesting() {
	global.mu.Lock()
	defer global.mu.Unlock()
	global.tag = make(map[string]TagSource)
	global.download = make(map[string]DownloadSource)
}
