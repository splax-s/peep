-- +goose Up
CREATE TABLE IF NOT EXISTS environments (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    project_id UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    slug TEXT NOT NULL,
    name TEXT NOT NULL,
    environment_type TEXT NOT NULL,
    protected BOOLEAN NOT NULL DEFAULT FALSE,
    position INTEGER NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (project_id, slug)
);

CREATE INDEX IF NOT EXISTS environments_project_position_idx
    ON environments (project_id, position);

CREATE TABLE IF NOT EXISTS environment_versions (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    environment_id UUID NOT NULL REFERENCES environments(id) ON DELETE CASCADE,
    version INTEGER NOT NULL,
    description TEXT,
    created_by UUID REFERENCES users(id),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (environment_id, version)
);

CREATE INDEX IF NOT EXISTS environment_versions_env_created_idx
    ON environment_versions (environment_id, created_at DESC);

CREATE TABLE IF NOT EXISTS environment_version_vars (
    version_id UUID NOT NULL REFERENCES environment_versions(id) ON DELETE CASCADE,
    key TEXT NOT NULL,
    value BYTEA NOT NULL,
    checksum TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (version_id, key)
);

CREATE TABLE IF NOT EXISTS environment_audits (
    id BIGSERIAL PRIMARY KEY,
    project_id UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    environment_id UUID REFERENCES environments(id) ON DELETE CASCADE,
    version_id UUID REFERENCES environment_versions(id) ON DELETE CASCADE,
    actor_id UUID REFERENCES users(id),
    action TEXT NOT NULL,
    metadata JSONB,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS environment_audits_project_created_idx
    ON environment_audits (project_id, created_at DESC);

ALTER TABLE deployments
    ADD COLUMN IF NOT EXISTS environment_id UUID REFERENCES environments(id),
    ADD COLUMN IF NOT EXISTS environment_version_id UUID REFERENCES environment_versions(id),
    ADD COLUMN IF NOT EXISTS rolled_back_from UUID REFERENCES deployments(id);

CREATE INDEX IF NOT EXISTS deployments_env_idx
    ON deployments (project_id, environment_id, started_at DESC);

-- seed default environments for existing projects
INSERT INTO environments (id, project_id, slug, name, environment_type, protected, position)
SELECT uuid_generate_v4(), p.id, defaults.slug, defaults.name, defaults.environment_type, defaults.protected, defaults.position
FROM projects p
CROSS JOIN (
    VALUES
        ('dev', 'Development', 'development', FALSE, 1),
        ('staging', 'Staging', 'staging', FALSE, 2),
        ('prod', 'Production', 'production', TRUE, 3)
) AS defaults(slug, name, environment_type, protected, position)
ON CONFLICT DO NOTHING;

INSERT INTO environment_versions (id, environment_id, version, description)
SELECT uuid_generate_v4(), env.id, 1, 'Initial snapshot'
FROM environments env
WHERE NOT EXISTS (
    SELECT 1 FROM environment_versions ev WHERE ev.environment_id = env.id
);

INSERT INTO environment_version_vars (version_id, key, value, created_at)
SELECT ev.id, pev.key, pev.value, NOW()
FROM environment_versions ev
JOIN environments env ON env.id = ev.environment_id
JOIN project_env_vars pev ON pev.project_id = env.project_id
WHERE ev.version = 1;

INSERT INTO environment_audits (project_id, environment_id, version_id, actor_id, action, metadata)
SELECT env.project_id, env.id, ev.id, NULL, 'bootstrap', jsonb_build_object('version', ev.version)
FROM environment_versions ev
JOIN environments env ON env.id = ev.environment_id
WHERE ev.version = 1;

-- +goose Down
DROP INDEX IF EXISTS deployments_env_idx;
ALTER TABLE deployments
    DROP COLUMN IF EXISTS rolled_back_from,
    DROP COLUMN IF EXISTS environment_version_id,
    DROP COLUMN IF EXISTS environment_id;

DROP INDEX IF EXISTS environment_audits_project_created_idx;
DROP TABLE IF EXISTS environment_audits;

DROP TABLE IF EXISTS environment_version_vars;
DROP INDEX IF EXISTS environment_versions_env_created_idx;
DROP TABLE IF EXISTS environment_versions;

DROP INDEX IF EXISTS environments_project_position_idx;
DROP TABLE IF EXISTS environments;
