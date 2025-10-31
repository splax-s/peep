-- +goose Up
ALTER TABLE project_containers
    ADD COLUMN IF NOT EXISTS host_ip TEXT,
    ADD COLUMN IF NOT EXISTS host_port INTEGER;

CREATE UNIQUE INDEX IF NOT EXISTS project_containers_container_id_idx
    ON project_containers(container_id);

-- +goose Down
DROP INDEX IF EXISTS project_containers_container_id_idx;

ALTER TABLE project_containers
    DROP COLUMN IF EXISTS host_port,
    DROP COLUMN IF EXISTS host_ip;
