package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
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
	_ repository.UserRepository         = (*Repository)(nil)
	_ repository.TeamRepository         = (*Repository)(nil)
	_ repository.ProjectRepository      = (*Repository)(nil)
	_ repository.EnvironmentRepository  = (*Repository)(nil)
	_ repository.DeploymentRepository   = (*Repository)(nil)
	_ repository.LogRepository          = (*Repository)(nil)
	_ repository.WebhookRepository      = (*Repository)(nil)
	_ repository.ContainerRepository    = (*Repository)(nil)
	_ repository.RuntimeEventRepository = (*Repository)(nil)
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

// ListTeamsByUser returns teams the user belongs to.
func (r *Repository) ListTeamsByUser(ctx context.Context, userID string) ([]domain.Team, error) {
	const query = `SELECT t.id, t.name, t.owner_id, t.max_projects, t.max_containers, t.storage_limit_mb, t.created_at
		FROM teams t
		INNER JOIN team_members tm ON tm.team_id = t.id
		WHERE tm.user_id = $1
		ORDER BY t.created_at DESC`
	rows, err := r.pool.Query(ctx, query, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	teams := make([]domain.Team, 0)
	for rows.Next() {
		var team domain.Team
		if err := rows.Scan(&team.ID, &team.Name, &team.OwnerID, &team.MaxProjects, &team.MaxContainers, &team.StorageLimitMB, &team.CreatedAt); err != nil {
			return nil, err
		}
		teams = append(teams, team)
	}
	return teams, rows.Err()
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

// ListProjectsByTeam returns projects for the provided team.
func (r *Repository) ListProjectsByTeam(ctx context.Context, teamID string) ([]domain.Project, error) {
	const query = `SELECT id, team_id, name, repo_url, type, build_command, run_command, created_at
		FROM projects WHERE team_id = $1 ORDER BY created_at DESC`
	rows, err := r.pool.Query(ctx, query, teamID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	projects := make([]domain.Project, 0)
	for rows.Next() {
		var project domain.Project
		if err := rows.Scan(&project.ID, &project.TeamID, &project.Name, &project.RepoURL, &project.Type, &project.BuildCommand, &project.RunCommand, &project.CreatedAt); err != nil {
			return nil, err
		}
		projects = append(projects, project)
	}
	return projects, rows.Err()
}

// UpsertEnvVar upserts an environment variable.
func (r *Repository) UpsertEnvVar(ctx context.Context, envVar *domain.ProjectEnvVar) error {
	const query = `INSERT INTO project_env_vars (project_id, key, value, created_at)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (project_id, key) DO UPDATE SET value = EXCLUDED.value`
	_, err := r.pool.Exec(ctx, query, envVar.ProjectID, envVar.Key, envVar.Value, envVar.CreatedAt)
	return err
}

// ListProjectEnvVars returns environment variables for a project.
func (r *Repository) ListProjectEnvVars(ctx context.Context, projectID string) ([]domain.ProjectEnvVar, error) {
	const query = `SELECT project_id, key, value, created_at FROM project_env_vars WHERE project_id = $1 ORDER BY key`
	rows, err := r.pool.Query(ctx, query, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	vars := make([]domain.ProjectEnvVar, 0)
	for rows.Next() {
		var env domain.ProjectEnvVar
		if err := rows.Scan(&env.ProjectID, &env.Key, &env.Value, &env.CreatedAt); err != nil {
			return nil, err
		}
		vars = append(vars, env)
	}
	return vars, rows.Err()
}

// ListEnvironmentsByProject returns environments ordered by position for the project.
func (r *Repository) ListEnvironmentsByProject(ctx context.Context, projectID string) ([]domain.Environment, error) {
	const query = `SELECT id, project_id, slug, name, environment_type, protected, position, created_at, updated_at
		FROM environments WHERE project_id = $1 ORDER BY position ASC, created_at ASC`
	rows, err := r.pool.Query(ctx, query, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	envs := make([]domain.Environment, 0)
	for rows.Next() {
		var env domain.Environment
		if err := rows.Scan(
			&env.ID,
			&env.ProjectID,
			&env.Slug,
			&env.Name,
			&env.EnvironmentType,
			&env.Protected,
			&env.Position,
			&env.CreatedAt,
			&env.UpdatedAt,
		); err != nil {
			return nil, err
		}
		envs = append(envs, env)
	}
	return envs, rows.Err()
}

// GetEnvironmentByID loads a single environment.
func (r *Repository) GetEnvironmentByID(ctx context.Context, environmentID string) (*domain.Environment, error) {
	const query = `SELECT id, project_id, slug, name, environment_type, protected, position, created_at, updated_at
		FROM environments WHERE id = $1`
	row := r.pool.QueryRow(ctx, query, environmentID)
	var env domain.Environment
	if err := row.Scan(
		&env.ID,
		&env.ProjectID,
		&env.Slug,
		&env.Name,
		&env.EnvironmentType,
		&env.Protected,
		&env.Position,
		&env.CreatedAt,
		&env.UpdatedAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, repository.ErrNotFound
		}
		return nil, err
	}
	return &env, nil
}

// CreateEnvironment inserts a new environment record.
func (r *Repository) CreateEnvironment(ctx context.Context, environment *domain.Environment) error {
	if environment == nil {
		return fmt.Errorf("environment required")
	}
	const query = `INSERT INTO environments (id, project_id, slug, name, environment_type, protected, position, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, COALESCE($7, 0), NOW(), NOW())
		RETURNING created_at, updated_at`
	var createdAt, updatedAt time.Time
	err := r.pool.QueryRow(ctx, query,
		environment.ID,
		environment.ProjectID,
		environment.Slug,
		environment.Name,
		environment.EnvironmentType,
		environment.Protected,
		intToNil(environment.Position),
	).Scan(&createdAt, &updatedAt)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) {
			switch pgErr.Code {
			case "23503":
				return repository.ErrNotFound
			case "23514", "22P02", "23505":
				return repository.ErrInvalidArgument
			}
		}
		return err
	}
	environment.CreatedAt = createdAt
	environment.UpdatedAt = updatedAt
	return nil
}

// UpdateEnvironment mutates environment metadata.
func (r *Repository) UpdateEnvironment(ctx context.Context, environment *domain.Environment) error {
	if environment == nil {
		return fmt.Errorf("environment required")
	}
	const query = `UPDATE environments
		SET slug = $2,
			name = $3,
			environment_type = $4,
			protected = $5,
			position = COALESCE($6, position),
			updated_at = NOW()
		WHERE id = $1 RETURNING updated_at`
	row := r.pool.QueryRow(ctx, query,
		environment.ID,
		environment.Slug,
		environment.Name,
		environment.EnvironmentType,
		environment.Protected,
		intToNil(environment.Position),
	)
	var updatedAt time.Time
	if err := row.Scan(&updatedAt); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) {
			switch pgErr.Code {
			case "23505":
				return repository.ErrInvalidArgument
			case "23503":
				return repository.ErrNotFound
			case "23514", "22P02":
				return repository.ErrInvalidArgument
			}
		}
		if errors.Is(err, pgx.ErrNoRows) {
			return repository.ErrNotFound
		}
		return err
	}
	environment.UpdatedAt = updatedAt
	return nil
}

// CreateEnvironmentVersion stores a new environment version and associated variables.
func (r *Repository) CreateEnvironmentVersion(ctx context.Context, version *domain.EnvironmentVersion, vars []domain.EnvironmentVariable) error {
	if version == nil {
		return fmt.Errorf("environment version required")
	}
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	const versionInsert = `INSERT INTO environment_versions (id, environment_id, version, description, created_by, created_at)
		VALUES ($1, $2, $3, $4, $5, NOW()) RETURNING created_at`
	var createdAt time.Time
	if err := tx.QueryRow(ctx, versionInsert,
		version.ID,
		version.EnvironmentID,
		version.Version,
		nilIfEmpty(version.Description),
		stringPtrToNil(version.CreatedBy),
	).Scan(&createdAt); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) {
			switch pgErr.Code {
			case "23503":
				return repository.ErrNotFound
			case "23514", "22P02", "23505":
				return repository.ErrInvalidArgument
			}
		}
		return err
	}
	version.CreatedAt = createdAt

	if len(vars) > 0 {
		const varInsert = `INSERT INTO environment_version_vars (version_id, key, value, checksum)
			VALUES ($1, $2, $3, $4)`
		batch := &pgx.Batch{}
		for _, variable := range vars {
			batch.Queue(varInsert,
				version.ID,
				variable.Key,
				variable.Value,
				stringPtrToNil(variable.Checksum),
			)
		}
		br := tx.SendBatch(ctx, batch)
		for range vars {
			if _, err := br.Exec(); err != nil {
				br.Close()
				var pgErr *pgconn.PgError
				if errors.As(err, &pgErr) {
					switch pgErr.Code {
					case "23503":
						return repository.ErrNotFound
					case "23514", "22P02":
						return repository.ErrInvalidArgument
					case "23505":
						return repository.ErrInvalidArgument
					}
				}
				return err
			}
		}
		if err := br.Close(); err != nil {
			return err
		}
	}

	if _, err := tx.Exec(ctx, `UPDATE environments SET updated_at = NOW() WHERE id = $1`, version.EnvironmentID); err != nil {
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return err
	}
	return nil
}

// ListEnvironmentVersions enumerates versions for an environment.
func (r *Repository) ListEnvironmentVersions(ctx context.Context, environmentID string, limit int) ([]domain.EnvironmentVersion, error) {
	if limit <= 0 {
		limit = 20
	}
	const query = `SELECT id, environment_id, version, description, created_by, created_at
		FROM environment_versions WHERE environment_id = $1 ORDER BY version DESC LIMIT $2`
	rows, err := r.pool.Query(ctx, query, environmentID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	versions := make([]domain.EnvironmentVersion, 0)
	for rows.Next() {
		var (
			version   domain.EnvironmentVersion
			createdBy sql.NullString
		)
		if err := rows.Scan(
			&version.ID,
			&version.EnvironmentID,
			&version.Version,
			&version.Description,
			&createdBy,
			&version.CreatedAt,
		); err != nil {
			return nil, err
		}
		if createdBy.Valid {
			value := createdBy.String
			version.CreatedBy = &value
		}
		versions = append(versions, version)
	}
	return versions, rows.Err()
}

// GetEnvironmentVersion fetches a specific version and its variables.
func (r *Repository) GetEnvironmentVersion(ctx context.Context, versionID string) (*domain.EnvironmentVersion, []domain.EnvironmentVariable, error) {
	const query = `SELECT id, environment_id, version, description, created_by, created_at
		FROM environment_versions WHERE id = $1`
	row := r.pool.QueryRow(ctx, query, versionID)
	var (
		version   domain.EnvironmentVersion
		createdBy sql.NullString
	)
	if err := row.Scan(
		&version.ID,
		&version.EnvironmentID,
		&version.Version,
		&version.Description,
		&createdBy,
		&version.CreatedAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil, repository.ErrNotFound
		}
		return nil, nil, err
	}
	if createdBy.Valid {
		value := createdBy.String
		version.CreatedBy = &value
	}
	vars, err := r.listEnvironmentVariables(ctx, version.ID)
	if err != nil {
		return nil, nil, err
	}
	return &version, vars, nil
}

// GetLatestEnvironmentVersion loads most recent version for an environment.
func (r *Repository) GetLatestEnvironmentVersion(ctx context.Context, environmentID string) (*domain.EnvironmentVersion, []domain.EnvironmentVariable, error) {
	const query = `SELECT id, environment_id, version, description, created_by, created_at
		FROM environment_versions WHERE environment_id = $1 ORDER BY version DESC LIMIT 1`
	row := r.pool.QueryRow(ctx, query, environmentID)
	var (
		version   domain.EnvironmentVersion
		createdBy sql.NullString
	)
	if err := row.Scan(
		&version.ID,
		&version.EnvironmentID,
		&version.Version,
		&version.Description,
		&createdBy,
		&version.CreatedAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil, repository.ErrNotFound
		}
		return nil, nil, err
	}
	if createdBy.Valid {
		value := createdBy.String
		version.CreatedBy = &value
	}
	vars, err := r.listEnvironmentVariables(ctx, version.ID)
	if err != nil {
		return nil, nil, err
	}
	return &version, vars, nil
}

// InsertEnvironmentAudit records an audit entry.
func (r *Repository) InsertEnvironmentAudit(ctx context.Context, audit *domain.EnvironmentAudit) error {
	if audit == nil {
		return fmt.Errorf("audit required")
	}
	const query = `INSERT INTO environment_audits (project_id, environment_id, version_id, actor_id, action, metadata, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, NOW()) RETURNING id, created_at`
	var (
		environmentID any
		versionID     any
		actorID       any
	)
	if audit.EnvironmentID != nil {
		environmentID = nilIfEmpty(*audit.EnvironmentID)
	}
	if audit.VersionID != nil {
		versionID = nilIfEmpty(*audit.VersionID)
	}
	if audit.ActorID != nil {
		actorID = nilIfEmpty(*audit.ActorID)
	}
	var createdAt time.Time
	if err := r.pool.QueryRow(ctx, query,
		audit.ProjectID,
		environmentID,
		versionID,
		actorID,
		audit.Action,
		bytesToNil(audit.Metadata),
	).Scan(&audit.ID, &createdAt); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) {
			switch pgErr.Code {
			case "23503":
				return repository.ErrNotFound
			case "23514", "22P02":
				return repository.ErrInvalidArgument
			}
		}
		return err
	}
	audit.CreatedAt = createdAt
	return nil
}

// ListEnvironmentAudits enumerates recent audit entries for a project.
func (r *Repository) ListEnvironmentAudits(ctx context.Context, projectID, environmentID string, limit int) ([]domain.EnvironmentAudit, error) {
	if limit <= 0 {
		limit = 50
	}
	const query = `SELECT id, project_id, environment_id, version_id, actor_id, action, metadata, created_at
		FROM environment_audits
		WHERE project_id = $1 AND ($2 = '' OR environment_id::text = $2)
		ORDER BY created_at DESC
		LIMIT $3`
	rows, err := r.pool.Query(ctx, query, projectID, strings.TrimSpace(environmentID), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	audits := make([]domain.EnvironmentAudit, 0)
	for rows.Next() {
		var (
			audit     domain.EnvironmentAudit
			envID     sql.NullString
			versionID sql.NullString
			actorID   sql.NullString
			metadata  []byte
		)
		if err := rows.Scan(
			&audit.ID,
			&audit.ProjectID,
			&envID,
			&versionID,
			&actorID,
			&audit.Action,
			&metadata,
			&audit.CreatedAt,
		); err != nil {
			return nil, err
		}
		if envID.Valid {
			value := envID.String
			audit.EnvironmentID = &value
		}
		if versionID.Valid {
			value := versionID.String
			audit.VersionID = &value
		}
		if actorID.Valid {
			value := actorID.String
			audit.ActorID = &value
		}
		if len(metadata) > 0 {
			audit.Metadata = append([]byte(nil), metadata...)
		}
		audits = append(audits, audit)
	}
	return audits, rows.Err()
}

func (r *Repository) listEnvironmentVariables(ctx context.Context, versionID string) ([]domain.EnvironmentVariable, error) {
	const query = `SELECT version_id, key, value, checksum, created_at
		FROM environment_version_vars WHERE version_id = $1 ORDER BY key`
	rows, err := r.pool.Query(ctx, query, versionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	vars := make([]domain.EnvironmentVariable, 0)
	for rows.Next() {
		var (
			variable domain.EnvironmentVariable
			checksum sql.NullString
		)
		if err := rows.Scan(
			&variable.VersionID,
			&variable.Key,
			&variable.Value,
			&checksum,
			&variable.CreatedAt,
		); err != nil {
			return nil, err
		}
		if checksum.Valid {
			value := checksum.String
			variable.Checksum = &value
		}
		vars = append(vars, variable)
	}
	return vars, rows.Err()
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

// GetDeploymentByID fetches a deployment by identifier.
func (r *Repository) GetDeploymentByID(ctx context.Context, deploymentID string) (*domain.Deployment, error) {
	const query = `SELECT id, project_id, commit_sha, status, stage, message, url, error, metadata, started_at, completed_at, updated_at
		FROM deployments WHERE id = $1`
	row := r.pool.QueryRow(ctx, query, deploymentID)
	var d domain.Deployment
	var completedAt sql.NullTime
	if err := row.Scan(&d.ID, &d.ProjectID, &d.CommitSHA, &d.Status, &d.Stage, &d.Message, &d.URL, &d.Error, &d.Metadata, &d.StartedAt, &completedAt, &d.UpdatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, repository.ErrNotFound
		}
		return nil, err
	}
	if completedAt.Valid {
		value := completedAt.Time
		d.CompletedAt = &value
	}
	return &d, nil
}

// DeleteDeployment removes a deployment record.
func (r *Repository) DeleteDeployment(ctx context.Context, deploymentID string) error {
	const query = `DELETE FROM deployments WHERE id = $1`
	cmdTag, err := r.pool.Exec(ctx, query, deploymentID)
	if err != nil {
		return err
	}
	if cmdTag.RowsAffected() == 0 {
		return repository.ErrNotFound
	}
	return nil
}

func emptyToNil(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}

func intPtrToNil(v *int) any {
	if v == nil {
		return nil
	}
	return *v
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

func timePtrToNil(t *time.Time) any {
	if t == nil {
		return nil
	}
	if t.IsZero() {
		return nil
	}
	return t.UTC()
}

// InsertRuntimeEvent persists a runtime telemetry event.
func (r *Repository) InsertRuntimeEvent(ctx context.Context, event *domain.RuntimeEvent) error {
	if event == nil {
		return fmt.Errorf("runtime event required")
	}
	source := strings.TrimSpace(event.Source)
	if source == "" {
		source = "runtime"
	}
	event.Source = source
	eventType := strings.TrimSpace(event.EventType)
	if eventType == "" {
		eventType = "http_request"
	}
	event.EventType = eventType
	level := strings.TrimSpace(event.Level)
	if level == "" {
		level = "info"
	}
	event.Level = level
	message := strings.TrimSpace(event.Message)
	if message != event.Message {
		event.Message = message
	}
	method := strings.TrimSpace(event.Method)
	if method != event.Method {
		event.Method = method
	}
	path := strings.TrimSpace(event.Path)
	if path != event.Path {
		event.Path = path
	}
	occurred := event.OccurredAt
	if occurred.IsZero() {
		occurred = time.Now().UTC()
	}
	const query = `INSERT INTO runtime_events (
		project_id,
		source,
		event_type,
		level,
		message,
		method,
		path,
		status_code,
		latency_ms,
		bytes_in,
		bytes_out,
		metadata,
		occurred_at,
		ingested_at
	) VALUES (
		$1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,COALESCE($14, NOW())
	) RETURNING id, ingested_at`
	var (
		metadata any
	)
	if len(event.Metadata) > 0 {
		metadata = event.Metadata
	}
	var (
		id       int64
		ingested time.Time
	)
	err := r.pool.QueryRow(ctx, query,
		event.ProjectID,
		emptyToNil(event.Source),
		emptyToNil(event.EventType),
		emptyToNil(event.Level),
		nilIfEmpty(event.Message),
		nilIfEmpty(event.Method),
		nilIfEmpty(event.Path),
		intPtrToNil(event.StatusCode),
		floatPtrToNil(event.LatencyMS),
		int64PtrToNil(event.BytesIn),
		int64PtrToNil(event.BytesOut),
		metadata,
		occurred,
		nilTime(event.IngestedAt),
	).Scan(&id, &ingested)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) {
			switch pgErr.Code {
			case "23503":
				return repository.ErrNotFound
			case "23514", "22P02":
				return repository.ErrInvalidArgument
			}
		}
		return err
	}
	event.ID = id
	event.OccurredAt = occurred
	event.IngestedAt = ingested
	return nil
}

func nilIfEmpty(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}

func nilTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t
}

func stringPtrToNil(v *string) any {
	if v == nil {
		return nil
	}
	if strings.TrimSpace(*v) == "" {
		return nil
	}
	return *v
}

func bytesToNil(b []byte) any {
	if len(b) == 0 {
		return nil
	}
	return b
}

// ListRuntimeEvents returns recent runtime telemetry events for a project.
func (r *Repository) ListRuntimeEvents(ctx context.Context, projectID string, eventType string, limit, offset int) ([]domain.RuntimeEvent, error) {
	if limit <= 0 {
		limit = 100
	}
	if offset < 0 {
		offset = 0
	}
	const query = `SELECT
		id,
		project_id,
		source,
		event_type,
		level,
		message,
		method,
		path,
		status_code,
		latency_ms,
		bytes_in,
		bytes_out,
		metadata,
		occurred_at,
		ingested_at
	FROM runtime_events
	WHERE project_id = $1 AND ($2 = '' OR event_type = $2)
	ORDER BY occurred_at DESC, id DESC
	LIMIT $3 OFFSET $4`
	rows, err := r.pool.Query(ctx, query, projectID, strings.TrimSpace(eventType), limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	events := make([]domain.RuntimeEvent, 0)
	for rows.Next() {
		var (
			e        domain.RuntimeEvent
			status   sql.NullInt32
			latency  sql.NullFloat64
			bytesIn  sql.NullInt64
			bytesOut sql.NullInt64
			metadata []byte
		)
		if err := rows.Scan(
			&e.ID,
			&e.ProjectID,
			&e.Source,
			&e.EventType,
			&e.Level,
			&e.Message,
			&e.Method,
			&e.Path,
			&status,
			&latency,
			&bytesIn,
			&bytesOut,
			&metadata,
			&e.OccurredAt,
			&e.IngestedAt,
		); err != nil {
			return nil, err
		}
		if status.Valid {
			value := int(status.Int32)
			e.StatusCode = &value
		}
		if latency.Valid {
			value := latency.Float64
			e.LatencyMS = &value
		}
		if bytesIn.Valid {
			value := bytesIn.Int64
			e.BytesIn = &value
		}
		if bytesOut.Valid {
			value := bytesOut.Int64
			e.BytesOut = &value
		}
		if len(metadata) > 0 {
			e.Metadata = append([]byte(nil), metadata...)
		}
		events = append(events, e)
	}
	return events, rows.Err()
}

// UpsertRuntimeRollups writes aggregated metrics for runtime events.
func (r *Repository) UpsertRuntimeRollups(ctx context.Context, rollups []domain.RuntimeMetricRollup) error {
	if len(rollups) == 0 {
		return nil
	}
	const query = `INSERT INTO runtime_metrics_rollups (
		project_id,
		bucket_start,
		bucket_span_seconds,
		source,
		event_type,
		count,
		error_count,
		p50_ms,
		p90_ms,
		p95_ms,
		p99_ms,
		max_ms,
		avg_ms,
		updated_at
	) VALUES (
		$1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,NOW()
	) ON CONFLICT (project_id, bucket_start, bucket_span_seconds, source, event_type)
	DO UPDATE SET
		count = EXCLUDED.count,
		error_count = EXCLUDED.error_count,
		p50_ms = EXCLUDED.p50_ms,
		p90_ms = EXCLUDED.p90_ms,
		p95_ms = EXCLUDED.p95_ms,
		p99_ms = EXCLUDED.p99_ms,
		max_ms = EXCLUDED.max_ms,
		avg_ms = EXCLUDED.avg_ms,
		updated_at = NOW()`
	batch := &pgx.Batch{}
	for _, rollup := range rollups {
		spanSeconds := int(rollup.BucketSpan.Seconds())
		if spanSeconds <= 0 {
			spanSeconds = 60
		}
		batch.Queue(query,
			rollup.ProjectID,
			rollup.BucketStart,
			spanSeconds,
			emptyToNil(rollup.Source),
			emptyToNil(rollup.EventType),
			rollup.Count,
			rollup.ErrorCount,
			floatPtrToNil(rollup.P50MS),
			floatPtrToNil(rollup.P90MS),
			floatPtrToNil(rollup.P95MS),
			floatPtrToNil(rollup.P99MS),
			floatPtrToNil(rollup.MaxMS),
			floatPtrToNil(rollup.AvgMS),
		)
	}
	br := r.pool.SendBatch(ctx, batch)
	defer br.Close()
	for range rollups {
		if _, err := br.Exec(); err != nil {
			return err
		}
	}
	return nil
}

// ListRuntimeRollups returns aggregated metrics for a project.
func (r *Repository) ListRuntimeRollups(ctx context.Context, projectID string, eventType string, source string, bucketSpan time.Duration, limit int) ([]domain.RuntimeMetricRollup, error) {
	if limit <= 0 {
		limit = 100
	}
	spanSeconds := int(bucketSpan.Seconds())
	if spanSeconds <= 0 {
		spanSeconds = 60
	}
	const query = `SELECT
		project_id,
		bucket_start,
		bucket_span_seconds,
		source,
		event_type,
		count,
		error_count,
		p50_ms,
		p90_ms,
		p95_ms,
		p99_ms,
		max_ms,
		avg_ms,
		updated_at
	FROM runtime_metrics_rollups
	WHERE project_id = $1
		AND bucket_span_seconds = $2
		AND ($3 = '' OR event_type = $3)
		AND ($4 = '' OR source = $4)
	ORDER BY bucket_start DESC
	LIMIT $5`
	rows, err := r.pool.Query(ctx, query, projectID, spanSeconds, strings.TrimSpace(eventType), strings.TrimSpace(source), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	rollups := make([]domain.RuntimeMetricRollup, 0)
	for rows.Next() {
		var (
			r                            domain.RuntimeMetricRollup
			bucketSpanSeconds            int
			p50, p90, p95, p99, max, avg sql.NullFloat64
		)
		if err := rows.Scan(
			&r.ProjectID,
			&r.BucketStart,
			&bucketSpanSeconds,
			&r.Source,
			&r.EventType,
			&r.Count,
			&r.ErrorCount,
			&p50,
			&p90,
			&p95,
			&p99,
			&max,
			&avg,
			&r.UpdatedAt,
		); err != nil {
			return nil, err
		}
		if bucketSpanSeconds > 0 {
			r.BucketSpan = time.Duration(bucketSpanSeconds) * time.Second
		}
		if p50.Valid {
			value := p50.Float64
			r.P50MS = &value
		}
		if p90.Valid {
			value := p90.Float64
			r.P90MS = &value
		}
		if p95.Valid {
			value := p95.Float64
			r.P95MS = &value
		}
		if p99.Valid {
			value := p99.Float64
			r.P99MS = &value
		}
		if max.Valid {
			value := max.Float64
			r.MaxMS = &value
		}
		if avg.Valid {
			value := avg.Float64
			r.AvgMS = &value
		}
		rollups = append(rollups, r)
	}
	return rollups, rows.Err()
}

// AppendLog persists a log line.
func (r *Repository) AppendLog(ctx context.Context, log domain.ProjectLog) error {
	const query = `INSERT INTO project_logs (project_id, source, level, message, metadata, created_at)
		VALUES ($1, $2, $3, $4, $5, $6)`
	_, err := r.pool.Exec(ctx, query, log.ProjectID, log.Source, log.Level, log.Message, log.Metadata, log.CreatedAt)
	if err == nil {
		return nil
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "22P02":
			return repository.ErrInvalidArgument
		case "23503":
			return repository.ErrNotFound
		}
	}
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
	const query = `INSERT INTO project_containers (project_id, deployment_id, container_id, status, cpu_percent, memory_bytes, uptime_seconds, host_ip, host_port, last_heartbeat_at, ttl_expires_at, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, NOW(), NOW())
	ON CONFLICT (container_id) DO UPDATE SET
			deployment_id = EXCLUDED.deployment_id,
			status = EXCLUDED.status,
			cpu_percent = EXCLUDED.cpu_percent,
			memory_bytes = EXCLUDED.memory_bytes,
			uptime_seconds = EXCLUDED.uptime_seconds,
			host_ip = EXCLUDED.host_ip,
			host_port = EXCLUDED.host_port,
			last_heartbeat_at = COALESCE(EXCLUDED.last_heartbeat_at, project_containers.last_heartbeat_at),
			ttl_expires_at = COALESCE(EXCLUDED.ttl_expires_at, project_containers.ttl_expires_at),
		updated_at = NOW()`
	_, err := r.pool.Exec(ctx, query,
		container.ProjectID,
		emptyToNil(container.DeploymentID),
		container.ContainerID,
		container.Status,
		floatPtrToNil(container.CPUPercent),
		int64PtrToNil(container.MemoryBytes),
		int64PtrToNil(container.UptimeSeconds),
		emptyToNil(container.HostIP),
		intToNil(container.HostPort),
		timePtrToNil(container.LastHeartbeatAt),
		timePtrToNil(container.TTLExpiresAt),
	)
	return err
}

// DeleteContainer removes container metadata.
func (r *Repository) DeleteContainer(ctx context.Context, containerID string) error {
	const query = `DELETE FROM project_containers WHERE container_id = $1`
	_, err := r.pool.Exec(ctx, query, containerID)
	return err
}

// DeleteContainersByDeployment removes all containers associated with a deployment.
func (r *Repository) DeleteContainersByDeployment(ctx context.Context, deploymentID string) error {
	const query = `DELETE FROM project_containers WHERE deployment_id = $1`
	_, err := r.pool.Exec(ctx, query, deploymentID)
	return err
}

// ListProjectContainers returns containers associated with a project.
func (r *Repository) ListProjectContainers(ctx context.Context, projectID string) ([]domain.ProjectContainer, error) {
	const query = `SELECT id, project_id, deployment_id, container_id, status, cpu_percent, memory_bytes, uptime_seconds, host_ip, host_port, last_heartbeat_at, ttl_expires_at, created_at, updated_at
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
			cpu           sql.NullFloat64
			mem           sql.NullInt64
			uptime        sql.NullInt64
			hostIP        sql.NullString
			hostPort      sql.NullInt64
			lastHeartbeat sql.NullTime
			ttlExpires    sql.NullTime
		)
		if err := rows.Scan(&c.ID, &c.ProjectID, &c.DeploymentID, &c.ContainerID, &c.Status, &cpu, &mem, &uptime, &hostIP, &hostPort, &lastHeartbeat, &ttlExpires, &c.CreatedAt, &c.UpdatedAt); err != nil {
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
		if lastHeartbeat.Valid {
			value := lastHeartbeat.Time.UTC()
			c.LastHeartbeatAt = &value
		}
		if ttlExpires.Valid {
			value := ttlExpires.Time.UTC()
			c.TTLExpiresAt = &value
		}
		containers = append(containers, c)
	}
	return containers, rows.Err()
}

// ListContainers returns all tracked containers.
func (r *Repository) ListContainers(ctx context.Context) ([]domain.ProjectContainer, error) {
	const query = `SELECT id, project_id, deployment_id, container_id, status, cpu_percent, memory_bytes, uptime_seconds, host_ip, host_port, last_heartbeat_at, ttl_expires_at, created_at, updated_at FROM project_containers`
	rows, err := r.pool.Query(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var containers []domain.ProjectContainer
	for rows.Next() {
		var c domain.ProjectContainer
		var (
			cpu           sql.NullFloat64
			mem           sql.NullInt64
			uptime        sql.NullInt64
			hostIP        sql.NullString
			hostPort      sql.NullInt64
			lastHeartbeat sql.NullTime
			ttlExpires    sql.NullTime
		)
		if err := rows.Scan(&c.ID, &c.ProjectID, &c.DeploymentID, &c.ContainerID, &c.Status, &cpu, &mem, &uptime, &hostIP, &hostPort, &lastHeartbeat, &ttlExpires, &c.CreatedAt, &c.UpdatedAt); err != nil {
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
		if lastHeartbeat.Valid {
			value := lastHeartbeat.Time.UTC()
			c.LastHeartbeatAt = &value
		}
		if ttlExpires.Valid {
			value := ttlExpires.Time.UTC()
			c.TTLExpiresAt = &value
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

// ListDeploymentsWithStatusUpdatedBefore finds deployments with a matching status updated before the cutoff.
func (r *Repository) ListDeploymentsWithStatusUpdatedBefore(ctx context.Context, status string, updatedBefore time.Time) ([]domain.Deployment, error) {
	const query = `SELECT id, project_id, commit_sha, status, stage, message, url, error, metadata, started_at, completed_at, updated_at
		FROM deployments WHERE status = $1 AND updated_at < $2`
	rows, err := r.pool.Query(ctx, query, status, updatedBefore)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var deployments []domain.Deployment
	for rows.Next() {
		var d domain.Deployment
		var completedAt sql.NullTime
		if err := rows.Scan(&d.ID, &d.ProjectID, &d.CommitSHA, &d.Status, &d.Stage, &d.Message, &d.URL, &d.Error, &d.Metadata, &d.StartedAt, &completedAt, &d.UpdatedAt); err != nil {
			return nil, err
		}
		if completedAt.Valid {
			value := completedAt.Time
			d.CompletedAt = &value
		}
		deployments = append(deployments, d)
	}
	return deployments, rows.Err()
}
