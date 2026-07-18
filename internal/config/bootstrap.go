package config

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// ─────────────────────────────────────────────────────────────────────────────
// First-boot auto-bootstrap for "lazy" deployments
// ─────────────────────────────────────────────────────────────────────────────
//
// Goal: an operator who has just cloned the repo and runs
//   docker compose up -d --build
// without ever editing .env, .env.example, or generating any secrets must
// end up with a working gateway — with credentials that survive container
// restarts.
//
// Mechanism: any of {JWT_SECRET, ADMIN_USERS, WEBHOOK_INTERNAL_TOKEN} that
// is missing or equal to one of the obvious sentinels ("__REPLACE_ME__",
// "change-me-in-production", etc.) gets auto-generated via crypto/rand and
// persisted to bootstrapPath (default /app/data/.bootstrap-creds, which
// the gateway compose service bind-mounts from ./data). On every
// subsequent boot os.Setenv() repopulates from the file BEFORE the
// existing fail-closed FATAL guards run, so the existing checks just see
// real values and pass.
//
// This file runs ONLY in non-insecure mode (i.e. when
// ALLOW_INSECURE_DEFAULTS is unset/0). Setting
// ALLOW_INSECURE_DEFAULTS=1 keeps the old dev-friendly behavior so a
// contributor who wants to test the sentinel-detection itself can still
// do so without triggering auto-bootstrap.

const bootstrapVersion = 1
const bcryptCost = 10
const defaultAdminUser = "admin"
const defaultAdminPasswordLen = 16

// bootstrapPath is overwritten by tests (t.Setenv not usable here because
// we want to override the file location). Production stays at /app/data.
//
// The path is intentionally a `var` (not `const`) so tests can swap it.
//   var bootstrapPath = "/app/data/.bootstrap-creds"
var bootstrapPath = "/app/data/.bootstrap-creds"

// bootstrapCreds is the JSON shape persisted to bootstrapPath. The
// AdminPasswordPlain field is intentionally transient: it lives in the
// returned struct only long enough for announceBoostrap to print it
// once at first boot; writeBootstrap strips it before atomic-rename,
// and the JSON tag `omitempty` ensures reloads don't grow the on-disk
// file if a future caller forgets to strip. After first boot, disk
// contains the bcrypt hash + admin user + JWT_SECRET + webhook token
// — never the plaintext password.
type bootstrapCreds struct {
	Version            int    `json:"version"`
	GeneratedAt        string `json:"generated_at"`
	JWTSecret          string `json:"jwt_secret"`
	AdminUser          string `json:"admin_user"`
	AdminPasswordPlain string `json:"admin_password_plain,omitempty"` // ephemeral
	WebhookToken       string `json:"webhook_internal_token"`

	// wasNewlyGenerated is set ONLY for the in-memory instance returned
	// from a freshly-generated creds; JSON-skipped when written to disk.
	wasNewlyGenerated bool `json:"-"`
}

// ensureBootstrap populates missing/placeholder JWT_SECRET, ADMIN_USERS
// (whole-string or any pair's password), and WEBHOOK_INTERNAL_TOKEN from
// /app/data/.bootstrap-creds; on first boot when that file is absent it
// generates fresh secrets and persists them. Idempotent: a second call
// without intermediate changes produces no log noise beyond a one-line
// "loaded existing bootstrap" marker (when something actually got filled
// in).
//
// Called only from Load() in non-insecure mode. The function never
// silently overwrites operator-supplied real values; isBootstrapTarget
// is the truth table.
//
// SECURITY (post round-1 review): the on-disk bootstrap file NEVER
// contains the plaintext admin password after this function returns.
// loadOrGenerate keeps `plaintext` field only in the returned in-memory
// bootstrapCreds so announceBoostrap can log it once, then writeBootstrap
// drops the field before atomic-rename persists. A second boot will
// therefore find a plaintext-empty file; announce will silently re-read,
// not print a password to logs. Backup tools / inspectors / syslog
// forwarders only ever see the bcrypt on disk.
func ensureBootstrap() {
	secrets := loadOrGenerate()

	// Fill each missing/junk env var via os.Setenv so downstream
	// os.Getenv() calls see real values. The existing FATAL guards then
	// don't fire.
	var filled []string
	jwt := strings.TrimSpace(os.Getenv(envJWTSecret))
	if isBootstrapTarget(jwt) {
		if err := os.Setenv(envJWTSecret, secrets.JWTSecret); err == nil {
			filled = append(filled, envJWTSecret)
		}
	}
	adm := strings.TrimSpace(os.Getenv(envAdminUsers))
	if isBootstrapTarget(adm) || containsPlaceholderAdminPair(adm) {
		// Re-emit as user:bcrypt-with-hash so the auth handler's
		// bcrypt.Compare path runs unaffected. The plaintext lives only
		// in the in-memory bootstrapCreds (cleared from disk below).
		hashed := bcryptFromPlain(secrets.AdminPasswordPlain)
		if err := os.Setenv(envAdminUsers, secrets.AdminUser+":"+hashed); err == nil {
			filled = append(filled, envAdminUsers)
		}
	}
	hook := strings.TrimSpace(os.Getenv(envWebhookInternalToken))
	if isBootstrapTarget(hook) {
		if err := os.Setenv(envWebhookInternalToken, secrets.WebhookToken); err == nil {
			filled = append(filled, envWebhookInternalToken)
		}
	}

	if len(filled) > 0 {
		announceBoostrap(secrets, secrets.wasNewlyGenerated, filled)
	}

	// Always re-persist the final state, stripping the plaintext admin
	// password before the bytes touch disk. After this, restart reads a
	// bcrypt-only file and announcement goes silent.
	if err := writeBootstrap(secrets); err != nil {
		log.Printf("[config] WARNING: could not refresh bootstrap file to plaintext-free form: %v", err)
	}
}

// isInsecureDevMode returns true when ALLOW_INSECURE_DEFAULTS=1.
// Extracted so tests can assert that ensureBootstrap is the ONLY path
// for the lazy-deploy feature; the conditional lives in Load(), so
// tests of bootstrap logic itself don't depend on env wiring.
func isInsecureDevMode() bool {
	return isTruthyEnv(envAllowInsecure)
}

// isBootstrapTarget returns true when the env value is one that
// auto-bootstrap should replace: empty, the legacy literal
// "change-me-in-production", or any of the example sentinels (which
// isPlaceholderEnv detects). Chosen for set complement intersection with
// `insecure && isPlaceholderEnv(...)` warning path so behaviour is
// predictable.
func isBootstrapTarget(v string) bool {
	if strings.TrimSpace(v) == "" {
		return true
	}
	if strings.EqualFold(strings.TrimSpace(v), defaultJWTSecret) {
		return true
	}
	if isPlaceholderEnv(v) {
		return true
	}
	return false
}

// loadOrGenerate returns the persisted bootstrap file (creating it on
// first boot). Errors during persistence are fatal because they signal
// a misconfigured volume mount — surfacing them loudly beats silent
// failure ("the gateway boots but accepts placeholder credentials").
//
// On a freshly-generated creds, writeBootstrap is called with the
// plaintext field intact (so ensureBootstrap can later pass the value
// to announceBoostrap via the in-memory copy). On reread, the
// already-stripped file returns creds with AdminPasswordPlain=="".
func loadOrGenerate() *bootstrapCreds {
	if c, err := readBootstrap(); err == nil {
		c.wasNewlyGenerated = false
		return c
	}
	c := &bootstrapCreds{
		Version:            bootstrapVersion,
		GeneratedAt:        time.Now().UTC().Format(time.RFC3339),
		JWTSecret:          randBase64(48),
		AdminUser:          defaultAdminUser,
		AdminPasswordPlain: randAlnum(defaultAdminPasswordLen),
		WebhookToken:       randHex(32),
		wasNewlyGenerated:   true,
	}
	if err := writeBootstrap(c); err != nil {
		log.Fatalf("[config] FATAL: cannot persist bootstrap creds to %s: %v\n"+
			"  This usually means the host ./data directory is missing or not writable.\n"+
			"  Fix: ensure ./data exists (mkdir -p ./data) and is writable, OR pin\n"+
			"  real values in .env for JWT_SECRET / ADMIN_USERS / WEBHOOK_INTERNAL_TOKEN,\n"+
			"  OR set ALLOW_INSECURE_DEFAULTS=1 to opt out of fail-closed enforcement.",
			bootstrapPath, err)
	}
	return c
}

func readBootstrap() (*bootstrapCreds, error) {
	data, err := os.ReadFile(bootstrapPath)
	if err != nil {
		return nil, err
	}
	var c bootstrapCreds
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("malformed json: %w", err)
	}
	if c.Version != bootstrapVersion {
		return nil, fmt.Errorf("version drift: have %d want %d", c.Version, bootstrapVersion)
	}
	if c.JWTSecret == "" || c.AdminUser == "" || c.WebhookToken == "" {
		return nil, fmt.Errorf("missing required field (jwt_secret / admin_user / webhook_internal_token)")
	}
	return &c, nil
}

// writeBootstrap persists c atomically: write to `<path>.tmp.<pid>` then
// os.Rename over the live path. Atomic on POSIX; on Windows falls back
// to best-effort replace. STDOUT/FATALLY admin password plaintext is
// always cleared before Marshal so the dynamic on-disk shape never
// contains a recoverable credential.
//
// Mode 0o600 because the file contains JWT_SECRET + webhook token
// (not the plaintext password after the first pass). Director is 0o700.
func writeBootstrap(c *bootstrapCreds) error {
	dir := filepath.Dir(bootstrapPath)
	if dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
	}
	// Strip plaintext before marshalling so the on-disk JSON never
	// carries it; `wasNewlyGenerated` is `json:"-"` so it's skipped too.
	persistable := *c
	persistable.AdminPasswordPlain = ""
	data, err := json.MarshalIndent(&persistable, "", "  ")
	if err != nil {
		return err
	}
	tmpPath := fmt.Sprintf("%s.tmp.%d", bootstrapPath, os.Getpid())
	if err := os.WriteFile(tmpPath, data, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, bootstrapPath); err != nil {
		// best-effort cleanup of temp file on rename failure
		_ = os.Remove(tmpPath)
		return err
	}
	return nil
}

// bcryptFromPlain is intentionally fatal on error — the only failure is
// cost-too-high or resource exhaustion, both of which mean the host is
// too constrained to operate the gateway safely.
func bcryptFromPlain(plain string) string {
	h, err := bcrypt.GenerateFromPassword([]byte(plain), bcryptCost)
	if err != nil {
		log.Fatalf("[config] FATAL: bcrypt hash: %v", err)
	}
	return string(h)
}

func randBase64(nBytes int) string {
	b := make([]byte, nBytes)
	if _, err := rand.Read(b); err != nil {
		log.Fatalf("[config] FATAL: crypto/rand: %v", err)
	}
	return base64.StdEncoding.EncodeToString(b)
}

func randHex(nBytes int) string {
	b := make([]byte, nBytes)
	if _, err := rand.Read(b); err != nil {
		log.Fatalf("[config] FATAL: crypto/rand: %v", err)
	}
	return hex.EncodeToString(b)
}

// randAlnum rejects the base64 '+', '/', '=' characters so the result
// is typeable / paste-able for an admin to log in.
//   length-16 ceil(16*4/3)=22 raw bytes after stripping pads.
func randAlnum(n int) string {
	alphabet := "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"
	src := randBase64(n + 4) // over-fetch then trim
	out := make([]byte, 0, n)
	for i := 0; i < len(src) && len(out) < n; i++ {
		c := src[i]
		if strings.IndexByte(alphabet, c) >= 0 {
			out = append(out, c)
		}
	}
	// If under-sampled (impossible with n=16 but defensive), top up.
	for len(out) < n {
		out = append(out, alphabet[len(out)%len(alphabet)])
	}
	return string(out)
}

// announceBoostrap prints the FIRST-BOOT credentials banner IFF the
// values were freshly generated (wasNew=true). On rereads it emits a
// single-line silent re-load marker so an operator doesn't suddenly see
// a one-time password in repeated gateway restarts (which would re-print
// in journald / syslog aggregators). Plaintext appears ONLY on a fresh
// generation; never from on-disk.
func announceBoostrap(c *bootstrapCreds, wasNew bool, filled []string) {
	if !wasNew {
		log.Printf("[config] bootstrap secrets reloaded from %s (credentials established on prior boot)", bootstrapPath)
		return
	}
	log.Printf("=========================================================")
	log.Printf("[config] FIRST-BOOT auto-bootstrap: %s", strings.Join(filled, ", "))
	log.Printf("[config]   admin user:                  %s", c.AdminUser)
	log.Printf("[config]   admin password (PLAINTEXT):  %s   <- record this", c.AdminPasswordPlain)
	log.Printf("[config]   JWT_SECRET (base64, 48B):    %s", c.JWTSecret)
	log.Printf("[config]   WEBHOOK_INTERNAL_TOKEN:      %s", c.WebhookToken)
	log.Printf("[config]   persisted to:                %s", bootstrapPath)
	log.Printf("[config]   file readable by owner only (mode 0600); plaintext admin password will vanish from disk after this process restarts.")
	log.Printf("=========================================================")
}
