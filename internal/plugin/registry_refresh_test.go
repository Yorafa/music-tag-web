package plugin

import (
	"context"
	"testing"
)

// baseFake is a minimal TagSource WITHOUT SetSecret / SetAPIBase.
// Tests compose by embedding baseFake into struct types that add the
// respective setter methods, so that a `p.(SecretConfigurable)` type
// assertion fails iff the embedding struct didn't add SetSecret, and
// likewise for APIBaseConfigurable.
//
// This mirrors the real-plugin topology: kg has only SetAPIBase (no
// SetSecret method on its *Server), migu has only SetAPIBase, etc.
// We avoid the more obvious "always have both, gate via runtime flag"
// pattern because Go's method-set semantics would always satisfy the
// interface assertion regardless of a runtime boolean.
type baseFake struct {
	name        string
	displayName string

	// Tracking counters reachable via embedded pointers; the
	// embedding structs call these directly when their own setter is
	// invoked.
	secretCalls  int
	secretLast   string
	apiBaseCalls int
	apiBaseLast  string
}

func (b *baseFake) Name() string           { return b.name }
func (b *baseFake) DisplayName() string    { return b.displayName }
func (b *baseFake) SupportsSearch() bool   { return false }
func (b *baseFake) SupportsLyric() bool    { return false }
func (b *baseFake) SupportsId3() bool      { return false }
func (b *baseFake) SupportsAudioURL() bool { return false }
func (b *baseFake) Search(_ context.Context, _ string, _, _ int) (*SearchResult, error) {
	return &SearchResult{}, nil
}
func (b *baseFake) FetchID3ByTitle(_ context.Context, _ string) ([]Song, error) {
	return nil, nil
}
func (b *baseFake) FetchLyric(_ context.Context, _ string) (string, error) {
	return "", nil
}
func (b *baseFake) GetAudioURL(_ context.Context, _ string) (string, error) {
	return "", nil
}

// fakeWithSecret: implements SecretConfigurable on top of baseFake.
type fakeWithSecret struct {
	*baseFake
}

func (f *fakeWithSecret) SetSecret(s string) {
	f.secretCalls++
	f.secretLast = s
}

// fakeWithAPIBase: implements APIBaseConfigurable on top of baseFake.
// Mirrors the kg / migu / qmusic reality (no upstream Secret header).
type fakeWithAPIBase struct {
	*baseFake
}

func (f *fakeWithAPIBase) SetAPIBase(s string) {
	f.apiBaseCalls++
	f.apiBaseLast = s
}

// fakeWithBoth: implements BOTH setters, mirroring kuwo (which carries
// both an HTTP `Secret` header and an api_base URL).
type fakeWithBoth struct {
	*baseFake
}

func (f *fakeWithBoth) SetSecret(s string) {
	f.secretCalls++
	f.secretLast = s
}

func (f *fakeWithBoth) SetAPIBase(s string) {
	f.apiBaseCalls++
	f.apiBaseLast = s
}

// freshRegistry constructs an isolated Registry — useful so each test
// starts from an empty tag map without depending on the package-global
// global registered state.
func freshRegistry() *Registry {
	return &Registry{tag: make(map[string]TagSource)}
}

// TestRefreshOverrides_DispatchesBothSetters: a plugin implementing
// BOTH SetSecret and SetAPIBase gets both called exactly once with
// the override values. Covers kuwo.
func TestRefreshOverrides_DispatchesBothSetters(t *testing.T) {
	r := freshRegistry()
	mock := &fakeWithBoth{&baseFake{name: "kuwo", displayName: "酷我音乐"}}
	r.tag["kuwo"] = mock

	overrides := map[string]SourceOverride{
		"kuwo": {
			Name:    "kuwo",
			APIBase: "https://mirror.example.com",
			Secrets: map[string]string{"kuwoSecret": "rotated-2026"},
		},
	}
	n := r.RefreshOverrides(overrides)
	if n != 2 {
		t.Errorf("want n=2 (both setters), got %d", n)
	}
	if mock.secretCalls != 1 || mock.secretLast != "rotated-2026" {
		t.Errorf("SetSecret: calls=%d lastValue=%q", mock.secretCalls, mock.secretLast)
	}
	if mock.apiBaseCalls != 1 || mock.apiBaseLast != "https://mirror.example.com" {
		t.Errorf("SetAPIBase: calls=%d lastValue=%q", mock.apiBaseCalls, mock.apiBaseLast)
	}
}

// TestRefreshOverrides_FreeFnUsesGlobal: the package-level RefreshOverrides
// helper delegates to the package-global registry (NOT to a caller-provided
// one). Pair InstallMockTagSource with a Cleanup that uninstalls to keep
// global clean between tests.
func TestRefreshOverrides_FreeFnUsesGlobal(t *testing.T) {
	// Reuse the canonical "kuwo" plugin name; the lookup key in
	// override.Secrets is then "<name>Secret" = "kuwoSecret".
	mock := &fakeWithBoth{&baseFake{name: "kuwo", displayName: "酷我-glob"}}
	InstallMockTagSource("kuwo", mock)
	t.Cleanup(func() { UninstallMockTagSource("kuwo") })

	overrides := map[string]SourceOverride{
		"kuwo": {
			Name:    "kuwo",
			APIBase: "https://mirror.example.com",
			Secrets: map[string]string{"kuwoSecret": "rotated-2026"},
		},
	}
	n := RefreshOverrides(overrides)
	if n != 2 {
		t.Errorf("free-fn want n=2, got %d", n)
	}
	if mock.secretCalls != 1 || mock.apiBaseCalls != 1 {
		t.Errorf("free-fn dispatches: secret=%d apiBase=%d", mock.secretCalls, mock.apiBaseCalls)
	}
}

// TestRefreshOverrides_APIBaseOnly: a plugin implementing ONLY
// APIBaseConfigurable (kg / migu / qmusic) gets SetAPIBase called.
// Even when the override carries a Secrets.<name>Secret entry, it's
// silently ignored because the type assertion fails.
func TestRefreshOverrides_APIBaseOnly(t *testing.T) {
	r := freshRegistry()
	mock := &fakeWithAPIBase{&baseFake{name: "kg", displayName: "酷狗"}}
	r.tag["kg"] = mock

	overrides := map[string]SourceOverride{
		"kg": {
			Name:    "kg",
			APIBase: "https://kg.mirror.example.com",
			Secrets: map[string]string{"kgSecret": "should-be-ignored"},
		},
	}
	n := r.RefreshOverrides(overrides)
	if n != 1 {
		t.Errorf("want n=1 (only SetAPIBase), got %d", n)
	}
	if mock.apiBaseCalls != 1 {
		t.Errorf("SetAPIBase call count: %d", mock.apiBaseCalls)
	}
	if mock.secretCalls != 0 {
		t.Errorf("SetSecret should NOT be called: %d", mock.secretCalls)
	}
}

// TestRefreshOverrides_UnknownNameSilentlySkipped: forward-compat case.
// Override referencing a plugin name not in the registry must NOT crash.
func TestRefreshOverrides_UnknownNameSilentlySkipped(t *testing.T) {
	r := freshRegistry()
	r.tag["kuwo"] = &fakeWithBoth{&baseFake{name: "kuwo", displayName: "酷我"}}

	overrides := map[string]SourceOverride{
		"kuwo":           {Name: "kuwo", APIBase: "https://k.example.com"},
		"unknown-future": {Name: "unknown-future", APIBase: "https://x.example.com"},
	}
	n := r.RefreshOverrides(overrides)
	if n != 1 {
		t.Errorf("want n=1 (only kuwo dispatched), got %d", n)
	}
}

// TestRefreshOverrides_EmptyValuesSkipSetters: empty APIBase AND empty
// secret string both mean "do not override"; neither setter invoked.
func TestRefreshOverrides_EmptyValuesSkipSetters(t *testing.T) {
	r := freshRegistry()
	mock := &fakeWithBoth{&baseFake{name: "kuwo", displayName: "酷我"}}
	r.tag["kuwo"] = mock

	overrides := map[string]SourceOverride{
		"kuwo": {
			Name:    "kuwo",
			APIBase: "",
			Secrets: map[string]string{"kuwoSecret": ""},
		},
	}
	n := r.RefreshOverrides(overrides)
	if n != 0 {
		t.Errorf("want n=0, got %d", n)
	}
	if mock.apiBaseCalls != 0 || mock.secretCalls != 0 {
		t.Errorf("expected zero setter calls, got apiBase=%d secret=%d",
			mock.apiBaseCalls, mock.secretCalls)
	}
}

// TestOverrideSnapshot_RedactsSecrets: the read-only view MUST NOT
// carry the secret value; only HasSecret (presence boolean).
// Plan Open Details H.
func TestOverrideSnapshot_RedactsSecrets(t *testing.T) {
	r := freshRegistry()
	cur := map[string]SourceOverride{
		"kuwo": {
			Name:    "kuwo",
			APIBase: "https://k.example.com",
			Secrets: map[string]string{"kuwoSecret": "DO-NOT-LEAK"},
		},
	}
	views := r.OverrideSnapshot(cur)
	if len(views) != 1 {
		t.Fatalf("want 1 view, got %d", len(views))
	}
	v := views[0]
	if v.Name != "kuwo" {
		t.Errorf("view.Name: %q", v.Name)
	}
	if !v.HasOverride {
		t.Errorf("HasOverride should be true")
	}
	if v.APIBase != "https://k.example.com" {
		t.Errorf("APIBase should be exposed: %q", v.APIBase)
	}
	if !v.HasSecret {
		t.Errorf("HasSecret should be true (presence, not value)")
	}
	// Defense-in-depth: no field carries the raw secret value.
	if v.Name == "DO-NOT-LEAK" || v.APIBase == "DO-NOT-LEAK" {
		t.Errorf("secret value leaked: %+v", v)
	}
}
