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

var (
	errInvalidProjectName = errors.New("project name is required")
	errInvalidRepoURL     = errors.New("repository URL is required")
	errInvalidType        = errors.New("project type must be frontend or backend")
	errInvalidEnvKey      = errors.New("environment variable key is required")
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
