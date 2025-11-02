package runtime

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/splax/localvercel/api/internal/domain"
	"github.com/splax/localvercel/api/internal/repository"
	"github.com/splax/localvercel/api/internal/service/deploy"
	"github.com/splax/localvercel/pkg/config"
)

func TestControllerRemovesExpiredContainers(t *testing.T) {
	now := time.Now()
	projectID := "project-1"
	containerID := "container-1"

	projects := &testProjectRepo{projects: map[string]domain.Project{
		projectID: {ID: projectID, Name: "Test"},
	}}
	containers := newTestContainerRepo([]domain.ProjectContainer{
		{
			ProjectID:   projectID,
			ContainerID: containerID,
			Status:      deploy.StatusRunning,
			UpdatedAt:   now.Add(-2 * time.Hour),
		},
	})
	deployments := &testDeploymentRepo{}
	ingress := &testIngress{}
	logger := slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{}))

	cfg := config.APIConfig{
		RuntimeReconcileInterval: time.Second,
		RuntimeContainerTTL:      time.Hour,
	}

	ctrl := New(projects, containers, deployments, ingress, logger, cfg)
	if ctrl == nil {
		t.Fatalf("expected controller to be created")
	}
	ctrl.now = func() time.Time { return now }

	ctrl.runIteration(context.Background())

	if containers.count() != 0 {
		t.Fatalf("expected container to be removed, still have %d", containers.count())
	}
	if len(ingress.applied) != 1 {
		t.Fatalf("expected ingress apply to run once, got %d", len(ingress.applied))
	}
	if ingress.applied[0].ProjectID != projectID {
		t.Fatalf("ingress applied for wrong project: %s", ingress.applied[0].ProjectID)
	}
}

func TestControllerRemovesContainersExceedingLimits(t *testing.T) {
	now := time.Now()
	projectID := "project-2"
	containerID := "container-2"
	cpu := 92.4
	mem := int64(900 * 1024 * 1024)

	projects := &testProjectRepo{projects: map[string]domain.Project{projectID: {ID: projectID}}}
	containers := newTestContainerRepo([]domain.ProjectContainer{
		{
			ProjectID:   projectID,
			ContainerID: containerID,
			Status:      deploy.StatusRunning,
			CPUPercent:  &cpu,
			MemoryBytes: &mem,
			UpdatedAt:   now,
		},
	})
	deployments := &testDeploymentRepo{}
	ingress := &testIngress{}
	logger := slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{}))

	cfg := config.APIConfig{
		RuntimeReconcileInterval: time.Second,
		RuntimeCPULimitPercent:   75,
		RuntimeMemoryLimitMB:     512,
	}

	ctrl := New(projects, containers, deployments, ingress, logger, cfg)
	if ctrl == nil {
		t.Fatalf("expected controller to be created")
	}
	ctrl.now = func() time.Time { return now }

	ctrl.runIteration(context.Background())

	if containers.count() != 0 {
		t.Fatalf("expected container to be removed under limits, still have %d", containers.count())
	}
	if len(ingress.applied) != 1 {
		t.Fatalf("expected ingress apply to run once, got %d", len(ingress.applied))
	}
}

func TestControllerTimesOutDeployments(t *testing.T) {
	now := time.Now()
	projectID := "project-3"
	deploymentID := "dep-3"

	projects := &testProjectRepo{projects: map[string]domain.Project{projectID: {ID: projectID}}}
	containers := newTestContainerRepo(nil)
	deployments := &testDeploymentRepo{
		deployments: map[string]domain.Deployment{
			deploymentID: {
				ID:        deploymentID,
				ProjectID: projectID,
				Status:    deploy.StatusRunning,
				UpdatedAt: now.Add(-2 * time.Hour),
			},
		},
	}
	ingress := &testIngress{}
	logger := slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{}))

	cfg := config.APIConfig{
		RuntimeReconcileInterval: time.Second,
		RuntimeDeploymentTTL:     time.Hour,
	}

	ctrl := New(projects, containers, deployments, ingress, logger, cfg)
	if ctrl == nil {
		t.Fatalf("expected controller to be created")
	}
	ctrl.now = func() time.Time { return now }

	ctrl.runIteration(context.Background())

	deployments.mu.Lock()
	defer deployments.mu.Unlock()
	update, ok := deployments.lastUpdate[deploymentID]
	if !ok {
		t.Fatalf("expected deployment update to be recorded")
	}
	if update.Status != deploy.StatusFailed {
		t.Fatalf("expected deployment to be marked failed, got %s", update.Status)
	}
	if update.Stage != runtimeTimeoutStage {
		t.Fatalf("expected stage %s, got %s", runtimeTimeoutStage, update.Stage)
	}
	if update.CompletedAt == nil {
		t.Fatalf("expected completedAt to be set")
	}
}

type testProjectRepo struct {
	projects map[string]domain.Project
}

func (r *testProjectRepo) CreateProject(context.Context, *domain.Project) error      { return nil }
func (r *testProjectRepo) UpsertEnvVar(context.Context, *domain.ProjectEnvVar) error { return nil }
func (r *testProjectRepo) GetProjectByID(ctx context.Context, projectID string) (*domain.Project, error) {
	project, ok := r.projects[projectID]
	if !ok {
		return nil, fmt.Errorf("project %s not found", projectID)
	}
	return &project, nil
}
func newTestContainerRepo(initial []domain.ProjectContainer) *testContainerRepo {
	repo := &testContainerRepo{containers: make(map[string]domain.ProjectContainer)}
	for _, c := range initial {
		repo.containers[c.ContainerID] = c
	}
	return repo
}

type testContainerRepo struct {
	mu         sync.Mutex
	containers map[string]domain.ProjectContainer
}

func (r *testContainerRepo) UpsertContainer(context.Context, domain.ProjectContainer) error {
	return nil
}
func (r *testContainerRepo) RemoveStaleContainers(context.Context, string, string) error { return nil }

func (r *testContainerRepo) DeleteContainer(ctx context.Context, containerID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.containers, containerID)
	return nil
}

func (r *testContainerRepo) ListProjectContainers(ctx context.Context, projectID string) ([]domain.ProjectContainer, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var result []domain.ProjectContainer
	for _, container := range r.containers {
		if container.ProjectID == projectID {
			result = append(result, container)
		}
	}
	return result, nil
}

func (r *testContainerRepo) ListContainers(context.Context) ([]domain.ProjectContainer, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	result := make([]domain.ProjectContainer, 0, len(r.containers))
	for _, c := range r.containers {
		result = append(result, c)
	}
	return result, nil
}

func (r *testContainerRepo) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.containers)
}

type testDeploymentRepo struct {
	mu          sync.Mutex
	deployments map[string]domain.Deployment
	lastUpdate  map[string]domain.DeploymentStatusUpdate
}

func (r *testDeploymentRepo) CreateDeployment(context.Context, *domain.Deployment) error { return nil }

func (r *testDeploymentRepo) UpdateDeploymentStatus(ctx context.Context, update domain.DeploymentStatusUpdate) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.lastUpdate == nil {
		r.lastUpdate = make(map[string]domain.DeploymentStatusUpdate)
	}
	r.lastUpdate[update.DeploymentID] = update
	return nil
}

func (r *testDeploymentRepo) ListDeploymentsByProject(context.Context, string, int) ([]domain.Deployment, error) {
	return nil, nil
}

func (r *testDeploymentRepo) ListDeploymentsWithStatusUpdatedBefore(ctx context.Context, status string, updatedBefore time.Time) ([]domain.Deployment, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var result []domain.Deployment
	for _, dep := range r.deployments {
		if dep.Status == status && dep.UpdatedAt.Before(updatedBefore) {
			result = append(result, dep)
		}
	}
	return result, nil
}

func (r *testDeploymentRepo) GetDeploymentByID(ctx context.Context, id string) (*domain.Deployment, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.deployments == nil {
		return nil, repository.ErrNotFound
	}
	dep, ok := r.deployments[id]
	if !ok {
		return nil, repository.ErrNotFound
	}
	copy := dep
	return &copy, nil
}

func (r *testDeploymentRepo) DeleteDeployment(ctx context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.deployments == nil {
		return repository.ErrNotFound
	}
	if _, ok := r.deployments[id]; !ok {
		return repository.ErrNotFound
	}
	delete(r.deployments, id)
	return nil
}

type testIngress struct {
	mu      sync.Mutex
	applied []ingressApply
}

type ingressApply struct {
	ProjectID string
	Count     int
}

func (t *testIngress) Apply(ctx context.Context, project domain.Project, containers []domain.ProjectContainer) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.applied = append(t.applied, ingressApply{ProjectID: project.ID, Count: len(containers)})
	return nil
}
