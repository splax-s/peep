-- +goose Up
ALTER TABLE project_containers
    ADD COLUMN IF NOT EXISTS deployment_id UUID REFERENCES deployments(id) ON DELETE SET NULL;

CREATE INDEX IF NOT EXISTS project_containers_deployment_id_idx
    ON project_containers(deployment_id);

-- +goose Down
DROP INDEX IF EXISTS project_containers_deployment_id_idx;

ALTER TABLE project_containers
    DROP COLUMN IF EXISTS deployment_id;
