package config

import (
	"log"
	"os"
	"strconv"
	"strings"
	"sync"
)

// Config holds all application configuration.
type Config struct {
	// HTTP
	Host string
	Port string

	// Database
	DBDriver string // "sqlite3" or "mysql"
	DBDSN    string

	// Redis (for asynq task queue)
	RedisAddr string

	// JWT
	JWTSecret string

	// File system
	MusicDir string
	DataDir  string

	// ─── 下载缓存 (AUDIO_CACHE_DIR) ───────────────────────────────────────
	// 试听和「加库」都从 AUDIO_CACHE_DIR 读已下载的音频，它是 named volume
	// 里的常驻数据，从前只进不出。AudioCacheMaxMB 是自动清理的容量上限：
	// 超过后由 worker 从最旧的开始删，0 表示关掉自动清理（仍然可以手动清）。
	AudioCacheMaxMB int64
	// AudioCacheMinAgeMinutes 保护这段时间内写过的文件不被自动清理删掉。
	// /api/stream 直接 ServeFile 这个目录，正在播的那首和刚下载、正准备
	// 复制进库的那次下载都在这个窗口里。
	AudioCacheMinAgeMinutes int

	// gRPC plugins
	PluginGRPCAddrs map[string]string // source name -> gRPC address

	// Celery-compat (asynq queue names)
	TaskQueueDefault string

	// ─── gRPC transport + CORS hardening ───────────────────────────────────
	// CORSAllowedOrigins 列出允许跨域携带凭证的来源。空字符串切片表示
	// 拒绝所有跨域请求 (后端不会回显 Access-Control-Allow-Origin)。
	CORSAllowedOrigins []string
	// GRPCUseTLS 控制 gateway/worker 连接到各 plugin gRPC 服务时是否
	// 走 TLS。false 维持当前 insecure 行为，仅在 ALLOW_INSECURE_DEFAULTS=1
	// 下用于本地 dev；生产强烈建议设为 true。
	GRPCUseTLS bool
	// GRPCCAFile 是 PEM CA bundle 的路径；仅在 GRPCUseTLS=true 时使用。
	// 空字符串代表走系统根证书 (system cert pool)。
	GRPCCAFile string
	// AllowInsecureDevDefaults 表示允许 dev 兜底默认值 (placeholder JWT
	// secret 的告警 / admin/admin Login 等)。本字段直接读 env：
	//   ALLOW_INSECURE_DEFAULTS=1
	// 否则即便 dev 友好的 warning 也升级为 log.Fatalf 以阻止把 placeholder
	// 密钥的 gateway 推到生产环境。
	AllowInsecureDevDefaults bool

	// WebhookInternalToken is the shared secret workers MUST present in
	// the X-Internal-Token header when calling /api/webhooks/*. Empty in
	// production causes the middleware to fail-closed (HTTP 403). Dev
	// rigs can leave it empty only when ALLOW_INSECURE_DEFAULTS=1.
	WebhookInternalToken string
}

const (
	defaultJWTSecret = "change-me-in-production"
	// placeholderAdminUsers / placeholderWebhookToken are the obvious
	// sentinel tokens shipped in .env.example. When any of
	// these (or the equivalent string "__REPLACE_ME__") is detected,
	// config.LoadAtBoot() refuses to start outside dev mode — siloed the same
	// way as defaultJWTSecret so an operator who copies the example
	// without editing can't accidentally boot the gateway with a public
	// placeholder credential.
	placeholderAdminUsers   = "admin:__REPLACE_ME__"
	placeholderWebhookToken = "__REPLACE_ME__"
	placeholderSentinel     = "__REPLACE_ME__"
	envAllowInsecure        = "ALLOW_INSECURE_DEFAULTS"
	envCORSAllowed          = "CORS_ALLOWED_ORIGINS"
	envGRPCUseTLS           = "GRPC_USE_TLS"
	envGRPCCAFile           = "GRPC_TLS_CA_FILE"
	envJWTSecret            = "JWT_SECRET"
	envAdminUsers           = "ADMIN_USERS"
	envWebhookInternalToken = "WEBHOOK_INTERNAL_TOKEN"
)

// isPlaceholderEnv returns true when val looks like one of the obvious
// sentinel tokens shipped in .env.example. It also catches a handful of
// common sloppy placeholders so a reviewer-only edit (CHANGEME / FIXME
// / TODO / your-secret-here) still trips the guard.
//
// The match is exact (after trim + upper) — a real password containing
// 'mytodopass' or 'inameitchangeme' will NOT trip the guard. This is
// deliberate: substring matching would generate too many false positives
// in production passwords.
func isPlaceholderEnv(val string) bool {
	v := strings.TrimSpace(val)
	if v == "" {
		return false
	}
	if v == placeholderSentinel {
		return true
	}
	upper := strings.ToUpper(v)
	switch upper {
	case "CHANGEME", "TODO", "FIXME", "REPLACE-ME", "REPLACE_ME", "YOUR-SECRET-HERE":
		return true
	}
	return false
}

// containsPlaceholderAdminPair parses an ADMIN_USERS='user:pwd[,user:pwd...]'
// value and returns true if ANY *password* component looks like the
// example placeholder. The username half is never checked — only the
// credential half — so a user literally named 'TODO' or 'CHANGEME' still
// works.
//
// This is the guard that catches the previously-missing compound case:
//
//	ADMIN_USERS='alice:__REPLACE_ME__,bob:realpwd'
//
// where neither Load()'s whole-string check nor isPlaceholderEnv on the
// joined string trips.
func containsPlaceholderAdminPair(raw string) bool {
	if raw == "" {
		return false
	}
	for _, pair := range strings.Split(raw, ",") {
		parts := strings.SplitN(strings.TrimSpace(pair), ":", 2)
		if len(parts) != 2 {
			continue // malformed — login will fail later via crypt/Compare
		}
		pwd := strings.TrimSpace(parts[1])
		if pwd == placeholderSentinel || isPlaceholderEnv(pwd) {
			return true
		}
	}
	return false
}

// ─── Boot-time vs request-time config (REVIEW.md P0-3) ────────────────────
//
// Load() used to be called from BOTH main() and every auth handler. Because
// Load() unconditionally ran ensureBootstrap() — which reads AND rewrites
// ./data/.bootstrap-creds — that meant every unauthenticated POST
// /api/token/ performed a file read + write + rename. Three consequences:
//
//  1. DoS amplification: anonymous requests drove disk IO.
//  2. Concurrent stomping: the temp file is named `.tmp.<pid>`, so two
//     in-flight Load() calls in the same process raced on one path.
//  3. Anonymous process kill: loadOrGenerate() calls log.Fatalf when the
//     creds file can neither be read nor written. A full disk or a chmod on
//     ./data turned one anonymous HTTP request into a gateway exit.
//
// The split:
//
//	LoadAtBoot() — bootstrap + fail-closed validation + snapshot. main() only.
//	Current()    — read-only snapshot. Handlers only. Never touches disk.
//
// Current() falls back to a pure env read when no snapshot has been
// installed. That path exists for unit tests, which drive handlers directly
// with t.Setenv and never call LoadAtBoot(); it deliberately skips both
// ensureBootstrap and the log.Fatalf guards so a test binary can exercise
// placeholder-credential behaviour without writing to /app/data or dying.
var (
	snapshotMu sync.RWMutex
	snapshot   *Config
)

// LoadAtBoot performs first-boot bootstrap, runs the fail-closed secret
// validation (log.Fatalf on placeholder JWT_SECRET / ADMIN_USERS), caches
// the result, and returns it. Call exactly once, from main().
func LoadAtBoot() *Config {
	// ─── First-boot auto-bootstrap (lazy deploy) ───
	// In non-insecure mode, fill any missing/placeholder JWT_SECRET /
	// ADMIN_USERS / WEBHOOK_INTERNAL_TOKEN from ./data/.bootstrap-creds
	// (regenerating on first boot). Lets `docker compose up -d --build`
	// succeed with zero .env edits.
	if !isInsecureDevMode() {
		ensureBootstrap()
	}
	validateSecrets()
	cfg := loadFromEnv()

	snapshotMu.Lock()
	snapshot = cfg
	snapshotMu.Unlock()
	return cfg
}

// Current returns the boot-time snapshot. Safe to call per request: it does
// no disk IO and cannot terminate the process. When LoadAtBoot has not run
// (unit tests), it returns a fresh env-only read instead.
func Current() *Config {
	snapshotMu.RLock()
	c := snapshot
	snapshotMu.RUnlock()
	if c != nil {
		return c
	}
	return loadFromEnv()
}

// ResetSnapshotForTest drops the cached snapshot so a following Current()
// re-reads the environment. Test-only; production never calls this.
func ResetSnapshotForTest() {
	snapshotMu.Lock()
	snapshot = nil
	snapshotMu.Unlock()
}

// Load is retained as the boot-time entry point under its historical name.
//
// Deprecated: call LoadAtBoot() from main() and Current() from request
// handlers. Kept so existing callers/tests keep compiling; it is exactly
// LoadAtBoot().
func Load() *Config { return LoadAtBoot() }

// validateSecrets runs the fail-closed startup guards. Separated from
// loadFromEnv so that the request-time path (Current) can never log.Fatalf.
func validateSecrets() {
	insecure := isTruthyEnv(envAllowInsecure)

	jwtSecret := os.Getenv(envJWTSecret)
	if !insecure && (jwtSecret == "" || strings.EqualFold(jwtSecret, defaultJWTSecret) || isPlaceholderEnv(jwtSecret)) {
		log.Fatalf("[config] FATAL: %s is unset, equal to the placeholder, or equal to a sentinel "+
			"like __REPLACE_ME__; refusing to start. Set %s to a strong value (e.g. `openssl rand "+
			"-base64 48`) or set %s=1 for explicit local dev.",
			envJWTSecret, envJWTSecret, envAllowInsecure)
	}
	if insecure && (jwtSecret == "" || strings.EqualFold(jwtSecret, defaultJWTSecret) || isPlaceholderEnv(jwtSecret)) {
		log.Printf("[config] WARNING: %s is placeholder; this is only acceptable because %s=1",
			envJWTSecret, envAllowInsecure)
	}

	adminUsers := os.Getenv(envAdminUsers)
	adminUsersPlaceholder := adminUsers == "" ||
		adminUsers == placeholderAdminUsers ||
		isPlaceholderEnv(adminUsers) ||
		containsPlaceholderAdminPair(adminUsers)
	if !insecure && adminUsersPlaceholder {
		// In non-dev mode, an unset/placeholder ADMIN_USERS must fail-closed
		// SAME way as the JWT placeholder — nobody should be able to log in
		// with a credential that ships in a public example file. The
		// containsPlaceholderAdminPair check guards against the compound
		// case (alice:__REPLACE_ME__,bob:realpwd) where any single pair's
		// password component still matches the example sentinel.
		log.Fatalf("[config] FATAL: %s is unset, equal to the example placeholder "+
			"(%q), or contains a pair whose password is a placeholder; refusing to start. "+
			"Set %s to user:bcrypt_or_plain_password pairs or set %s=1 for explicit local dev.",
			envAdminUsers, placeholderAdminUsers, envAdminUsers, envAllowInsecure)
	}
	if insecure && adminUsersPlaceholder {
		log.Printf("[config] WARNING: %s is placeholder; this is only acceptable because %s=1",
			envAdminUsers, envAllowInsecure)
	}

	webhookToken := os.Getenv(envWebhookInternalToken)
	if !insecure && (webhookToken != "" && isPlaceholderEnv(webhookToken)) {
		// Unlike JWT_SECRET / ADMIN_USERS, webhook token may legitimately be
		// empty in single-host dev (no workers exist). But if it IS set,
		// refuse placeholders so a forgotten edit doesn't silently match a
		// downstream worker that happens to send the same literal.
		log.Fatalf("[config] FATAL: %s is set to the example placeholder (%q); refusing to start. "+
			"Either unset it (single-host) or set %s=openssl rand -hex 32.",
			envWebhookInternalToken, placeholderWebhookToken, envWebhookInternalToken)
	}
	if insecure && isPlaceholderEnv(webhookToken) {
		log.Printf("[config] WARNING: %s is placeholder; this is only acceptable because %s=1",
			envWebhookInternalToken, envAllowInsecure)
	}

	if insecure && !isTruthyEnv(envGRPCUseTLS) {
		log.Printf("[config] WARNING: gRPC plugins connect via insecure credentials; "+
			"set %s=1 (and provide %s if needed) for production.",
			envGRPCUseTLS, envGRPCCAFile)
	}
	if !insecure && os.Getenv(envWebhookInternalToken) == "" {
		log.Printf("[config] WARNING: %s unset; /api/webhooks/* will reject all requests (fail-closed)", envWebhookInternalToken)
	}
	if insecure && os.Getenv(envWebhookInternalToken) == "" {
		log.Printf("[config] WARNING: %s unset; dev webhook auth bypassed (only because %s=1)", envWebhookInternalToken, envAllowInsecure)
	}
}

// loadFromEnv builds a Config purely by reading the environment. It performs
// NO disk IO and never calls log.Fatalf, which is what makes it safe on the
// request-time path via Current(). All fail-closed enforcement lives in
// validateSecrets, which only LoadAtBoot() calls.
func loadFromEnv() *Config {
	insecure := isTruthyEnv(envAllowInsecure)
	origins := parseCORSOrigins(os.Getenv(envCORSAllowed))

	return &Config{
		Host:                     getEnv("GATEWAY_HOST", "0.0.0.0"),
		Port:                     getEnv("GATEWAY_PORT", "8001"),
		DBDriver:                 getEnv("DB_DRIVER", "sqlite3"),
		DBDSN:                    getEnv("DB_DSN", "/app/data/db.sqlite3"),
		RedisAddr:                getEnv("REDIS_ADDR", "127.0.0.1:6379"),
		JWTSecret:                os.Getenv(envJWTSecret),
		MusicDir:                 getEnv("MUSIC_DIR", "/app/media"),
		DataDir:                  getEnv("DATA_DIR", "/app/data"),
		AudioCacheMaxMB:          getEnvInt64("AUDIO_CACHE_MAX_MB", 2048),
		AudioCacheMinAgeMinutes:  int(getEnvInt64("AUDIO_CACHE_MIN_AGE_MIN", 30)),
		PluginGRPCAddrs:          parsePluginAddrs(),
		TaskQueueDefault:         getEnv("TASK_QUEUE", "music-tag-tasks"),
		CORSAllowedOrigins:       origins,
		GRPCUseTLS:               isTruthyEnv(envGRPCUseTLS),
		GRPCCAFile:               os.Getenv(envGRPCCAFile),
		AllowInsecureDevDefaults: insecure,
		WebhookInternalToken:     os.Getenv(envWebhookInternalToken),
	}
}

// IsInsecureDevDefaults 暴露 dev-default 开关，让 handler.Login() 在
// 每次请求时也能读到最新 env (避免依赖缓存 Config struct)。
func IsInsecureDevDefaults() bool { return isTruthyEnv(envAllowInsecure) }

// JWTSecretIsDefault 告诉 handler.Login/Refresh/Verify 当前 JWT_SECRET
// 是否就是源码树携带的兜底值；非 dev 模式下应该会被 Load() 在启动
// 阶段直接 log.Fatalf 阻断，但日志/告警路径中调用此函数可以更早地报错。
func JWTSecretIsDefault() bool {
	s := os.Getenv(envJWTSecret)
	if s == "" {
		return true
	}
	return strings.EqualFold(s, defaultJWTSecret)
}

func parseCORSOrigins(raw string) []string {
	if raw == "" {
		return nil
	}
	out := []string{}
	for _, o := range strings.Split(raw, ",") {
		o = strings.TrimSpace(o)
		if o != "" {
			out = append(out, o)
		}
	}
	return out
}

// parsePluginAddrs reads every PLUGIN_<NAME>_ADDR variable, so the set of
// sources follows whatever compose configures rather than a list kept in
// sync by hand. That list silently dropped acoustid: the container ran,
// healthy, with fpcalc on PATH, while nothing ever dialed it — and because
// no connection was attempted there was not even an unreachable warning.
//
// Format: PLUGIN_NETEASE_ADDR=localhost:50051
func parsePluginAddrs() map[string]string {
	m := make(map[string]string)
	for _, kv := range os.Environ() {
		name, addr, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}
		rest, ok := strings.CutPrefix(name, "PLUGIN_")
		if !ok {
			continue
		}
		plugin, ok := strings.CutSuffix(rest, "_ADDR")
		if !ok || plugin == "" || strings.TrimSpace(addr) == "" {
			continue
		}
		m[strings.ToLower(plugin)] = addr
	}
	return m
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// getEnvInt64 reads a non-negative integer env var, falling back when it is
// unset, blank or unparseable. A malformed value must not silently become 0
// here: 0 is a meaningful setting (AUDIO_CACHE_MAX_MB=0 disables the
// automatic prune), so a typo has to land on the default instead.
func getEnvInt64(key string, fallback int64) int64 {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	v, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || v < 0 {
		log.Printf("[config] %s=%q is not a non-negative integer, using %d", key, raw, fallback)
		return fallback
	}
	return v
}

func isTruthyEnv(key string) bool {
	val := strings.ToLower(strings.TrimSpace(os.Getenv(key)))
	return val == "1" || val == "true" || val == "yes" || val == "on"
}
