package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"log/slog"

	"github.com/splax/localvercel/builder/internal/docker"
	httpx "github.com/splax/localvercel/builder/internal/http"
	"github.com/splax/localvercel/builder/internal/service/deploy"
	"github.com/splax/localvercel/builder/internal/workspace"
	"github.com/splax/localvercel/pkg/config"
	"github.com/splax/localvercel/pkg/logger"
)

func main() {
	cfg := config.LoadBuilderConfig()
	log := logger.New("builder", slog.LevelInfo)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	dockerClient, err := docker.New(cfg.DockerHost)
	if err != nil {
		log.Error("failed to create docker client", "error", err)
		os.Exit(1)
	}
	defer dockerClient.Close()

	if err := dockerClient.Ping(ctx); err != nil {
		log.Error("docker ping failed", "error", err)
		os.Exit(1)
	}

	workspaceManager, err := workspace.New(cfg.Workdir)
	if err != nil {
		log.Error("workspace init failed", "error", err, "workdir", cfg.Workdir)
		os.Exit(1)
	}

	deploySvc := deploy.New(dockerClient, workspaceManager, log, cfg)
	router := httpx.New(log, deploySvc)

	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           router,
		ReadHeaderTimeout: 5 * time.Second,
	}

	errorCh := make(chan error, 1)
	go func() {
		log.Info("builder server starting", "addr", cfg.Addr)
		errorCh <- srv.ListenAndServe()
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			log.Error("graceful shutdown failed", "error", err)
		}
		log.Info("builder server stopped")
	case err := <-errorCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("server error", "error", err)
			os.Exit(1)
		}
	}
}
