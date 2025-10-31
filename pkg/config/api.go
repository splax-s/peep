package config

import "time"

// APIConfig holds runtime configuration for the API service.
type APIConfig struct {
	Environment         string
	Addr                string
	DatabaseURL         string
	MigrationsDir       string
	JWTSecret           string
	EnvEncryptionKey    string
	AccessTokenTTL      time.Duration
	RefreshTokenTTL     time.Duration
	BuilderURL          string
	BuilderAuthToken    string
	NginxConfigPath     string
	NginxReloadCommand  string
	NginxContainerName  string
	IngressDomainSuffix string
	LogChannelName      string
	LogBuffer           int
	WebhookSecret       string
	MetricsSampleEvery  time.Duration
	RateLimitRedisAddr  string
	RateLimitRedisPass  string
	RateLimitRedisDB    int
}

// LoadAPIConfig constructs an APIConfig from environment variables.
func LoadAPIConfig() APIConfig {
	return APIConfig{
		Environment:         GetString("APP_ENV", "development"),
		Addr:                GetString("API_ADDR", ":4000"),
		DatabaseURL:         GetString("DATABASE_URL", "postgres://vercel:vercel@db:5432/vercel?sslmode=disable"),
		MigrationsDir:       GetString("DB_MIGRATIONS_DIR", "../db/migrations"),
		JWTSecret:           GetString("JWT_SECRET", "supersecuresecret"),
		EnvEncryptionKey:    GetString("ENV_ENCRYPTION_KEY", "supersecuresecret"),
		AccessTokenTTL:      time.Duration(GetInt("ACCESS_TOKEN_TTL_MIN", 15)) * time.Minute,
		RefreshTokenTTL:     time.Duration(GetInt("REFRESH_TOKEN_TTL_HOURS", 24)) * time.Hour,
		BuilderURL:          GetString("BUILDER_URL", "http://builder:5000"),
		BuilderAuthToken:    GetString("BUILDER_AUTH_TOKEN", ""),
		NginxConfigPath:     GetString("NGINX_CONFIG_PATH", "/etc/nginx/conf.d"),
		NginxReloadCommand:  GetString("NGINX_RELOAD_COMMAND", ""),
		NginxContainerName:  GetString("NGINX_CONTAINER_NAME", ""),
		IngressDomainSuffix: GetString("INGRESS_DOMAIN_SUFFIX", ".local.peep"),
		LogChannelName:      GetString("PG_LOG_CHANNEL", "project_logs"),
		LogBuffer:           GetInt("WS_LOG_BUFFER", 100),
		WebhookSecret:       GetString("GIT_WEBHOOK_SECRET", "supersecret"),
		MetricsSampleEvery:  time.Duration(GetInt("METRICS_SAMPLE_SECONDS", 10)) * time.Second,
		RateLimitRedisAddr:  GetString("RATE_LIMIT_REDIS_ADDR", ""),
		RateLimitRedisPass:  GetString("RATE_LIMIT_REDIS_PASSWORD", ""),
		RateLimitRedisDB:    GetInt("RATE_LIMIT_REDIS_DB", 0),
	}
}
