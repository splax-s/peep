package config

import "time"

// BuilderConfig holds runtime configuration for the builder service.
type BuilderConfig struct {
	Environment           string
	Addr                  string
	DatabaseURL           string
	DockerHost            string
	NginxConfigPath       string
	NginxReloadCmd        string
	Workdir               string
	GitTimeout            time.Duration
	BuildTimeout          time.Duration
	Registry              string
	LogChannelName        string
	LogCallbackURL        string
	DeployCallbackURL     string
	DeployCallbackTimeout time.Duration
	MetricsSampleEvery    time.Duration
}

// LoadBuilderConfig constructs a BuilderConfig from environment variables.
func LoadBuilderConfig() BuilderConfig {
	return BuilderConfig{
		Environment:           GetString("APP_ENV", "development"),
		Addr:                  GetString("BUILDER_ADDR", ":5000"),
		DatabaseURL:           GetString("DATABASE_URL", "postgres://vercel:vercel@db:5432/vercel?sslmode=disable"),
		DockerHost:            GetString("DOCKER_HOST", "unix:///var/run/docker.sock"),
		NginxConfigPath:       GetString("NGINX_CONFIG_PATH", "/etc/nginx/conf.d"),
		NginxReloadCmd:        GetString("NGINX_RELOAD_CMD", "nginx -s reload"),
		Workdir:               GetString("BUILDER_WORKDIR", "/tmp/peep"),
		GitTimeout:            time.Duration(GetInt("GIT_TIMEOUT_SECONDS", 60)) * time.Second,
		BuildTimeout:          time.Duration(GetInt("BUILD_TIMEOUT_SECONDS", 600)) * time.Second,
		Registry:              GetString("DOCKER_REGISTRY", "peep"),
		LogChannelName:        GetString("PG_LOG_CHANNEL", "project_logs"),
		LogCallbackURL:        GetString("LOG_CALLBACK_URL", ""),
		DeployCallbackURL:     GetString("DEPLOY_CALLBACK_URL", ""),
		DeployCallbackTimeout: time.Duration(GetInt("DEPLOY_CALLBACK_TIMEOUT_SECONDS", 10)) * time.Second,
		MetricsSampleEvery:    time.Duration(GetInt("METRICS_SAMPLE_SECONDS", 10)) * time.Second,
	}
}
