package config

import "testing"

// ─── Table: isPlaceholderEnv ─────────────────────────────────────────────
// Single-value sentinels. Match is exact (after TrimSpace + upper), so
// long real passwords that merely contain 'TODO' as a substring do NOT
// trip the guard.
func TestIsPlaceholderEnv(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		// Empty is NOT placeholder — empty is its own branch (warns / fatals).
		{"", false},

		// Exact match.
		{"__REPLACE_ME__", true},
		{"  __REPLACE_ME__  ", true},

		// Uppercase sentinels.
		{"CHANGEME", true},
		{"changeme", true},
		{"TODO", true},
		{"todo", true},
		{"FIXME", true},
		{"REPLACE-ME", true},
		{"REPLACE_ME", true},
		{"YOUR-SECRET-HERE", true},

		// Real-looking strings that should NOT trip.
		{"alice:realpwd", false},
		{"mytodopass", false},
		{"inameitchangeme", false},
		{"real-secret-with-fixme-in-it", false},
		{"$2a$10$N9qo8uLOickgx2ZMRZoMyeIjZAgcfl7p92ldGxad68LJZdL17lhWy", false},
	}
	for _, c := range cases {
		if got := isPlaceholderEnv(c.in); got != c.want {
			t.Errorf("isPlaceholderEnv(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

// ─── Table: containsPlaceholderAdminPair ──────────────────────────────────
// Multi-pair ADMIN_USERS values. The guard must fire if ANY *password*
// component is a placeholder, even when other pairs in the string have
// real credentials.
func TestContainsPlaceholderAdminPair(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want bool
	}{
		{"empty", "", false},
		{"single placeholder admin only", "admin:__REPLACE_ME__", true},
		{"single real", "admin:realpwd", false},
		{"compound: placeholder alice + real bob", "alice:__REPLACE_ME__,bob:realpwd", true},
		{"compound: real alice + placeholder bob", "alice:realpwd,bob:__REPLACE_ME__", true},
		{"compound: all real", "alice:realpwd,bob:realpwd2", false},
		{"compound: alice uppercase sentinel + real bob", "alice:CHANGEME,bob:realpwd", true},
		{"compound: TODO-pwd in middle pair", "alice:real,bob:TODO,carol:real", true},
		{"single pair with whitespace around colon", "alice : __REPLACE_ME__", true},
		{"malformed (no colon) is ignored", "no_colon_here", false},
		{"three pairs, one bad, two good", "alice:real,bob:__REPLACE_ME__,carol:real2", true},
		// Username literally containing 'TODO' must NOT trip — only pwd half is checked.
		{"username='TODO' but pwd real", "TODO:realpwd", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := containsPlaceholderAdminPair(c.in); got != c.want {
				t.Errorf("containsPlaceholderAdminPair(%q) = %v, want %v", c.in, got, c.want)
			}
		})
	}
}

// ─── Table: sentinel constants are stable ─────────────────────────────────
// If somebody renames these accidentally, tests should fail loudly.
func TestPlaceholderConstantsUnchanged(t *testing.T) {
	if placeholderSentinel != "__REPLACE_ME__" {
		t.Errorf("placeholderSentinel drifted: got %q", placeholderSentinel)
	}
	if placeholderAdminUsers != "admin:__REPLACE_ME__" {
		t.Errorf("placeholderAdminUsers drifted: got %q", placeholderAdminUsers)
	}
	if placeholderWebhookToken != "__REPLACE_ME__" {
		t.Errorf("placeholderWebhookToken drifted: got %q", placeholderWebhookToken)
	}
}
