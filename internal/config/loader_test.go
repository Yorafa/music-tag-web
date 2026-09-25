package config

import (
	"os"
	"path/filepath"
	"testing"
)

// TestLoadSourceOverrides_Success drives the happy path: two well-formed
// YAML files, both parsed, both loaded by Name field.
func TestLoadSourceOverrides_Success(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "kuwo.yaml"), `
name: kuwo
api_base: "https://mirror.example.com"
secrets:
  kuwoSecret: "rotated-2026"
`)
	mustWrite(t, filepath.Join(dir, "kg.yaml"), `
name: kg
api_base: ""
`)

	got, err := LoadSourceOverrides(dir)
	if err != nil {
		t.Fatalf("LoadSourceOverrides: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 entries, got %d: %+v", len(got), got)
	}
	if got["kuwo"].APIBase != "https://mirror.example.com" {
		t.Errorf("kuwo api_base mismatch: %q", got["kuwo"].APIBase)
	}
	if got["kuwo"].Secrets["kuwoSecret"] != "rotated-2026" {
		t.Errorf("kuwo secret mismatch: %q", got["kuwo"].Secrets["kuwoSecret"])
	}
	if got["kg"].APIBase != "" {
		t.Errorf("kg api_base should be empty: %q", got["kg"].APIBase)
	}
}

// TestLoadSourceOverrides_MissingDir returns empty map (no error) when
// the dir doesn't exist — operators may not have created any overrides
// yet, and that's a benign state.
func TestLoadSourceOverrides_MissingDir(t *testing.T) {
	got, err := LoadSourceOverrides(filepath.Join(t.TempDir(), "does-not-exist"))
	if err != nil {
		t.Fatalf("missing dir should be benign, got error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("want empty map, got: %+v", got)
	}
}

// TestLoadSourceOverrides_InvalidYamlQuarantines ensures a bad YAML file
// is moved aside to .yaml.bak and NOT included in the returned map.
// Operator can re-read the .bak to see what failed.
func TestLoadSourceOverrides_InvalidYamlQuarantines(t *testing.T) {
	dir := t.TempDir()
	badPath := filepath.Join(dir, "broken.yaml")
	mustWrite(t, badPath, "::not yaml::\n  - [unterminated")

	got, err := LoadSourceOverrides(dir)
	if err != nil {
		t.Fatalf("LoadSourceOverrides: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("want empty map, got: %+v", got)
	}
	if _, err := os.Stat(badPath + ".bak"); err != nil {
		t.Errorf("expected quarantine to %s.bak: %v", badPath, err)
	}
	if _, err := os.Stat(badPath); !os.IsNotExist(err) {
		t.Errorf("expected broken.yaml to be moved aside, stat err=%v", err)
	}
}

// TestLoadSourceOverrides_NameFallbackFromFilename covers the case where
// yaml.name is missing: the loader falls back to the filename stem so
// operators don't have to be pedantic.
func TestLoadSourceOverrides_NameFallbackFromFilename(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "qmusic.yaml"), `
api_base: "https://qq.mirror.example.com"
`)

	got, err := LoadSourceOverrides(dir)
	if err != nil {
		t.Fatalf("LoadSourceOverrides: %v", err)
	}
	if got["qmusic"].Name != "qmusic" {
		t.Errorf("name fallback mismatch: %q", got["qmusic"].Name)
	}
	if got["qmusic"].APIBase != "https://qq.mirror.example.com" {
		t.Errorf("api_base mismatch: %q", got["qmusic"].APIBase)
	}
}

// TestLoadSourceOverrides_SecretsMultiKey covers a plugin that may have
// multiple secret entries (current C.4 schema only reads <name>Secret,
// but the loader preserves every key verbatim so future schema
// extensions can add e.g. userId/keyPath without changing the loader).
func TestLoadSourceOverrides_SecretsMultiKey(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "kuwo.yaml"), `
name: kuwo
secrets:
  kuwoSecret: "secret1"
  futureKey: "secret2"
`)
	got, err := LoadSourceOverrides(dir)
	if err != nil {
		t.Fatalf("LoadSourceOverrides: %v", err)
	}
	if got["kuwo"].Secrets["kuwoSecret"] != "secret1" {
		t.Errorf("kuwoSecret mismatch: %q", got["kuwo"].Secrets["kuwoSecret"])
	}
	if got["kuwo"].Secrets["futureKey"] != "secret2" {
		t.Errorf("futureKey mismatch: %q", got["kuwo"].Secrets["futureKey"])
	}
}

// mustWrite is a tiny helper: writes content or fatal-fails the test.
func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
