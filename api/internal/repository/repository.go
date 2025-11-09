package repository

import (
	"context"
	"time"

	"github.com/splax/localvercel/api/internal/domain"
)

// UserRepository persists users.
type UserRepository interface {
	CreateUser(ctx context.Context, user *domain.User) error
	GetUserByEmail(ctx context.Context, email string) (*domain.User, error)
	GetUserByID(ctx context.Context, id string) (*domain.User, error)
}

// TeamRepository manages teams and memberships.
type TeamRepository interface {
	CreateTeam(ctx context.Context, team *domain.Team) error
	UpsertMember(ctx context.Context, member *domain.TeamMember) error
	CountProjects(ctx context.Context, teamID string) (int, error)
	GetTeamByID(ctx context.Context, teamID string) (*domain.Team, error)
	ListTeamsByUser(ctx context.Context, userID string) ([]domain.Team, error)
}

// ProjectRepository persists project configuration.
type ProjectRepository interface {
	CreateProject(ctx context.Context, project *domain.Project) error
	UpsertEnvVar(ctx context.Context, envVar *domain.ProjectEnvVar) error
	GetProjectByID(ctx context.Context, projectID string) (*domain.Project, error)
	ListProjectsByTeam(ctx context.Context, teamID string) ([]domain.Project, error)
	ListProjectEnvVars(ctx context.Context, projectID string) ([]domain.ProjectEnvVar, error)
}

// EnvironmentRepository persists environment hierarchy and versions.
type EnvironmentRepository interface {
	ListEnvironmentsByProject(ctx context.Context, projectID string) ([]domain.Environment, error)
	GetEnvironmentByID(ctx context.Context, environmentID string) (*domain.Environment, error)
	CreateEnvironment(ctx context.Context, environment *domain.Environment) error
	UpdateEnvironment(ctx context.Context, environment *domain.Environment) error
	CreateEnvironmentVersion(ctx context.Context, version *domain.EnvironmentVersion, vars []domain.EnvironmentVariable) error
	ListEnvironmentVersions(ctx context.Context, environmentID string, limit int) ([]domain.EnvironmentVersion, error)
	GetEnvironmentVersion(ctx context.Context, versionID string) (*domain.EnvironmentVersion, []domain.EnvironmentVariable, error)
	GetLatestEnvironmentVersion(ctx context.Context, environmentID string) (*domain.EnvironmentVersion, []domain.EnvironmentVariable, error)
	InsertEnvironmentAudit(ctx context.Context, audit *domain.EnvironmentAudit) error
	ListEnvironmentAudits(ctx context.Context, projectID, environmentID string, limit int) ([]domain.EnvironmentAudit, error)
}

// ContainerRepository stores running container metadata.
type ContainerRepository interface {
	UpsertContainer(ctx context.Context, container domain.ProjectContainer) error
	DeleteContainer(ctx context.Context, containerID string) error
	DeleteContainersByDeployment(ctx context.Context, deploymentID string) error
	ListProjectContainers(ctx context.Context, projectID string) ([]domain.ProjectContainer, error)
	RemoveStaleContainers(ctx context.Context, projectID, activeContainerID string) error
	ListContainers(ctx context.Context) ([]domain.ProjectContainer, error)
}

// DeploymentRepository stores deployment history.
type DeploymentRepository interface {
	CreateDeployment(ctx context.Context, deployment *domain.Deployment) error
	UpdateDeploymentStatus(ctx context.Context, update domain.DeploymentStatusUpdate) error
	ListDeploymentsByProject(ctx context.Context, projectID string, limit int) ([]domain.Deployment, error)
	ListDeploymentsWithStatusUpdatedBefore(ctx context.Context, status string, updatedBefore time.Time) ([]domain.Deployment, error)
	GetDeploymentByID(ctx context.Context, deploymentID string) (*domain.Deployment, error)
	DeleteDeployment(ctx context.Context, deploymentID string) error
}

// LogRepository handles log persistence and retrieval.
type LogRepository interface {
	AppendLog(ctx context.Context, log domain.ProjectLog) error
	ListLogsByProject(ctx context.Context, projectID string, limit, offset int) ([]domain.ProjectLog, error)
}

// RuntimeEventRepository handles runtime telemetry persistence and aggregation access.
type RuntimeEventRepository interface {
	InsertRuntimeEvent(ctx context.Context, event *domain.RuntimeEvent) error
	ListRuntimeEvents(ctx context.Context, projectID string, eventType string, limit, offset int) ([]domain.RuntimeEvent, error)
	UpsertRuntimeRollups(ctx context.Context, rollups []domain.RuntimeMetricRollup) error
	ListRuntimeRollups(ctx context.Context, projectID string, eventType string, source string, bucketSpan time.Duration, limit int) ([]domain.RuntimeMetricRollup, error)
}

// WebhookRepository stores webhook secrets.
type WebhookRepository interface {
	UpsertWebhook(ctx context.Context, projectID string, secret []byte) error
	GetWebhookSecret(ctx context.Context, projectID string) ([]byte, error)
}
