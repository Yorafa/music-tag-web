package config

import (
	"log"
	"os"
	"strings"
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

	// gRPC plugins
	PluginGRPCAddrs map[string]string // source name -> gRPC address

	// Celery-compat (asynq queue names)
	TaskQueueDefault string

	// ─── P1.5 hardening (issue F) ──────────────────────────────────────────
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
	defaultJWTSecret     = "change-me-in-production"
	envAllowInsecure     = "ALLOW_INSECURE_DEFAULTS"
	envCORSAllowed       = "CORS_ALLOWED_ORIGINS"
	envGRPCUseTLS        = "GRPC_USE_TLS"
	envGRPCCAFile        = "GRPC_TLS_CA_FILE"
	envJWTSecret            = "JWT_SECRET"
	envAdminUsers           = "ADMIN_USERS"
	envWebhookInternalToken = "WEBHOOK_INTERNAL_TOKEN"
)

// Load reads config from environment variables. In secure mode (the
// default) it refuses to start with the placeholder JWT_SECRET; ops must
// pin ALLOW_INSECURE_DEFAULTS=1 explicitly to tolerate dev defaults.
//
// ADMIN_USERS unset behaviour: Login() always rejects; warning is logged
// here so a log-only deployment sees a loud signal.
func Load() *Config {
	insecure := isTruthyEnv(envAllowInsecure)

	jwtSecret := os.Getenv(envJWTSecret)
	if !insecure && (jwtSecret == "" || strings.EqualFold(jwtSecret, defaultJWTSecret)) {
		log.Fatalf("[config] FATAL: %s is unset or equal to the placeholder; refusing to start. "+
			"Set %s to a strong value (e.g. `openssl rand -base64 48`) or set %s=1 for explicit local dev.",
			envJWTSecret, envJWTSecret, envAllowInsecure)
	}
	if insecure && (jwtSecret == "" || strings.EqualFold(jwtSecret, defaultJWTSecret)) {
		log.Printf("[config] WARNING: %s is placeholder; this is only acceptable because %s=1",
			envJWTSecret, envAllowInsecure)
	}

	if !insecure && os.Getenv(envAdminUsers) == "" {
		log.Printf("[config] WARNING: %s unset; /api/token/ Login will refuse all credentials", envAdminUsers)
	}
	if insecure && os.Getenv(envAdminUsers) == "" {
		log.Printf("[config] WARNING: %s unset; falling back to dev admin/admin "+
			"(acceptable only because %s=1)", envAdminUsers, envAllowInsecure)
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

	origins := parseCORSOrigins(os.Getenv(envCORSAllowed))

	return &Config{
		Host:                     getEnv("GATEWAY_HOST", "0.0.0.0"),
		Port:                     getEnv("GATEWAY_PORT", "8001"),
		DBDriver:                 getEnv("DB_DRIVER", "sqlite3"),
		DBDSN:                    getEnv("DB_DSN", "/app/data/db.sqlite3"),
		RedisAddr:                getEnv("REDIS_ADDR", "127.0.0.1:6379"),
		JWTSecret:                jwtSecret,
		MusicDir:                 getEnv("MUSIC_DIR", "/app/media"),
		DataDir:                  getEnv("DATA_DIR", "/app/data"),
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

func parsePluginAddrs() map[string]string {
	m := make(map[string]string)
	// Format: PLUGIN_NETEASE_ADDR=localhost:50051
	for _, name := range []string{"netease", "kugou", "kuwo", "migu", "musicbrainz", "qmusic", "youtube"} {
		key := "PLUGIN_" + strings.ToUpper(name) + "_ADDR"
		if addr := os.Getenv(key); addr != "" {
			m[name] = addr
		}
	}
	return m
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func isTruthyEnv(key string) bool {
	val := strings.ToLower(strings.TrimSpace(os.Getenv(key)))
	return val == "1" || val == "true" || val == "yes" || val == "on"
}
