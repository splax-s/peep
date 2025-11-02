package deploy

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/splax/localvercel/api/internal/domain"
	"github.com/splax/localvercel/api/internal/repository"
	"github.com/splax/localvercel/api/internal/service/logs"
	"github.com/splax/localvercel/pkg/config"
)

func TestProcessCallbackRejectsInvalidProjectID(t *testing.T) {
	depRepo := &fakeDeploymentRepo{}
	svc := newTestService(func(s *Service) {
		s.deployments = depRepo
	})

	err := svc.ProcessCallback(context.Background(), CallbackPayload{
		DeploymentID: "dep-123",
		ProjectID:    "not-a-uuid",
		Status:       "running",
		Stage:        "workspace",
	})

	if !errors.Is(err, ErrInvalidProjectID) {
		t.Fatalf("expected ErrInvalidProjectID, got %v", err)
	}
	if depRepo.updateCalls > 0 {
		t.Fatalf("expected no status updates, got %d", depRepo.updateCalls)
	}
}

func TestProcessCallbackPropagatesNotFound(t *testing.T) {
	depRepo := &fakeDeploymentRepo{updateErr: repository.ErrNotFound}
	svc := newTestService(func(s *Service) {
		s.deployments = depRepo
	})

	payload := CallbackPayload{
		DeploymentID: "dep-123",
		ProjectID:    uuid.NewString(),
		Status:       "running",
		Stage:        "workspace",
		Message:      "updating",
		Timestamp:    time.Now().UTC(),
	}

	err := svc.ProcessCallback(context.Background(), payload)
	if !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("expected repository.ErrNotFound, got %v", err)
	}
	if depRepo.updateCalls != 1 {
		t.Fatalf("expected exactly one status update, got %d", depRepo.updateCalls)
	}
}

func TestProcessCallbackSkipsStatusUpdateForMetricsStage(t *testing.T) {
	depRepo := &fakeDeploymentRepo{}
	svc := newTestService(func(s *Service) {
		s.deployments = depRepo
	})

	payload := CallbackPayload{
		DeploymentID: "dep-123",
		ProjectID:    uuid.NewString(),
		Status:       "running",
		Stage:        "metrics",
		Metadata: map[string]any{
			"container_id": "container-1",
			"cpu_percent":  12.5,
		},
	}

	if err := svc.ProcessCallback(context.Background(), payload); err != nil {
		t.Fatalf("ProcessCallback returned error: %v", err)
	}
	if depRepo.updateCalls != 0 {
		t.Fatalf("expected no status updates for metrics stage, got %d", depRepo.updateCalls)
	}
}

type fakeDeploymentRepo struct {
	updateCalls int
	lastUpdate  domain.DeploymentStatusUpdate
	updateErr   error
	getErr      error
	deleteErr   error
	deployment  *domain.Deployment
}

func (f *fakeDeploymentRepo) CreateDeployment(context.Context, *domain.Deployment) error {
	return nil
}

func (f *fakeDeploymentRepo) UpdateDeploymentStatus(ctx context.Context, update domain.DeploymentStatusUpdate) error {
	f.updateCalls++
	f.lastUpdate = update
	return f.updateErr
}

func (f *fakeDeploymentRepo) ListDeploymentsByProject(context.Context, string, int) ([]domain.Deployment, error) {
	return nil, nil
}

func (f *fakeDeploymentRepo) ListDeploymentsWithStatusUpdatedBefore(context.Context, string, time.Time) ([]domain.Deployment, error) {
	return nil, nil
}

func (f *fakeDeploymentRepo) GetDeploymentByID(ctx context.Context, id string) (*domain.Deployment, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	if f.deployment != nil {
		return f.deployment, nil
	}
	return &domain.Deployment{ID: id, ProjectID: uuid.NewString()}, nil
}

func (f *fakeDeploymentRepo) DeleteDeployment(context.Context, string) error {
	return f.deleteErr
}

type fakeProjectRepo struct{}

func (fakeProjectRepo) CreateProject(context.Context, *domain.Project) error { return nil }
func (fakeProjectRepo) UpsertEnvVar(context.Context, *domain.ProjectEnvVar) error {
	return nil
}
func (fakeProjectRepo) GetProjectByID(ctx context.Context, projectID string) (*domain.Project, error) {
	return &domain.Project{ID: projectID, Name: "test", RepoURL: "https://example.com"}, nil
}

type fakeContainerRepo struct{}

func (fakeContainerRepo) UpsertContainer(context.Context, domain.ProjectContainer) error { return nil }
func (fakeContainerRepo) DeleteContainer(context.Context, string) error                  { return nil }
func (fakeContainerRepo) ListProjectContainers(context.Context, string) ([]domain.ProjectContainer, error) {
	return nil, nil
}
func (fakeContainerRepo) RemoveStaleContainers(context.Context, string, string) error { return nil }
func (fakeContainerRepo) ListContainers(context.Context) ([]domain.ProjectContainer, error) {
	return nil, nil
}

type fakeLogRepo struct{}

func (fakeLogRepo) AppendLog(context.Context, domain.ProjectLog) error { return nil }
func (fakeLogRepo) ListLogsByProject(context.Context, string, int, int) ([]domain.ProjectLog, error) {
	return nil, nil
}

type serviceOption func(*Service)

func newTestService(opts ...serviceOption) Service {
	logger := slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelDebug}))
	svc := Service{
		projects:    fakeProjectRepo{},
		deployments: &fakeDeploymentRepo{},
		containers:  nil,
		client:      nil,
		logger:      logger,
		ingress:     nil,
		cfg:         config.APIConfig{},
		logSvc:      logs.New(fakeLogRepo{}, nil, logger),
	}
	for _, opt := range opts {
		opt(&svc)
	}
	return svc
}
