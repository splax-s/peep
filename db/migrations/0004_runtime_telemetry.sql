-- +goose Up
CREATE TABLE IF NOT EXISTS runtime_events (
    id BIGSERIAL PRIMARY KEY,
    project_id UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    source TEXT NOT NULL,
    event_type TEXT NOT NULL,
    level TEXT NOT NULL DEFAULT 'info',
    message TEXT,
    method TEXT,
    path TEXT,
    status_code INTEGER,
    latency_ms NUMERIC(12,3),
    bytes_in BIGINT,
    bytes_out BIGINT,
    metadata JSONB,
    occurred_at TIMESTAMPTZ NOT NULL,
    ingested_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS runtime_events_project_occurred_idx
    ON runtime_events (project_id, occurred_at DESC);

CREATE INDEX IF NOT EXISTS runtime_events_project_type_idx
    ON runtime_events (project_id, event_type);

CREATE TABLE IF NOT EXISTS runtime_metrics_rollups (
    project_id UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    bucket_start TIMESTAMPTZ NOT NULL,
    bucket_span_seconds INTEGER NOT NULL,
    source TEXT NOT NULL,
    event_type TEXT NOT NULL,
    count BIGINT NOT NULL,
    error_count BIGINT NOT NULL,
    p50_ms NUMERIC(12,3),
    p90_ms NUMERIC(12,3),
    p95_ms NUMERIC(12,3),
    p99_ms NUMERIC(12,3),
    max_ms NUMERIC(12,3),
    avg_ms NUMERIC(12,3),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (project_id, bucket_start, bucket_span_seconds, source, event_type)
);

CREATE INDEX IF NOT EXISTS runtime_metrics_rollups_project_bucket_idx
    ON runtime_metrics_rollups (project_id, bucket_start DESC);

-- +goose Down
DROP INDEX IF EXISTS runtime_metrics_rollups_project_bucket_idx;
DROP TABLE IF EXISTS runtime_metrics_rollups;

DROP INDEX IF EXISTS runtime_events_project_type_idx;
DROP INDEX IF EXISTS runtime_events_project_occurred_idx;
DROP TABLE IF EXISTS runtime_events;
