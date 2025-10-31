# Database Migrations

The API service uses [goose](https://github.com/pressly/goose) to manage PostgreSQL schema changes. Migrations live in `db/migrations` and are applied in ascending order at runtime or through the standalone CLI described below.

## Runtime behaviour

- `api/cmd/api` loads configuration via `pkg/config` and calls the migration runner during startup.
- By default the service reads `DB_MIGRATIONS_DIR=../db/migrations`. Override this environment variable if the working directory differs (e.g. when packaging the API into a container image).

## Standalone CLI

The helper located at `api/cmd/migrate` lets you orchestrate schema changes outside the API binary. Typical invocations:

```sh
go run ./cmd/migrate -command up
```

```sh
go run ./cmd/migrate -command status
```

```sh
go run ./cmd/migrate -command down -target 0
```

Flags:

- `-command`: one of `up`, `status`, or `down` (default `up`).
- `-target`: optional version for `down`. A value of `0` rolls back a single migration.
- `-timeout`: maximum duration for any command (default 60s).

All commands honour the same environment variables as the API binary (`DATABASE_URL`, `DB_MIGRATIONS_DIR`, etc.).

## Local development

When running via Docker Compose, the default configuration already points to the shared Postgres container. If you shell into the API container you can apply migrations manually:

```sh
docker compose run --rm api go run ./cmd/migrate -command up
```

## Continuous integration

Include the CLI in CI workflows to assert that migrations apply cleanly:

```sh
go run ./cmd/migrate -command status
```

Expected workflow additions:

1. Run `migrate -command up` against a disposable database before executing integration tests.
2. Optionally run `migrate -command status` after deployments to verify there is no schema drift.

## Authoring migrations

1. Create a new file in `db/migrations` following goose naming conventions (e.g. `0002_add_widgets.sql`).
2. Add `-- +goose Up` / `-- +goose Down` sections with the forward and rollback SQL.
3. Run `go run ./cmd/migrate -command up` locally to validate the script.
4. Commit both the SQL file and any application code relying on the new schema.
