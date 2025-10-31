package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/splax/localvercel/api/internal/app/migrate"
	httpx "github.com/splax/localvercel/api/internal/http"
	"github.com/splax/localvercel/api/internal/repository/postgres"
	"github.com/splax/localvercel/api/internal/service/auth"
	"github.com/splax/localvercel/api/internal/service/deploy"
	"github.com/splax/localvercel/api/internal/service/ingress"
	"github.com/splax/localvercel/api/internal/service/logs"
	"github.com/splax/localvercel/api/internal/service/project"
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
	hub := ws.NewHub()

	authSvc := auth.New(repo, log, cfg)
	teamSvc := team.New(repo, log)
	projectSvc := project.New(repo, repo, log, cfg)
	logSvc := logs.New(repo, hub, log)
	ingressSvc := ingress.New(cfg, log)
	defer ingressSvc.Close()
	deploySvc := deploy.New(repo, repo, repo, ingressSvc, log, cfg, logSvc)
	webhookSvc := webhook.New(repo, log, cfg)

	router := httpx.NewRouter(log, authSvc, teamSvc, projectSvc, deploySvc, logSvc, webhookSvc)

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
