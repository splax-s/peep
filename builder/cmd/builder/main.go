package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"log/slog"

	"github.com/splax/localvercel/builder/internal/docker"
	httpx "github.com/splax/localvercel/builder/internal/http"
	"github.com/splax/localvercel/builder/internal/runtime"
	"github.com/splax/localvercel/builder/internal/runtime/kubernetes"
	"github.com/splax/localvercel/builder/internal/service/deploy"
	"github.com/splax/localvercel/builder/internal/workspace"
	"github.com/splax/localvercel/pkg/config"
	"github.com/splax/localvercel/pkg/logger"
	"github.com/splax/localvercel/pkg/runtime/telemetry"
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

	var runtimeEmitter deploy.TelemetryEmitter
	if trimmed := strings.TrimSpace(cfg.RuntimeTelemetryURL); trimmed != "" {
		client := &http.Client{Timeout: cfg.RuntimeTelemetryTimeout}
		emitter, err := telemetry.NewEmitter(trimmed, cfg.BuilderAuthToken, client)
		if err != nil {
			log.Warn("runtime telemetry emitter init failed", "error", err)
		} else {
			runtimeEmitter = emitter
		}
	}

	var runtimeManager runtime.Manager
	if cfg.RuntimeBackend == "kubernetes" {
		mgr, err := kubernetes.New(cfg.RuntimeNamespace, cfg.RuntimeServiceDomain, cfg.RuntimeServicePort, cfg.RuntimeReadyTimeout, log)
		if err != nil {
			log.Error("kubernetes runtime manager init failed", "error", err)
			os.Exit(1)
		}
		runtimeManager = mgr
	}

	deploySvc := deploy.New(dockerClient, workspaceManager, log, cfg, runtimeEmitter, runtimeManager)
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
