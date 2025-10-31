package deploy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"log/slog"

	"github.com/google/uuid"

	"github.com/splax/localvercel/api/internal/domain"
	"github.com/splax/localvercel/api/internal/repository"
	"github.com/splax/localvercel/api/internal/service/ingress"
	"github.com/splax/localvercel/api/internal/service/logs"
	"github.com/splax/localvercel/pkg/config"
)

// Status constants for deployments.
const (
	StatusPending = "pending"
	StatusRunning = "running"
	StatusFailed  = "failed"
	StatusSuccess = "success"
)

// Service orchestrates deployments via the builder service.
type Service struct {
	projects    repository.ProjectRepository
	deployments repository.DeploymentRepository
	containers  repository.ContainerRepository
	client      *http.Client
	logger      *slog.Logger
	ingress     *ingress.Service
	cfg         config.APIConfig
	logSvc      logs.Service
}

// New returns a deployment service.
func New(projects repository.ProjectRepository, deployments repository.DeploymentRepository, containers repository.ContainerRepository, ingressSvc *ingress.Service, logger *slog.Logger, cfg config.APIConfig, logSvc logs.Service) Service {
	return Service{
		projects:    projects,
		deployments: deployments,
		containers:  containers,
		client:      &http.Client{Timeout: 30 * time.Second},
		logger:      logger,
		ingress:     ingressSvc,
		cfg:         cfg,
		logSvc:      logSvc,
	}
}

// Trigger initiates a deployment and notifies builder.
func (s Service) Trigger(ctx context.Context, projectID, commitSHA string) (*domain.Deployment, error) {
	project, err := s.projects.GetProjectByID(ctx, projectID)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	deployment := &domain.Deployment{
		ID:        uuid.NewString(),
		ProjectID: project.ID,
		CommitSHA: commitSHA,
		Status:    StatusPending,
		Stage:     "queued",
		Message:   "deployment requested",
		StartedAt: now,
		UpdatedAt: now,
	}
	if err := s.deployments.CreateDeployment(ctx, deployment); err != nil {
		return nil, err
	}
	reqBody := map[string]any{
		"deployment_id": deployment.ID,
		"project_id":    project.ID,
		"repo_url":      project.RepoURL,
		"build_command": project.BuildCommand,
		"run_command":   project.RunCommand,
		"project_type":  project.Type,
	}
	payload, err := json.Marshal(reqBody)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.cfg.BuilderURL+"/deploy", bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.client.Do(req)
	if err != nil {
		s.logger.Error("builder request failed", "deployment_id", deployment.ID, "error", err)
		s.updateStatus(ctx, domain.DeploymentStatusUpdate{
			DeploymentID: deployment.ID,
			Status:       StatusFailed,
			Stage:        "builder_request",
			Message:      "failed to contact builder",
			Error:        err.Error(),
			CompletedAt:  toPtr(time.Now().UTC()),
		})
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		s.logger.Error("builder returned error", "deployment_id", deployment.ID, "status", resp.Status)
		s.updateStatus(ctx, domain.DeploymentStatusUpdate{
			DeploymentID: deployment.ID,
			Status:       StatusFailed,
			Stage:        "builder_response",
			Message:      "builder rejected deployment",
			Error:        resp.Status,
			CompletedAt:  toPtr(time.Now().UTC()),
		})
		return nil, errors.New("builder rejected deployment request")
	}
	s.updateStatus(ctx, domain.DeploymentStatusUpdate{
		DeploymentID: deployment.ID,
		Status:       StatusRunning,
		Stage:        "builder_ack",
		Message:      "builder accepted deployment",
	})
	s.logger.Info("deployment queued", "deployment_id", deployment.ID, "project_id", project.ID)
	return deployment, nil
}

// CallbackPayload represents progress events from the builder service.
type CallbackPayload struct {
	DeploymentID string                 `json:"deployment_id"`
	ProjectID    string                 `json:"project_id"`
	Status       string                 `json:"status"`
	Stage        string                 `json:"stage"`
	Message      string                 `json:"message"`
	Image        string                 `json:"image"`
	URL          string                 `json:"url"`
	Error        string                 `json:"error"`
	Timestamp    time.Time              `json:"timestamp"`
	Metadata     map[string]interface{} `json:"metadata"`
}

// ListByProject returns recent deployments for a project.
func (s Service) ListByProject(ctx context.Context, projectID string, limit int) ([]domain.Deployment, error) {
	return s.deployments.ListDeploymentsByProject(ctx, projectID, limit)
}

// ProcessCallback ingests deployment progress notifications from the builder.
func (s Service) ProcessCallback(ctx context.Context, payload CallbackPayload) error {
	if strings.TrimSpace(payload.DeploymentID) == "" {
		return errors.New("deployment_id required")
	}

	status := mapBuilderStatus(payload.Status)
	var completedAt *time.Time
	if status == StatusFailed || status == StatusSuccess {
		t := payload.Timestamp
		if t.IsZero() {
			t = time.Now().UTC()
		}
		completedAt = &t
	}

	metadata := mergeMetadata(payload)
	var metadataBytes []byte
	if len(metadata) > 0 {
		metadataBytes = mustJSON(metadata)
	}

	update := domain.DeploymentStatusUpdate{
		DeploymentID: payload.DeploymentID,
		Status:       status,
		Stage:        payload.Stage,
		Message:      payload.Message,
		URL:          payload.URL,
		Error:        payload.Error,
		Metadata:     metadataBytes,
		CompletedAt:  completedAt,
	}
	if err := s.deployments.UpdateDeploymentStatus(ctx, update); err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return err
		}
		s.logger.Error("deployment status update failed", "deployment_id", payload.DeploymentID, "error", err)
		return err
	}

	logFields := []any{"deployment_id", payload.DeploymentID, "status", payload.Status, "stage", payload.Stage}
	if payload.URL != "" {
		logFields = append(logFields, "url", payload.URL)
	}
	if payload.Error != "" {
		logFields = append(logFields, "error", payload.Error)
	}
	if image, ok := metadata["image"]; ok {
		logFields = append(logFields, "image", image)
	}
	if hostPort, ok := metadata["host_port"]; ok {
		logFields = append(logFields, "host_port", hostPort)
	}
	if hostIP, ok := metadata["host_ip"]; ok {
		logFields = append(logFields, "host_ip", hostIP)
	}
	s.logger.Info("deployment progress", logFields...)
	s.emitDeploymentLog(ctx, payload, metadataBytes)
	s.handleIngress(ctx, payload, metadata, status)
	return nil
}

func mergeMetadata(payload CallbackPayload) map[string]any {
	meta := make(map[string]any)
	for k, v := range payload.Metadata {
		meta[k] = v
	}
	if payload.DeploymentID != "" {
		meta["deployment_id"] = payload.DeploymentID
	}
	if payload.ProjectID != "" {
		meta["project_id"] = payload.ProjectID
	}
	if payload.Stage != "" {
		meta["stage"] = payload.Stage
	}
	if payload.Status != "" {
		meta["status"] = payload.Status
	}
	if payload.URL != "" {
		meta["url"] = payload.URL
	}
	if payload.Error != "" {
		meta["error"] = payload.Error
	}
	if payload.Image != "" {
		meta["image"] = payload.Image
	}
	if !payload.Timestamp.IsZero() {
		meta["timestamp"] = payload.Timestamp.UTC().Format(time.RFC3339Nano)
	}
	return meta
}

func mapBuilderStatus(raw string) string {
	switch strings.ToLower(raw) {
	case "failed":
		return StatusFailed
	case "success", "stopped":
		return StatusSuccess
	case "running", "ready":
		return StatusRunning
	case "queued":
		return StatusPending
	default:
		return StatusRunning
	}
}

func (s Service) updateStatus(ctx context.Context, update domain.DeploymentStatusUpdate) {
	if err := s.deployments.UpdateDeploymentStatus(ctx, update); err != nil {
		s.logger.Warn("update deployment status failed", "deployment_id", update.DeploymentID, "error", err)
	}
}

func (s Service) emitDeploymentLog(ctx context.Context, payload CallbackPayload, metadata []byte) {
	if s.logSvc.Hub() == nil {
		return
	}
	metaBytes := metadata
	if len(metaBytes) == 0 {
		fallback := map[string]any{}
		if payload.Stage != "" {
			fallback["stage"] = payload.Stage
		}
		if payload.Status != "" {
			fallback["status"] = payload.Status
		}
		if payload.URL != "" {
			fallback["url"] = payload.URL
		}
		if payload.Error != "" {
			fallback["error"] = payload.Error
		}
		metaBytes = mustJSON(fallback)
	}
	entry := domain.ProjectLog{
		ProjectID: payload.ProjectID,
		Source:    "builder",
		Level:     mapLogLevel(payload.Status),
		Message:   strings.TrimSpace(payload.Message),
		Metadata:  metaBytes,
		CreatedAt: payload.Timestamp,
	}
	if entry.CreatedAt.IsZero() {
		entry.CreatedAt = time.Now().UTC()
	}
	if entry.Message == "" {
		entry.Message = fmt.Sprintf("deployment %s status: %s", payload.DeploymentID, payload.Status)
	}
	if err := s.logSvc.Append(ctx, entry); err != nil {
		s.logger.Warn("failed to append deployment log", "deployment_id", payload.DeploymentID, "error", err)
	}
}

func mapLogLevel(status string) string {
	switch strings.ToLower(status) {
	case "failed":
		return "error"
	case "queued":
		return "info"
	case "running", "ready":
		return "info"
	default:
		return "info"
	}
}

func mustJSON(v any) []byte {
	data, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	return data
}

func toPtr(t time.Time) *time.Time {
	return &t
}

func (s Service) handleIngress(ctx context.Context, payload CallbackPayload, metadata map[string]any, status string) {
	if s.containers == nil || s.ingress == nil {
		return
	}
	containerID, _ := metadata["container_id"].(string)
	if strings.TrimSpace(containerID) == "" {
		return
	}

	opCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	if payload.Stage == "container_exit" {
		if err := s.containers.DeleteContainer(opCtx, containerID); err != nil {
			s.logger.Warn("failed to remove container metadata", "deployment_id", payload.DeploymentID, "container_id", containerID, "error", err)
		}
		s.syncIngress(opCtx, payload.ProjectID)
		return
	}

	if status == StatusFailed {
		if err := s.containers.DeleteContainer(opCtx, containerID); err != nil {
			s.logger.Warn("failed to remove container metadata", "deployment_id", payload.DeploymentID, "container_id", containerID, "error", err)
		}
		s.syncIngress(opCtx, payload.ProjectID)
		return
	}

	hostPort, ok := parseHostPort(metadata["host_port"])
	if !ok {
		s.logger.Warn("missing host port for ingress update", "deployment_id", payload.DeploymentID, "container_id", containerID)
		return
	}
	hostIP, _ := metadata["host_ip"].(string)

	container := domain.ProjectContainer{
		ProjectID:   payload.ProjectID,
		ContainerID: containerID,
		Status:      status,
		HostIP:      hostIP,
		HostPort:    hostPort,
		UpdatedAt:   time.Now().UTC(),
	}

	if err := s.containers.UpsertContainer(opCtx, container); err != nil {
		s.logger.Warn("failed to upsert container metadata", "deployment_id", payload.DeploymentID, "container_id", containerID, "error", err)
		return
	}
	if err := s.containers.RemoveStaleContainers(opCtx, payload.ProjectID, containerID); err != nil {
		s.logger.Warn("failed to prune stale container metadata", "deployment_id", payload.DeploymentID, "container_id", containerID, "error", err)
	}

	s.syncIngress(opCtx, payload.ProjectID)
}

func (s Service) syncIngress(ctx context.Context, projectID string) {
	if s.ingress == nil {
		return
	}
	project, err := s.projects.GetProjectByID(ctx, projectID)
	if err != nil {
		s.logger.Warn("failed to load project for ingress", "project_id", projectID, "error", err)
		return
	}
	containers, err := s.containers.ListProjectContainers(ctx, projectID)
	if err != nil {
		s.logger.Warn("failed to list project containers", "project_id", projectID, "error", err)
		return
	}
	if err := s.ingress.Apply(ctx, *project, containers); err != nil {
		s.logger.Warn("failed to apply ingress config", "project_id", projectID, "error", err)
	}
}

func parseHostPort(value any) (int, bool) {
	switch v := value.(type) {
	case string:
		if port, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
			return port, port > 0
		}
	case json.Number:
		if port, err := strconv.Atoi(v.String()); err == nil {
			return port, port > 0
		}
	case float64:
		return int(v), int(v) > 0
	case int:
		return v, v > 0
	case int64:
		return int(v), v > 0
	case uint64:
		return int(v), v > 0
	}
	return 0, false
}
