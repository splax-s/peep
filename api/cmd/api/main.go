package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/splax/localvercel/api/internal/app/migrate"
	httpx "github.com/splax/localvercel/api/internal/http"
	"github.com/splax/localvercel/api/internal/repository/postgres"
	"github.com/splax/localvercel/api/internal/service/auth"
	"github.com/splax/localvercel/api/internal/service/deploy"
	"github.com/splax/localvercel/api/internal/service/environment"
	"github.com/splax/localvercel/api/internal/service/ingress"
	"github.com/splax/localvercel/api/internal/service/logs"
	"github.com/splax/localvercel/api/internal/service/project"
	runtimectl "github.com/splax/localvercel/api/internal/service/runtime"
	"github.com/splax/localvercel/api/internal/service/team"
	"github.com/splax/localvercel/api/internal/service/webhook"
	"github.com/splax/localvercel/api/internal/ws"
	"github.com/splax/localvercel/pkg/config"
	"github.com/splax/localvercel/pkg/logger"
)

func main() {
	cfg := config.LoadAPIConfig()
	log := logger.New("api", slog.LevelInfo)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Error("failed to connect to database", "error", err)
		os.Exit(1)
	}

	runner, err := migrate.New(pool, cfg.DatabaseURL, cfg.MigrationsDir, log)
	if err != nil {
		log.Error("failed to configure migrations", "error", err)
		os.Exit(1)
	}
	defer runner.Close()
	if err := runner.Ping(ctx); err != nil {
		log.Error("database ping failed", "error", err)
		os.Exit(1)
	}
	if err := runner.Ensure(ctx); err != nil {
		log.Error("migrations failed", "error", err)
		os.Exit(1)
	}

	repo := postgres.New(pool)
	logHub := ws.NewHub()
	runtimeHub := ws.NewHub()

	authSvc := auth.New(repo, log, cfg)
	teamSvc := team.New(repo, log)
	projectSvc := project.New(repo, repo, log, cfg)
	environmentSvc := environment.New(repo, repo, log, cfg)
	logSvc := logs.New(repo, logHub, log)
	ingressSvc := ingress.New(cfg, log)
	defer ingressSvc.Close()
	deploySvc := deploy.New(repo, repo, repo, ingressSvc, log, cfg, logSvc)
	webhookSvc := webhook.New(repo, log, cfg)

	runtimeSvc := runtimectl.NewTelemetryService(repo, runtimeHub, log, cfg.RuntimeMetricBucketSpan, cfg.RuntimeMetricFlushEvery)
	if runtimeSvc != nil {
		go runtimeSvc.Run(ctx)
	}

	runtimeCtl := runtimectl.New(repo, repo, repo, ingressSvc, log, cfg)
	if runtimeCtl != nil {
		go runtimeCtl.Run(ctx)
	}

	limiter := httpx.NewMemoryRateLimiter()
	if addr := strings.TrimSpace(cfg.RateLimitRedisAddr); addr != "" {
		redisLimiter, err := httpx.NewRedisRateLimiter(addr, cfg.RateLimitRedisPass, cfg.RateLimitRedisDB, log)
		if err != nil {
			log.Warn("redis rate limiter unavailable", "error", err)
		} else {
			limiter = redisLimiter
		}
	}

	router := httpx.NewRouter(log, authSvc, teamSvc, projectSvc, environmentSvc, deploySvc, logSvc, runtimeSvc, webhookSvc, limiter, cfg.BuilderAuthToken, pool.Ping)
	defer router.Close()

	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           router,
		ReadHeaderTimeout: 5 * time.Second,
	}

	errorCh := make(chan error, 1)
	go func() {
		log.Info("api server starting", "addr", cfg.Addr)
		errorCh <- srv.ListenAndServe()
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			log.Error("graceful shutdown failed", "error", err)
		}
		log.Info("api server stopped")
	case err := <-errorCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("server error", "error", err)
			os.Exit(1)
		}
	}
}
