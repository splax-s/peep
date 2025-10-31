package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/splax/localvercel/api/internal/app/migrate"
	"github.com/splax/localvercel/pkg/config"
	"github.com/splax/localvercel/pkg/logger"
)

func main() {
	command := flag.String("command", "up", "migrate command (up|status|down)")
	timeout := flag.Duration("timeout", time.Minute, "command timeout")
	target := flag.Int64("target", 0, "target version for down command (optional)")
	flag.Parse()

	cfg := config.LoadAPIConfig()
	log := logger.New("migrate", slog.LevelInfo)

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Error("failed to connect to database", "error", err)
		os.Exit(1)
	}

	runner, err := migrate.New(pool, cfg.DatabaseURL, cfg.MigrationsDir, log)
	if err != nil {
		log.Error("failed to configure migration runner", "error", err)
		os.Exit(1)
	}
	defer runner.Close()

	switch *command {
	case "up":
		if err := runner.Ensure(ctx); err != nil {
			log.Error("failed to apply migrations", "error", err)
			os.Exit(1)
		}
	case "status":
		if err := runner.Status(ctx); err != nil {
			log.Error("failed to fetch migration status", "error", err)
			os.Exit(1)
		}
	case "down":
		if err := runner.Down(ctx, *target); err != nil {
			log.Error("failed to roll back migrations", "error", err)
			os.Exit(1)
		}
	default:
		log.Error("unsupported command", "command", *command)
		os.Exit(1)
	}

	log.Info("migration command completed", "command", *command)
}
