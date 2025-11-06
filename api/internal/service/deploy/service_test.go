package deploy

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"strings"
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

func TestProcessCallbackNormalizesDeploymentURL(t *testing.T) {
	projectID := uuid.NewString()
	depRepo := &fakeDeploymentRepo{}
	project := domain.Project{ID: projectID, Name: "My App"}
	cfg := config.APIConfig{IngressDomainSuffix: ".dev.peep", Environment: "production"}

	svc := newTestService(func(s *Service) {
		s.deployments = depRepo
		s.projects = singleProjectRepo{project: project}
		s.cfg = cfg
	})

	payload := CallbackPayload{
		DeploymentID: uuid.NewString(),
		ProjectID:    projectID,
		Status:       "running",
		Stage:        "ready",
		URL:          "http://127.0.0.1:49155",
		Metadata: map[string]any{
			"host_port": "49155",
		},
		Timestamp: time.Now().UTC(),
	}

	if err := svc.ProcessCallback(context.Background(), payload); err != nil {
		t.Fatalf("ProcessCallback returned error: %v", err)
	}

	if depRepo.updateCalls != 1 {
		t.Fatalf("expected one status update, got %d", depRepo.updateCalls)
	}

	gotURL := strings.TrimSpace(depRepo.lastUpdate.URL)
	wantURL := "https://my-app.dev.peep:8080"
	if gotURL != wantURL {
		t.Fatalf("expected normalized url %q, got %q", wantURL, gotURL)
	}

	if len(depRepo.lastUpdate.Metadata) == 0 {
		t.Fatal("expected metadata to be persisted")
	}
	var meta map[string]any
	if err := json.Unmarshal(depRepo.lastUpdate.Metadata, &meta); err != nil {
		t.Fatalf("decode metadata failed: %v", err)
	}
	if meta["public_url"] != wantURL {
		t.Fatalf("expected public_url metadata %q, got %v", wantURL, meta["public_url"])
	}
	if payload.URL != "http://127.0.0.1:49155" {
		t.Fatalf("expected original payload to remain unchanged, got %q", payload.URL)
	}
	if _, ok := meta["builder_url"]; ok {
		t.Fatal("expected internal builder_url not to be exposed")
	}
}

func TestProcessCallbackPreservesHttpsScheme(t *testing.T) {
	projectID := uuid.NewString()
	depRepo := &fakeDeploymentRepo{}
	project := domain.Project{ID: projectID, Name: "My App"}

	svc := newTestService(func(s *Service) {
		s.deployments = depRepo
		s.projects = singleProjectRepo{project: project}
		s.cfg = config.APIConfig{IngressDomainSuffix: ".dev.peep", Environment: "production"}
	})

	payload := CallbackPayload{
		DeploymentID: uuid.NewString(),
		ProjectID:    projectID,
		Status:       "running",
		Stage:        "ready",
		URL:          "https://127.0.0.1:51234/app",
		Metadata: map[string]any{
			"host_port": "51234",
		},
		Timestamp: time.Now().UTC(),
	}

	if err := svc.ProcessCallback(context.Background(), payload); err != nil {
		t.Fatalf("ProcessCallback returned error: %v", err)
	}

	gotURL := strings.TrimSpace(depRepo.lastUpdate.URL)
	wantURL := "https://my-app.dev.peep:8080/app"
	if gotURL != wantURL {
		t.Fatalf("expected normalized url %q, got %q", wantURL, gotURL)
	}
	var meta map[string]any
	if err := json.Unmarshal(depRepo.lastUpdate.Metadata, &meta); err != nil {
		t.Fatalf("decode metadata failed: %v", err)
	}
	if meta["public_url"] != wantURL {
		t.Fatalf("expected public_url metadata %q, got %v", wantURL, meta["public_url"])
	}
}

func TestProcessCallbackGeneratesURLFromHostPortWhenURLMissing(t *testing.T) {
	projectID := uuid.NewString()
	depRepo := &fakeDeploymentRepo{}
	project := domain.Project{ID: projectID, Name: "Awesome"}

	svc := newTestService(func(s *Service) {
		s.deployments = depRepo
		s.projects = singleProjectRepo{project: project}
		s.cfg = config.APIConfig{IngressDomainSuffix: ".example.test", Environment: "staging"}
	})

	payload := CallbackPayload{
		DeploymentID: uuid.NewString(),
		ProjectID:    projectID,
		Status:       "running",
		Stage:        "ready",
		Metadata: map[string]any{
			"host_port": 60001,
		},
		Timestamp: time.Now().UTC(),
	}

	if err := svc.ProcessCallback(context.Background(), payload); err != nil {
		t.Fatalf("ProcessCallback returned error: %v", err)
	}

	gotURL := strings.TrimSpace(depRepo.lastUpdate.URL)
	wantURL := "http://awesome.example.test:8080"
	if gotURL != wantURL {
		t.Fatalf("expected url derived from host port %q, got %q", wantURL, gotURL)
	}
	var meta map[string]any
	if err := json.Unmarshal(depRepo.lastUpdate.Metadata, &meta); err != nil {
		t.Fatalf("decode metadata failed: %v", err)
	}
	if _, ok := meta["builder_url"]; ok {
		t.Fatal("expected no builder_url when original url missing")
	}
	if meta["public_url"] != wantURL {
		t.Fatalf("expected public_url %q, got %v", wantURL, meta["public_url"])
	}
}

func TestNormalizeDeploymentURLFallsBackToRawOnProjectLookupFailure(t *testing.T) {
	projectID := uuid.NewString()
	depRepo := &fakeDeploymentRepo{}

	svc := newTestService(func(s *Service) {
		s.deployments = depRepo
		s.projects = errorProjectRepo{}
		s.cfg = config.APIConfig{IngressDomainSuffix: ".dev.peep"}
	})

	original := "http://127.0.0.1:60002"
	payload := CallbackPayload{
		DeploymentID: uuid.NewString(),
		ProjectID:    projectID,
		Status:       "running",
		Stage:        "ready",
		URL:          original,
		Metadata: map[string]any{
			"host_port": "60002",
		},
		Timestamp: time.Now().UTC(),
	}

	if err := svc.ProcessCallback(context.Background(), payload); err != nil {
		t.Fatalf("ProcessCallback returned error: %v", err)
	}
	if !strings.Contains(depRepo.lastUpdate.URL, "127.0.0.1") {
		t.Fatalf("expected raw url to pass through on lookup failure, got %q", depRepo.lastUpdate.URL)
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
func (fakeProjectRepo) ListProjectsByTeam(context.Context, string) ([]domain.Project, error) {
	return nil, nil
}
func (fakeProjectRepo) ListProjectEnvVars(context.Context, string) ([]domain.ProjectEnvVar, error) {
	return nil, nil
}

type singleProjectRepo struct {
	project domain.Project
}

func (s singleProjectRepo) CreateProject(context.Context, *domain.Project) error { return nil }

func (s singleProjectRepo) UpsertEnvVar(context.Context, *domain.ProjectEnvVar) error { return nil }

func (s singleProjectRepo) GetProjectByID(context.Context, string) (*domain.Project, error) {
	projectCopy := s.project
	return &projectCopy, nil
}

func (s singleProjectRepo) ListProjectsByTeam(context.Context, string) ([]domain.Project, error) {
	return []domain.Project{s.project}, nil
}

func (s singleProjectRepo) ListProjectEnvVars(context.Context, string) ([]domain.ProjectEnvVar, error) {
	return nil, nil
}

type errorProjectRepo struct{}

func (errorProjectRepo) CreateProject(context.Context, *domain.Project) error { return nil }

func (errorProjectRepo) UpsertEnvVar(context.Context, *domain.ProjectEnvVar) error { return nil }

func (errorProjectRepo) GetProjectByID(context.Context, string) (*domain.Project, error) {
	return nil, errors.New("lookup failed")
}

func (errorProjectRepo) ListProjectsByTeam(context.Context, string) ([]domain.Project, error) {
	return nil, errors.New("lookup failed")
}

func (errorProjectRepo) ListProjectEnvVars(context.Context, string) ([]domain.ProjectEnvVar, error) {
	return nil, errors.New("lookup failed")
}

type fakeContainerRepo struct{}

func (fakeContainerRepo) UpsertContainer(context.Context, domain.ProjectContainer) error { return nil }
func (fakeContainerRepo) DeleteContainer(context.Context, string) error                  { return nil }
func (fakeContainerRepo) DeleteContainersByDeployment(context.Context, string) error     { return nil }
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
