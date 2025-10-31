package migrate

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

// Runner wraps database migration capabilities.
type Runner struct {
	pool          *pgxpool.Pool
	dsn           string
	migrationsDir string
	log           *slog.Logger
}

// New returns a migration runner backed by goose.
func New(pool *pgxpool.Pool, dsn, migrationsDir string, log *slog.Logger) (Runner, error) {
	if pool == nil {
		return Runner{}, errors.New("nil pool provided")
	}
	if dsn == "" {
		return Runner{}, errors.New("empty database dsn")
	}
	if migrationsDir == "" {
		return Runner{}, errors.New("empty migrations directory")
	}
	if _, err := os.Stat(migrationsDir); err != nil {
		return Runner{}, fmt.Errorf("locate migrations dir: %w", err)
	}
	if log == nil {
		log = slog.Default()
	}

	return Runner{pool: pool, dsn: dsn, migrationsDir: migrationsDir, log: log}, nil
}

// Ensure applies pending migrations.
func (r Runner) Ensure(ctx context.Context) error {
	return r.withDB(func(db *sql.DB) error {
		if err := goose.SetDialect("postgres"); err != nil {
			return fmt.Errorf("configure goose: %w", err)
		}

		runCtx, cancel := context.WithTimeout(ctx, time.Minute)
		defer cancel()

		r.log.Info("applying migrations", "dir", r.migrationsDir)
		if err := goose.UpContext(runCtx, db, r.migrationsDir); err != nil {
			return fmt.Errorf("apply migrations: %w", err)
		}
		r.log.Info("migrations applied")
		return nil
	})
}

// Status reports applied and pending migrations.
func (r Runner) Status(ctx context.Context) error {
	return r.withDB(func(db *sql.DB) error {
		if err := goose.SetDialect("postgres"); err != nil {
			return fmt.Errorf("configure goose: %w", err)
		}

		r.log.Info("migration status", "dir", r.migrationsDir)
		if err := goose.Status(db, r.migrationsDir); err != nil {
			return fmt.Errorf("migration status: %w", err)
		}
		return nil
	})
}

// Down rolls back migrations either to the previous version or a specific target version.
func (r Runner) Down(ctx context.Context, targetVersion int64) error {
	return r.withDB(func(db *sql.DB) error {
		if err := goose.SetDialect("postgres"); err != nil {
			return fmt.Errorf("configure goose: %w", err)
		}

		runCtx, cancel := context.WithTimeout(ctx, time.Minute)
		defer cancel()

		if targetVersion > 0 {
			r.log.Info("rolling back migrations", "target", targetVersion)
			if err := goose.DownToContext(runCtx, db, r.migrationsDir, targetVersion); err != nil {
				return fmt.Errorf("rollback to version %d: %w", targetVersion, err)
			}
		} else {
			r.log.Info("rolling back latest migration")
			if err := goose.DownContext(runCtx, db, r.migrationsDir); err != nil {
				return fmt.Errorf("rollback latest migration: %w", err)
			}
		}

		r.log.Info("rollback complete")
		return nil
	})
}

// Ping ensures the database connection is alive.
func (r Runner) Ping(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := r.pool.Ping(ctx); err != nil {
		return fmt.Errorf("ping database: %w", err)
	}
	return nil
}

// Close releases underlying connections.
func (r Runner) Close() {
	r.pool.Close()
}

func (r Runner) withDB(fn func(*sql.DB) error) error {
	db, err := sql.Open("pgx", r.dsn)
	if err != nil {
		return fmt.Errorf("open sql connection: %w", err)
	}
	defer db.Close()

	if err := db.Ping(); err != nil {
		return fmt.Errorf("ping sql connection: %w", err)
	}

	return fn(db)
}
