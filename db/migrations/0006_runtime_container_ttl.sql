-- +goose Up
ALTER TABLE project_containers
    ADD COLUMN IF NOT EXISTS last_heartbeat_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS ttl_expires_at TIMESTAMPTZ;

UPDATE project_containers
SET last_heartbeat_at = COALESCE(last_heartbeat_at, updated_at);

-- +goose Down
ALTER TABLE project_containers
    DROP COLUMN IF EXISTS ttl_expires_at,
    DROP COLUMN IF EXISTS last_heartbeat_at;
