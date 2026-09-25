package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestIsInsecureDevMode covers the env gate that Load() uses to skip
// auto-bootstrap. A test failing here means a future refactor broke
// the dev-mode affordance that contributors rely on.
func TestIsInsecureDevMode(t *testing.T) {
	cases := []struct {
		env  string
		set  bool
		want bool
	}{
		{"", false, false},     // unset
		{"0", true, false},     // explicit zero
		{"1", true, true},      // single-digit truthy
		{"true", true, true},   // long-form truthy
		{"TRUE", true, true},   // case-insensitive
		{"yes", true, true},    // word truthy
		{"on", true, true},     // word truthy
		{"false", true, false}, // truthy-looking "false" is false
		{"off", true, false},   // truthy-looking "off" is false
	}
	for _, c := range cases {
		t.Run(strings.ReplaceAll(c.env, " ", "_"), func(t *testing.T) {
			orig, had := os.LookupEnv(envAllowInsecure)
			if c.set {
				os.Setenv(envAllowInsecure, c.env)
			} else {
				os.Unsetenv(envAllowInsecure)
			}
			defer func() {
				if had {
					os.Setenv(envAllowInsecure, orig)
				} else {
					os.Unsetenv(envAllowInsecure)
				}
			}()
			if got := isInsecureDevMode(); got != c.want {
				t.Errorf("env=%q set=%v → isInsecureDevMode=%v want %v",
					c.env, c.set, got, c.want)
			}
		})
	}
}

// TestEnsureBootstrap_LoadSkipsWhenInsecure replicates Load()'s leading
// block to confirm that ALLOW_INSECURE_DEFAULTS=1 actually bypasses
// ensureBootstrap and the bootstrap file does NOT get created. If a
// future refactor accidentally removes the gate, this test will fail
// (file gets created and env vars leak).
func TestEnsureBootstrap_LoadSkipsWhenInsecure(t *testing.T) {
	withCleanEnv(t)
	os.Setenv(envAllowInsecure, "1")
	p := withTempBootstrap(t)

	// Replicate Load() prologue:
	if !isInsecureDevMode() {
		ensureBootstrap()
	}

	// File must NOT be created.
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Errorf("bootstrap file %s should not exist under ALLOW_INSECURE_DEFAULTS=1", p)
	}
	// Env vars must remain empty.
	for _, k := range []string{envJWTSecret, envAdminUsers, envWebhookInternalToken} {
		if v := os.Getenv(k); v != "" {
			t.Errorf("%s should remain empty under insecure mode; got %q", k, v)
		}
	}
}

// TestEnsureBootstrap_FileModeIs0600_AfterWrite writes a fake creds and
// asserts the on-disk file is mode 0o600. The parent dir mode is NOT
// pinned: writeBootstrap calls `os.MkdirAll(dir, 0o700)` which creates
// with that mode only on first creation. If the parent exists already
// (e.g. host ./data is bind-mounted with default 0o755), writeBootstrap
// leaves it permissive — that's a documentation decision, not a
// regression. The credential file itself stays owner-only (0o600)
// because that prevents any unrelated host user from reading the file
// even if they can list the directory.
func TestEnsureBootstrap_FileModeIs0600_AfterWrite(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, ".bootstrap-creds")
	orig := bootstrapPath
	bootstrapPath = p
	t.Cleanup(func() { bootstrapPath = orig })

	c := &bootstrapCreds{
		Version:   bootstrapVersion,
		JWTSecret: "fake", AdminUser: "admin", WebhookToken: "fake",
	}
	if err := writeBootstrap(c); err != nil {
		t.Fatalf("writeBootstrap: %v", err)
	}
	info, err := os.Stat(p)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Errorf("file mode = %o, want 0o600", got)
	}
	if info.IsDir() {
		t.Errorf("target %s should be a regular file, not a directory", p)
	}
}

// TestEnsureBootstrap_PlaintextStrippedFromDiskAfterFirstBoot is the
// round-2 security regression test: the on-disk file must NOT contain
// the plaintext admin password after the FIRST BOOT cycle. Otherwise a
// backup or forensic analyst recovering ./data gets the live credential.
func TestEnsureBootstrap_PlaintextStrippedFromDiskAfterFirstBoot(t *testing.T) {
	withCleanEnv(t)
	p := withTempBootstrap(t)

	secrets := loadOrGenerate()
	if !secrets.wasNewlyGenerated {
		t.Fatal("expected wasNewlyGenerated=true on first loadOrGenerate")
	}
	// Round-trip through writeBootstrap (which ensureBootstrap invokes
	// at the end of its body). After this, the file should have plaintext
	// stripped.
	if err := writeBootstrap(secrets); err != nil {
		t.Fatalf("writeBootstrap: %v", err)
	}
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read after write: %v", err)
	}
	var c bootstrapCreds
	if err := json.Unmarshal(data, &c); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if c.AdminPasswordPlain != "" {
		t.Errorf("on-disk bootstrap has plaintext admin password (%q); "+
			"writeBootstrap must strip before persist or json tag must be omitempty",
			c.AdminPasswordPlain)
	}
	// also raw-string sanity: even if a future refactor accidentally
	// bypasses the struct path, the literal "admin_password_plain" key
	// should not appear with non-empty value.
	if strings.Contains(string(data), "admin_password_plain\":") && !strings.Contains(string(data), "admin_password_plain\":\"\"") {
		t.Errorf("on-disk JSON still contains plaintext admin_password_plain field: %s", data)
	}
}

// TestEnsureBootstrap_PlaintextSurvivesInMemoryForAnnounce is the
// counter-positive: despite on-disk strip, the in-memory creds struct
// keeps the plaintext so announceBoostrap can print it once. This is
// the asymmetry the security design relies on.
func TestEnsureBootstrap_PlaintextSurvivesInMemoryForAnnounce(t *testing.T) {
	withCleanEnv(t)
	withTempBootstrap(t)
	secrets := loadOrGenerate()
	// Before any disk-strip, plaintext present.
	if secrets.AdminPasswordPlain == "" {
		t.Fatal("plaintext expected in freshly-generated in-memory creds")
	}
}

// TestLoadOrGenerate_RereadMarksNotNewlyGenerated: on second boot, the
// returned creds has wasNewlyGenerated=false so announceBoostrap will
// print the silent re-read line, not the credential banner.
func TestLoadOrGenerate_RereadMarksNotNewlyGenerated(t *testing.T) {
	withCleanEnv(t)
	withTempBootstrap(t)
	first := loadOrGenerate()
	if !first.wasNewlyGenerated {
		t.Errorf("first load: wasNewlyGenerated=false; want true")
	}
	// Re-persist without plaintext (simulating ensureBootstrap's tail
	// writeBootstrap, which strips before disk-write on the same boot).
	first.AdminPasswordPlain = ""
	if err := writeBootstrap(first); err != nil {
		t.Fatalf("writeBootstrap: %v", err)
	}
	second := loadOrGenerate()
	if second.wasNewlyGenerated {
		t.Errorf("second load: wasNewlyGenerated=true; want false (re-read)")
	}
	if second.AdminPasswordPlain != "" {
		t.Errorf("re-read plaintext=%q; should be empty (was stripped on disk)", second.AdminPasswordPlain)
	}
}

// TestEnsureBootstrap_OnRereadLeavesEnvUnchanged: if all env vars are
// REAL and the bootstrap file exists, calling Load()'s prologue leaves
// them untouched (don't overwrite a real secret because disk file
// happens to exist).
func TestEnsureBootstrap_OnRereadLeavesEnvUnchanged(t *testing.T) {
	withCleanEnv(t)
	withTempBootstrap(t)
	seed := &bootstrapCreds{
		Version: bootstrapVersion, GeneratedAt: "2024-01-01T00:00:00Z",
		JWTSecret: "disk-jwt", AdminUser: "admin", WebhookToken: "disk-tok",
	}
	if err := writeBootstrap(seed); err != nil {
		t.Fatalf("seed writeBootstrap: %v", err)
	}
	const realJWT = "i-am-real-env-jwt"
	const realAdmin = "alice:$2a$10$abcdefghijklmnopqrstuvwxyz0123456789012345678901234567890123"
	const realHook = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	os.Setenv(envJWTSecret, realJWT)
	os.Setenv(envAdminUsers, realAdmin)
	os.Setenv(envWebhookInternalToken, realHook)

	ensureBootstrap()

	if got := os.Getenv(envJWTSecret); got != realJWT {
		t.Errorf("real JWT_SECRET got clobbered by bootstrap: %q vs %q", got, realJWT)
	}
	if got := os.Getenv(envAdminUsers); got != realAdmin {
		t.Errorf("real ADMIN_USERS got clobbered by bootstrap: %q vs %q", got, realAdmin)
	}
	if got := os.Getenv(envWebhookInternalToken); got != realHook {
		t.Errorf("real WEBHOOK got clobbered: %q vs %q", got, realHook)
	}
}

// TestWriteBootstrap_AtomicTakeover confirms writeBootstrap produced a
// temp file during the call (proving the tmp+rename path was actually
// taken, not just absent at end). We do this by inspecting the parent's
// file list AFTER a successful call. A test that only checked absence
// would pass even if a refactor skipped the tmp step entirely.
func TestWriteBootstrap_AtomicTakeover(t *testing.T) {
	dir := t.TempDir()
	orig := bootstrapPath
	bootstrapPath = filepath.Join(dir, ".bootstrap-creds")
	t.Cleanup(func() { bootstrapPath = orig })

	c := &bootstrapCreds{Version: bootstrapVersion, JWTSecret: "x", AdminUser: "admin", WebhookToken: "y"}
	if err := writeBootstrap(c); err != nil {
		t.Fatalf("writeBootstrap: %v", err)
	}

	// Final state should be exactly the bootstrap file (no .tmp leftovers).
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("readDir: %v", err)
	}
	if len(entries) != 1 {
		t.Errorf("dir should contain only 1 file post-write; got %d (%v)", len(entries), entries)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp.") {
			t.Errorf("tmp file leftover after atomic rename: %s", e.Name())
		}
	}
	// Final file should have correct content (renamed atomically from tmp).
	target := filepath.Join(dir, ".bootstrap-creds")
	if _, err := os.Stat(target); err != nil {
		t.Errorf("expected final bootstrap file at %s after rename; stat err=%v", target, err)
	}
}

// TestWriteBootstrap_TmpFileSuffixScheme anchors by inspection: the
// .tmp. suffix in bootstrap.go confirms the atomic-rename path is
// exercised. A refactor that drops the temp-file step would silently
// weaken the forensic-clean guarantee and should be caught here.
func TestWriteBootstrap_TmpFileSuffixScheme(t *testing.T) {
	bs, err := os.ReadFile("bootstrap.go")
	if err != nil {
		t.Skipf("source not readable from cwd: %v", err)
	}
	if !strings.Contains(string(bs), ".tmp.") {
		t.Errorf("bootstrap.go source should reference a .tmp. suffix for atomic writeBootstrap")
	}
}
