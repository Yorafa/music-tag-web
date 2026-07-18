package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// withTempBootstrap swaps the package-level bootstrapPath to a fresh
// temp file for the duration of the test. Resets env on cleanup. The
// function returns the new path so tests can inspect it.
func withTempBootstrap(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, ".bootstrap-creds")
	orig := bootstrapPath
	bootstrapPath = p
	t.Cleanup(func() { bootstrapPath = orig })
	return p
}

// withCleanEnv clears all five bootstrap-relevant env vars for the test
// scope so one test can't leak to the next via process state.
func withCleanEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{envJWTSecret, envAdminUsers, envWebhookInternalToken, envAllowInsecure} {
		orig, had := os.LookupEnv(k)
		os.Unsetenv(k)
		t.Cleanup(func() {
			if had {
				os.Setenv(k, orig)
			} else {
				os.Unsetenv(k)
			}
		})
	}
}

// ─── makeBootstrapCreds ──────────────────────────────────────────────────

func TestEnsureBootstrap_GeneratesFreshOnFirstBoot(t *testing.T) {
	withCleanEnv(t)
	p := withTempBootstrap(t)

	secrets := loadOrGenerate()

	if secrets.Version != bootstrapVersion {
		t.Errorf("Version = %d, want %d", secrets.Version, bootstrapVersion)
	}
	if len(secrets.JWTSecret) < 32 {
		t.Errorf("JWTSecret length %d too short", len(secrets.JWTSecret))
	}
	if len(secrets.AdminPasswordPlain) < 8 {
		t.Errorf("AdminPasswordPlain length %d too short", len(secrets.AdminPasswordPlain))
	}
	if len(secrets.WebhookToken) != 64 { // 32 bytes hex
		t.Errorf("WebhookToken length %d, want 64 (hex of 32 bytes)", len(secrets.WebhookToken))
	}

	// File must actually exist on disk.
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("bootstrap file not written: %v", err)
	}
	var roundtrip bootstrapCreds
	if err := json.Unmarshal(data, &roundtrip); err != nil {
		t.Fatalf("malformed json on disk: %v", err)
	}
	if roundtrip.JWTSecret != secrets.JWTSecret {
		t.Errorf("file contents diverged from in-memory secrets")
	}
}

func TestEnsureBootstrap_ReusesExistingFile(t *testing.T) {
	withCleanEnv(t)
	p := withTempBootstrap(t)

	first := loadOrGenerate()
	second := loadOrGenerate()

	if first.JWTSecret != second.JWTSecret {
		t.Errorf("repeated loadOrGenerate regenerated instead of reusing")
	}
	// File on disk should still be parseable and identical to `second`.
	data, _ := os.ReadFile(p)
	var onDisk bootstrapCreds
	_ = json.Unmarshal(data, &onDisk)
	if onDisk.JWTSecret != second.JWTSecret || onDisk.WebhookToken != second.WebhookToken {
		t.Errorf("disk drift after second loadOrGenerate")
	}
}

func TestEnsureBootstrap_RecoversFromCorruptFile(t *testing.T) {
	withCleanEnv(t)
	p := withTempBootstrap(t)
	if err := os.WriteFile(p, []byte("not json at all"), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	c := loadOrGenerate()
	if c.JWTSecret == "" {
		t.Errorf("corrupt file should trigger regenerate, got empty secrets")
	}
}

func TestEnsureBootstrap_RecoversFromVersionDrift(t *testing.T) {
	withCleanEnv(t)
	p := withTempBootstrap(t)
	bad := bootstrapCreds{Version: 999, JWTSecret: "junk", AdminUser: "admin", WebhookToken: "tok"}
	data, _ := json.Marshal(bad)
	if err := os.WriteFile(p, data, 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	c := loadOrGenerate()
	if c.Version != bootstrapVersion {
		t.Errorf("version-drift file should be regenerated; still got v%d", c.Version)
	}
	if c.JWTSecret == "junk" {
		t.Errorf("regenerated creds reused stale value")
	}
}

// ─── ensureBootstrap ──────────────────────────────────────────────────────

func TestEnsureBootstrap_FillsAllThreeEnvVars(t *testing.T) {
	withCleanEnv(t)
	withTempBootstrap(t)

	ensureBootstrap()

	for _, key := range []string{envJWTSecret, envAdminUsers, envWebhookInternalToken} {
		if os.Getenv(key) == "" {
			t.Errorf("%s not filled by ensureBootstrap", key)
		}
	}
	if !strings.HasPrefix(os.Getenv(envAdminUsers), "admin:") {
		t.Errorf("ADMIN_USERS should start with 'admin:' (auto-bootstrap default user); got %q",
			os.Getenv(envAdminUsers))
	}
	// bcrypt hash starts with $2 (so containsPlaceholderAdminPair returns false).
	admin := os.Getenv(envAdminUsers)
	if !strings.HasPrefix(admin, "admin:$2") {
		t.Errorf("ADMIN_USERS password should be a bcrypt hash (starts with $2); got prefix %q",
			strings.SplitN(admin, ":", 2)[1][:min(8, len(admin))])
	}
}

func TestEnsureBootstrap_KeepsExistingRealValues(t *testing.T) {
	withCleanEnv(t)
	withTempBootstrap(t)

	const realSecret = "i-am-real-secret-not-from-bootstrap-file"
	os.Setenv(envJWTSecret, realSecret)
	const realAdmin = "alice:$2a$10$LJ6V4VuZ7C9kQqgZQ4N5uOq5AKAbekkqZZBvZ9Qq7L5GY6a6fZLbeW"
	os.Setenv(envAdminUsers, realAdmin)
	const realHook = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	os.Setenv(envWebhookInternalToken, realHook)

	ensureBootstrap()

	if got := os.Getenv(envJWTSecret); got != realSecret {
		t.Errorf("ensureBootstrap clobbered real JWT_SECRET: %q vs %q", got, realSecret)
	}
	if got := os.Getenv(envAdminUsers); got != realAdmin {
		t.Errorf("ensureBootstrap clobbered real ADMIN_USERS")
	}
	if got := os.Getenv(envWebhookInternalToken); got != realHook {
		t.Errorf("ensureBootstrap clobbered real webhook token")
	}
}

func TestEnsureBootstrap_OverridesExamplePlaceholder(t *testing.T) {
	withCleanEnv(t)
	withTempBootstrap(t)

	os.Setenv(envJWTSecret, "__REPLACE_ME__")
	os.Setenv(envAdminUsers, "admin:__REPLACE_ME__")
	os.Setenv(envWebhookInternalToken, "__REPLACE_ME__")

	ensureBootstrap()

	if os.Getenv(envJWTSecret) == "__REPLACE_ME__" {
		t.Errorf("JWT_SECRET still placeholder after ensureBootstrap")
	}
	if os.Getenv(envJWTSecret) == "change-me-in-production" {
		t.Errorf("JWT_SECRET still defaultJWTSecret after ensureBootstrap")
	}
	if strings.Contains(os.Getenv(envAdminUsers), "__REPLACE_ME__") {
		t.Errorf("ADMIN_USERS still contains placeholder: %q", os.Getenv(envAdminUsers))
	}
}

func TestEnsureBootstrap_FillsCompoundPlaceholderAdminPair(t *testing.T) {
	withCleanEnv(t)
	withTempBootstrap(t)

	// alice is real, bob has placeholder password — only if bcrypt-aware
	// path catches this would it parse. We use plaintext pair with a
	// compound case (placeholder as second pair), which is enough for
	// containsPlaceholderAdminPair to return true.
	os.Setenv(envAdminUsers, "alice:realpwd,bob:__REPLACE_ME__")

	ensureBootstrap()

	if strings.Contains(os.Getenv(envAdminUsers), "__REPLACE_ME__") {
		t.Errorf("compound placeholder pair should be replaced wholesale: %q",
			os.Getenv(envAdminUsers))
	}
}

// ─── isBootstrapTarget ───────────────────────────────────────────────────

func TestIsBootstrapTarget(t *testing.T) {
	cases := map[string]bool{
		"":                          true,
		"__REPLACE_ME__":            true,
		"  __REPLACE_ME__  ":        true,
		"CHANGEME":                  true,
		"TODO":                      true,
		"change-me-in-production":   true,
		"CHANGE-ME-IN-PRODUCTION":   true,
		"real-secret":               false,
		"$2a$10$abc...":             false,
		"alice:realpwd":             false,
	}
	for in, want := range cases {
		if got := isBootstrapTarget(in); got != want {
			t.Errorf("isBootstrapTarget(%q) = %v, want %v", in, got, want)
		}
	}
}

// ─── randAlnum character set ─────────────────────────────────────────────

func TestRandAlnum_OnlyAlnum(t *testing.T) {
	for i := 0; i < 50; i++ {
		s := randAlnum(16)
		if len(s) != 16 {
			t.Errorf("randAlnum(16) returned length %d", len(s))
		}
		for _, c := range s {
			if !isAlnum(c) {
				t.Errorf("randAlnum returned non-alnum char %q in %q", string(c), s)
			}
		}
	}
}

func isAlnum(r int32) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
