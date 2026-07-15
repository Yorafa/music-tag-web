package plugin

import (
	"fmt"
	"sync"
)

// Registry manages tag-source and download-source plugins.
// Plugins can be registered at init() time by any package.
type Registry struct {
	mu        sync.RWMutex
	tag       map[string]TagSource
	download  map[string]DownloadSource
}

var global = &Registry{
	tag:      make(map[string]TagSource),
	download: make(map[string]DownloadSource),
}

// RegisterTagSource adds a tag source plugin. Safe to call during init().
func RegisterTagSource(p TagSource) {
	global.mu.Lock()
	defer global.mu.Unlock()
	name := p.Name()
	if _, ok := global.tag[name]; ok {
		panic(fmt.Sprintf("plugin: duplicate tag source %q", name))
	}
	global.tag[name] = p
}

// RegisterDownloadSource adds a download source plugin.
func RegisterDownloadSource(p DownloadSource) {
	global.mu.Lock()
	defer global.mu.Unlock()
	name := p.Name()
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
