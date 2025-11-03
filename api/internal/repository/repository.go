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

// WebhookRepository stores webhook secrets.
type WebhookRepository interface {
	UpsertWebhook(ctx context.Context, projectID string, secret []byte) error
	GetWebhookSecret(ctx context.Context, projectID string) ([]byte, error)
}
