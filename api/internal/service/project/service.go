package project

import (
	"context"
	"errors"
	"strings"
	"time"

	"log/slog"

	"github.com/google/uuid"

	"github.com/splax/localvercel/api/internal/domain"
	"github.com/splax/localvercel/api/internal/repository"
	"github.com/splax/localvercel/pkg/config"
	"github.com/splax/localvercel/pkg/crypto"
)

// CreateInput encapsulates project creation attributes.
type CreateInput struct {
	TeamID       string
	Name         string
	RepoURL      string
	Type         string
	BuildCommand string
	RunCommand   string
}

// EnvVarInput holds environment variable data.
type EnvVarInput struct {
	ProjectID string
	Key       string
	Value     string
}

// Service orchestrates project management.
type Service struct {
	projects repository.ProjectRepository
	teams    repository.TeamRepository
	logger   *slog.Logger
	cfg      config.APIConfig
}

// New returns a project service.
func New(projects repository.ProjectRepository, teams repository.TeamRepository, logger *slog.Logger, cfg config.APIConfig) Service {
	return Service{projects: projects, teams: teams, logger: logger, cfg: cfg}
}

// EnvVar represents a decrypted environment variable for API responses.
type EnvVar struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

var (
	errInvalidProjectName = errors.New("project name is required")
	errInvalidRepoURL     = errors.New("repository URL is required")
	errInvalidType        = errors.New("project type must be frontend or backend")
	errInvalidEnvKey      = errors.New("environment variable key is required")
	errMissingTeamID      = errors.New("team id required")
	errMissingProjectID   = errors.New("project id required")
)

// Create registers a new project respecting team quotas.
func (s Service) Create(ctx context.Context, input CreateInput) (*domain.Project, error) {
	if strings.TrimSpace(input.Name) == "" {
		return nil, errInvalidProjectName
	}
	if strings.TrimSpace(input.RepoURL) == "" {
		return nil, errInvalidRepoURL
	}
	typeNormalized := strings.ToLower(strings.TrimSpace(input.Type))
	if typeNormalized != "frontend" && typeNormalized != "backend" {
		return nil, errInvalidType
	}
	team, err := s.teams.GetTeamByID(ctx, input.TeamID)
	if err != nil {
		return nil, err
	}
	count, err := s.teams.CountProjects(ctx, input.TeamID)
	if err != nil {
		return nil, err
	}
	if count >= team.MaxProjects {
		return nil, errors.New("team project quota exceeded")
	}
	project := &domain.Project{
		ID:           uuid.NewString(),
		TeamID:       input.TeamID,
		Name:         input.Name,
		RepoURL:      input.RepoURL,
		Type:         typeNormalized,
		BuildCommand: input.BuildCommand,
		RunCommand:   input.RunCommand,
		CreatedAt:    time.Now().UTC(),
	}
	if err := s.projects.CreateProject(ctx, project); err != nil {
		return nil, err
	}
	s.logger.Info("project created", "project_id", project.ID, "team_id", project.TeamID)
	return project, nil
}

// AddEnvVar encrypts and stores an environment variable.
func (s Service) AddEnvVar(ctx context.Context, input EnvVarInput) error {
	if strings.TrimSpace(input.Key) == "" {
		return errInvalidEnvKey
	}
	ciphertext, err := crypto.EncryptString(s.cfg.EnvEncryptionKey, input.Value)
	if err != nil {
		return err
	}
	envVar := &domain.ProjectEnvVar{
		ProjectID: input.ProjectID,
		Key:       input.Key,
		Value:     ciphertext,
		CreatedAt: time.Now().UTC(),
	}
	return s.projects.UpsertEnvVar(ctx, envVar)
}

// ListByTeam returns projects owned by the team.
func (s Service) ListByTeam(ctx context.Context, teamID string) ([]domain.Project, error) {
	teamID = strings.TrimSpace(teamID)
	if teamID == "" {
		return nil, errMissingTeamID
	}
	projects, err := s.projects.ListProjectsByTeam(ctx, teamID)
	if err != nil {
		return nil, err
	}
	return projects, nil
}

// ListEnvVars decrypts stored environment variables for a project.
func (s Service) ListEnvVars(ctx context.Context, projectID string) ([]EnvVar, error) {
	projectID = strings.TrimSpace(projectID)
	if projectID == "" {
		return nil, errMissingProjectID
	}
	stored, err := s.projects.ListProjectEnvVars(ctx, projectID)
	if err != nil {
		return nil, err
	}
	vars := make([]EnvVar, 0, len(stored))
	for _, item := range stored {
		value, err := crypto.DecryptToString(s.cfg.EnvEncryptionKey, item.Value)
		if err != nil {
			if s.logger != nil {
				s.logger.Warn("failed to decrypt env var", "project_id", projectID, "key", item.Key, "error", err)
			}
			continue
		}
		vars = append(vars, EnvVar{Key: item.Key, Value: value})
	}
	return vars, nil
}

// Get returns project details by identifier.
func (s Service) Get(ctx context.Context, projectID string) (*domain.Project, error) {
	projectID = strings.TrimSpace(projectID)
	if projectID == "" {
		return nil, errMissingProjectID
	}
	project, err := s.projects.GetProjectByID(ctx, projectID)
	if err != nil {
		return nil, err
	}
	return project, nil
}
