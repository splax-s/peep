package project

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/splax/localvercel/api/internal/domain"
	"github.com/splax/localvercel/api/internal/repository"
	"github.com/splax/localvercel/pkg/config"
	"github.com/splax/localvercel/pkg/crypto"
)

type stubProjectRepository struct {
	envVars   map[string][]domain.ProjectEnvVar
	projects  map[string][]domain.Project
	projectBy map[string]domain.Project
}

func (s *stubProjectRepository) CreateProject(ctx context.Context, project *domain.Project) error {
	return nil
}

func (s *stubProjectRepository) UpsertEnvVar(ctx context.Context, envVar *domain.ProjectEnvVar) error {
	return nil
}

func (s *stubProjectRepository) GetProjectByID(ctx context.Context, projectID string) (*domain.Project, error) {
	if project, ok := s.projectBy[projectID]; ok {
		return &project, nil
	}
	return nil, repository.ErrNotFound
}

func (s *stubProjectRepository) ListProjectsByTeam(ctx context.Context, teamID string) ([]domain.Project, error) {
	return append([]domain.Project(nil), s.projects[teamID]...), nil
}

func (s *stubProjectRepository) ListProjectEnvVars(ctx context.Context, projectID string) ([]domain.ProjectEnvVar, error) {
	return append([]domain.ProjectEnvVar(nil), s.envVars[projectID]...), nil
}

type noopTeamRepository struct{}

func (noopTeamRepository) CreateTeam(ctx context.Context, team *domain.Team) error { return nil }
func (noopTeamRepository) UpsertMember(ctx context.Context, member *domain.TeamMember) error {
	return nil
}
func (noopTeamRepository) CountProjects(ctx context.Context, teamID string) (int, error) {
	return 0, nil
}
func (noopTeamRepository) GetTeamByID(ctx context.Context, teamID string) (*domain.Team, error) {
	return &domain.Team{ID: teamID, MaxProjects: 10}, nil
}
func (noopTeamRepository) ListTeamsByUser(ctx context.Context, userID string) ([]domain.Team, error) {
	return nil, nil
}

func TestListEnvVarsDecryptsValues(t *testing.T) {
	t.Helper()

	secret := "test-secret"
	cipher, err := crypto.EncryptString(secret, "value-123")
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}

	repo := &stubProjectRepository{
		envVars: map[string][]domain.ProjectEnvVar{
			"project-1": {
				{ProjectID: "project-1", Key: "KNOWN", Value: cipher, CreatedAt: time.Now()},
				{ProjectID: "project-1", Key: "BROKEN", Value: []byte("invalid")},
			},
		},
	}

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := Service{
		projects: repo,
		teams:    noopTeamRepository{},
		logger:   log,
		cfg:      config.APIConfig{EnvEncryptionKey: secret},
	}

	vars, err := svc.ListEnvVars(context.Background(), "project-1")
	if err != nil {
		t.Fatalf("ListEnvVars returned error: %v", err)
	}
	if len(vars) != 1 {
		t.Fatalf("expected 1 decrypted env var, got %d", len(vars))
	}
	if vars[0].Key != "KNOWN" || vars[0].Value != "value-123" {
		t.Fatalf("unexpected env var result: %+v", vars[0])
	}
}

func TestListByTeamRequiresTeamID(t *testing.T) {
	svc := Service{projects: &stubProjectRepository{}, teams: noopTeamRepository{}, cfg: config.APIConfig{}}
	if _, err := svc.ListByTeam(context.Background(), " "); !errors.Is(err, errMissingTeamID) {
		t.Fatalf("expected errMissingTeamID, got %v", err)
	}
}
