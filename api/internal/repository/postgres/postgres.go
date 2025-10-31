package postgres

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/splax/localvercel/api/internal/domain"
	"github.com/splax/localvercel/api/internal/repository"
)

// Repository implements persistence interfaces on PostgreSQL.
type Repository struct {
	pool *pgxpool.Pool
}

// New constructs a Repository.
func New(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

// ensure Repository satisfies interfaces.
var (
	_ repository.UserRepository       = (*Repository)(nil)
	_ repository.TeamRepository       = (*Repository)(nil)
	_ repository.ProjectRepository    = (*Repository)(nil)
	_ repository.DeploymentRepository = (*Repository)(nil)
	_ repository.LogRepository        = (*Repository)(nil)
	_ repository.WebhookRepository    = (*Repository)(nil)
	_ repository.ContainerRepository  = (*Repository)(nil)
)

// CreateUser inserts a user.
func (r *Repository) CreateUser(ctx context.Context, user *domain.User) error {
	const query = `INSERT INTO users (id, email, password_hash, created_at)
		VALUES ($1, $2, $3, $4)`
	_, err := r.pool.Exec(ctx, query, user.ID, user.Email, user.PasswordHash, user.CreatedAt)
	return err
}

// GetUserByEmail fetches a user by email.
func (r *Repository) GetUserByEmail(ctx context.Context, email string) (*domain.User, error) {
	const query = `SELECT id, email, password_hash, created_at FROM users WHERE email = $1`
	row := r.pool.QueryRow(ctx, query, email)
	var u domain.User
	if err := row.Scan(&u.ID, &u.Email, &u.PasswordHash, &u.CreatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, repository.ErrNotFound
		}
		return nil, err
	}
	return &u, nil
}

// GetUserByID retrieves a user by identifier.
func (r *Repository) GetUserByID(ctx context.Context, id string) (*domain.User, error) {
	const query = `SELECT id, email, password_hash, created_at FROM users WHERE id = $1`
	row := r.pool.QueryRow(ctx, query, id)
	var u domain.User
	if err := row.Scan(&u.ID, &u.Email, &u.PasswordHash, &u.CreatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, repository.ErrNotFound
		}
		return nil, err
	}
	return &u, nil
}

// CreateTeam creates a team record.
func (r *Repository) CreateTeam(ctx context.Context, team *domain.Team) error {
	const query = `INSERT INTO teams (id, name, owner_id, max_projects, max_containers, storage_limit_mb, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`
	_, err := r.pool.Exec(ctx, query, team.ID, team.Name, team.OwnerID, team.MaxProjects, team.MaxContainers, team.StorageLimitMB, team.CreatedAt)
	return err
}

// UpsertMember adds a member to a team.
func (r *Repository) UpsertMember(ctx context.Context, member *domain.TeamMember) error {
	const query = `INSERT INTO team_members (team_id, user_id, role, created_at)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (team_id, user_id) DO UPDATE SET role = EXCLUDED.role`
	_, err := r.pool.Exec(ctx, query, member.TeamID, member.UserID, member.Role, member.CreatedAt)
	return err
}

// CountProjects counts projects assigned to a team.
func (r *Repository) CountProjects(ctx context.Context, teamID string) (int, error) {
	const query = `SELECT COUNT(1) FROM projects WHERE team_id = $1`
	row := r.pool.QueryRow(ctx, query, teamID)
	var count int
	if err := row.Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
}

// GetTeamByID returns a team by identifier.
func (r *Repository) GetTeamByID(ctx context.Context, teamID string) (*domain.Team, error) {
	const query = `SELECT id, name, owner_id, max_projects, max_containers, storage_limit_mb, created_at FROM teams WHERE id = $1`
	row := r.pool.QueryRow(ctx, query, teamID)
	var team domain.Team
	if err := row.Scan(&team.ID, &team.Name, &team.OwnerID, &team.MaxProjects, &team.MaxContainers, &team.StorageLimitMB, &team.CreatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, repository.ErrNotFound
		}
		return nil, err
	}
	return &team, nil
}

// CreateProject inserts a project.
func (r *Repository) CreateProject(ctx context.Context, project *domain.Project) error {
	const query = `INSERT INTO projects (id, team_id, name, repo_url, type, build_command, run_command, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`
	_, err := r.pool.Exec(ctx, query, project.ID, project.TeamID, project.Name, project.RepoURL, project.Type, project.BuildCommand, project.RunCommand, project.CreatedAt)
	return err
}

// GetProjectByID fetches project details.
func (r *Repository) GetProjectByID(ctx context.Context, projectID string) (*domain.Project, error) {
	const query = `SELECT id, team_id, name, repo_url, type, build_command, run_command, created_at
		FROM projects WHERE id = $1`
	row := r.pool.QueryRow(ctx, query, projectID)
	var project domain.Project
	if err := row.Scan(&project.ID, &project.TeamID, &project.Name, &project.RepoURL, &project.Type, &project.BuildCommand, &project.RunCommand, &project.CreatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, repository.ErrNotFound
		}
		return nil, err
	}
	return &project, nil
}

// UpsertEnvVar upserts an environment variable.
func (r *Repository) UpsertEnvVar(ctx context.Context, envVar *domain.ProjectEnvVar) error {
	const query = `INSERT INTO project_env_vars (project_id, key, value, created_at)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (project_id, key) DO UPDATE SET value = EXCLUDED.value`
	_, err := r.pool.Exec(ctx, query, envVar.ProjectID, envVar.Key, envVar.Value, envVar.CreatedAt)
	return err
}

// CreateDeployment inserts a deployment record.
func (r *Repository) CreateDeployment(ctx context.Context, deployment *domain.Deployment) error {
	const query = `INSERT INTO deployments (id, project_id, commit_sha, status, stage, message, url, error, metadata, started_at, completed_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)`
	_, err := r.pool.Exec(ctx, query,
		deployment.ID,
		deployment.ProjectID,
		deployment.CommitSHA,
		deployment.Status,
		deployment.Stage,
		deployment.Message,
		deployment.URL,
		deployment.Error,
		deployment.Metadata,
		deployment.StartedAt,
		deployment.CompletedAt,
		deployment.UpdatedAt,
	)
	return err
}

// UpdateDeploymentStatus updates deployment status.
func (r *Repository) UpdateDeploymentStatus(ctx context.Context, update domain.DeploymentStatusUpdate) error {
	const query = `UPDATE deployments
		SET status = COALESCE($2, status),
			stage = COALESCE($3, stage),
			message = COALESCE($4, message),
			url = COALESCE($5, url),
			error = COALESCE($6, error),
			metadata = COALESCE($7, metadata),
			completed_at = $8,
			updated_at = NOW()
		WHERE id = $1`
	_, err := r.pool.Exec(ctx, query,
		update.DeploymentID,
		emptyToNil(update.Status),
		emptyToNil(update.Stage),
		emptyToNil(update.Message),
		emptyToNil(update.URL),
		emptyToNil(update.Error),
		update.Metadata,
		update.CompletedAt,
	)
	return err
}

// ListDeploymentsByProject fetches recent deployments for a project.
func (r *Repository) ListDeploymentsByProject(ctx context.Context, projectID string, limit int) ([]domain.Deployment, error) {
	if limit <= 0 {
		limit = 20
	}
	const query = `SELECT id, project_id, commit_sha, status, stage, message, url, error, metadata, started_at, completed_at, updated_at
		FROM deployments WHERE project_id = $1 ORDER BY started_at DESC LIMIT $2`
	rows, err := r.pool.Query(ctx, query, projectID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var deployments []domain.Deployment
	for rows.Next() {
		var d domain.Deployment
		if err := rows.Scan(&d.ID, &d.ProjectID, &d.CommitSHA, &d.Status, &d.Stage, &d.Message, &d.URL, &d.Error, &d.Metadata, &d.StartedAt, &d.CompletedAt, &d.UpdatedAt); err != nil {
			return nil, err
		}
		deployments = append(deployments, d)
	}
	return deployments, rows.Err()
}

func emptyToNil(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}

func intToNil(v int) any {
	if v == 0 {
		return nil
	}
	return v
}

func floatPtrToNil(v *float64) any {
	if v == nil {
		return nil
	}
	return *v
}

func int64PtrToNil(v *int64) any {
	if v == nil {
		return nil
	}
	return *v
}

// AppendLog persists a log line.
func (r *Repository) AppendLog(ctx context.Context, log domain.ProjectLog) error {
	const query = `INSERT INTO project_logs (project_id, source, level, message, metadata, created_at)
		VALUES ($1, $2, $3, $4, $5, $6)`
	_, err := r.pool.Exec(ctx, query, log.ProjectID, log.Source, log.Level, log.Message, log.Metadata, log.CreatedAt)
	return err
}

// ListLogsByProject fetches logs for a project.
func (r *Repository) ListLogsByProject(ctx context.Context, projectID string, limit, offset int) ([]domain.ProjectLog, error) {
	const query = `SELECT id, project_id, source, level, message, metadata, created_at
		FROM project_logs WHERE project_id = $1 ORDER BY id DESC LIMIT $2 OFFSET $3`
	rows, err := r.pool.Query(ctx, query, projectID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var logs []domain.ProjectLog
	for rows.Next() {
		var l domain.ProjectLog
		if err := rows.Scan(&l.ID, &l.ProjectID, &l.Source, &l.Level, &l.Message, &l.Metadata, &l.CreatedAt); err != nil {
			return nil, err
		}
		logs = append(logs, l)
	}
	return logs, rows.Err()
}

// UpsertWebhook saves a webhook secret.
func (r *Repository) UpsertWebhook(ctx context.Context, projectID string, secret []byte) error {
	const query = `INSERT INTO project_webhooks (project_id, secret, created_at)
		VALUES ($1, $2, NOW())
		ON CONFLICT (project_id) DO UPDATE SET secret = EXCLUDED.secret`
	_, err := r.pool.Exec(ctx, query, projectID, secret)
	return err
}

// GetWebhookSecret retrieves the stored secret for a project.
func (r *Repository) GetWebhookSecret(ctx context.Context, projectID string) ([]byte, error) {
	const query = `SELECT secret FROM project_webhooks WHERE project_id = $1`
	row := r.pool.QueryRow(ctx, query, projectID)
	var secret []byte
	if err := row.Scan(&secret); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, repository.ErrNotFound
		}
		return nil, err
	}
	return secret, nil
}

// UpsertContainer records container metadata for a project.
func (r *Repository) UpsertContainer(ctx context.Context, container domain.ProjectContainer) error {
	const query = `INSERT INTO project_containers (project_id, container_id, status, cpu_percent, memory_bytes, uptime_seconds, host_ip, host_port, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, NOW(), NOW())
		ON CONFLICT (container_id) DO UPDATE SET
			status = EXCLUDED.status,
			cpu_percent = EXCLUDED.cpu_percent,
			memory_bytes = EXCLUDED.memory_bytes,
			uptime_seconds = EXCLUDED.uptime_seconds,
			host_ip = EXCLUDED.host_ip,
			host_port = EXCLUDED.host_port,
			updated_at = NOW()`
	_, err := r.pool.Exec(ctx, query,
		container.ProjectID,
		container.ContainerID,
		container.Status,
		floatPtrToNil(container.CPUPercent),
		int64PtrToNil(container.MemoryBytes),
		int64PtrToNil(container.UptimeSeconds),
		emptyToNil(container.HostIP),
		intToNil(container.HostPort),
	)
	return err
}

// DeleteContainer removes container metadata.
func (r *Repository) DeleteContainer(ctx context.Context, containerID string) error {
	const query = `DELETE FROM project_containers WHERE container_id = $1`
	_, err := r.pool.Exec(ctx, query, containerID)
	return err
}

// ListProjectContainers returns containers associated with a project.
func (r *Repository) ListProjectContainers(ctx context.Context, projectID string) ([]domain.ProjectContainer, error) {
	const query = `SELECT id, project_id, container_id, status, cpu_percent, memory_bytes, uptime_seconds, host_ip, host_port, created_at, updated_at
		FROM project_containers WHERE project_id = $1 ORDER BY created_at DESC`
	rows, err := r.pool.Query(ctx, query, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var containers []domain.ProjectContainer
	for rows.Next() {
		var c domain.ProjectContainer
		var (
			cpu      sql.NullFloat64
			mem      sql.NullInt64
			uptime   sql.NullInt64
			hostIP   sql.NullString
			hostPort sql.NullInt64
		)
		if err := rows.Scan(&c.ID, &c.ProjectID, &c.ContainerID, &c.Status, &cpu, &mem, &uptime, &hostIP, &hostPort, &c.CreatedAt, &c.UpdatedAt); err != nil {
			return nil, err
		}
		if cpu.Valid {
			value := cpu.Float64
			c.CPUPercent = &value
		}
		if mem.Valid {
			value := mem.Int64
			c.MemoryBytes = &value
		}
		if uptime.Valid {
			value := uptime.Int64
			c.UptimeSeconds = &value
		}
		if hostIP.Valid {
			c.HostIP = hostIP.String
		}
		if hostPort.Valid {
			c.HostPort = int(hostPort.Int64)
		}
		containers = append(containers, c)
	}
	return containers, rows.Err()
}

// RemoveStaleContainers removes all containers for a project except the active one.
func (r *Repository) RemoveStaleContainers(ctx context.Context, projectID, activeContainerID string) error {
	const query = `DELETE FROM project_containers WHERE project_id = $1 AND container_id <> $2`
	_, err := r.pool.Exec(ctx, query, projectID, activeContainerID)
	return err
}
