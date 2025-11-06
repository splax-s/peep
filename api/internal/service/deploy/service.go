package deploy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
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

var projectSlugExpr = regexp.MustCompile(`[^a-z0-9-]+`)

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

func (s Service) builderEndpoint(path string) string {
	base := strings.TrimSpace(s.cfg.BuilderURL)
	if base == "" {
		if strings.HasPrefix(path, "/") {
			return path
		}
		return "/" + path
	}
	base = strings.TrimSuffix(base, "/")
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return base + path
}

// ErrInvalidProjectID indicates the callback referenced a non-UUID project id.
var ErrInvalidProjectID = errors.New("deploy: invalid project id")

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
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.builderEndpoint("/deploy"), bytes.NewReader(payload))
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

// DeleteDeployment stops a running deployment, removes ingress, and deletes persistence state.
func (s Service) DeleteDeployment(ctx context.Context, deploymentID string) error {
	id := strings.TrimSpace(deploymentID)
	if id == "" {
		return fmt.Errorf("deployment id required")
	}
	if _, err := uuid.Parse(id); err != nil {
		return fmt.Errorf("invalid deployment id: %w", err)
	}
	deployment, err := s.deployments.GetDeploymentByID(ctx, id)
	if err != nil {
		return err
	}
	if err := s.cancelBuilderDeployment(ctx, id); err != nil {
		s.logger.Warn("builder cancellation failed", "deployment_id", id, "error", err)
	}
	projectID := deployment.ProjectID
	if err := s.cleanupContainers(ctx, projectID, id); err != nil {
		s.logger.Warn("container cleanup failed", "project_id", projectID, "deployment_id", id, "error", err)
	}
	if err := s.removeIngress(ctx, projectID); err != nil {
		s.logger.Warn("ingress removal failed", "project_id", projectID, "deployment_id", id, "error", err)
	}
	if err := s.deployments.DeleteDeployment(ctx, id); err != nil {
		return err
	}
	s.logger.Info("deployment deleted", "deployment_id", id, "project_id", projectID)
	return nil
}

func (s Service) cancelBuilderDeployment(ctx context.Context, deploymentID string) error {
	if s.client == nil {
		return errors.New("builder client not configured")
	}
	endpoint := s.builderEndpoint("/deploy/" + deploymentID)
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, endpoint, nil)
	if err != nil {
		return err
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= http.StatusBadRequest {
		return fmt.Errorf("builder cancel returned %s", resp.Status)
	}
	return nil
}

func (s Service) cleanupContainers(ctx context.Context, projectID, deploymentID string) error {
	if s.containers == nil {
		return nil
	}
	if strings.TrimSpace(deploymentID) != "" {
		if err := s.containers.DeleteContainersByDeployment(ctx, deploymentID); err != nil {
			return err
		}
	}
	containers, err := s.containers.ListProjectContainers(ctx, projectID)
	if err != nil {
		return err
	}
	var firstErr error
	for _, container := range containers {
		if strings.TrimSpace(deploymentID) != "" && strings.TrimSpace(container.DeploymentID) == strings.TrimSpace(deploymentID) {
			continue
		}
		if err := s.containers.DeleteContainer(ctx, container.ContainerID); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (s Service) removeIngress(ctx context.Context, projectID string) error {
	if s.ingress == nil {
		return nil
	}
	project, err := s.projects.GetProjectByID(ctx, projectID)
	if err != nil {
		return err
	}
	return s.ingress.Remove(ctx, *project)
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
	projectID := strings.TrimSpace(payload.ProjectID)
	if projectID == "" {
		return ErrInvalidProjectID
	}
	if _, err := uuid.Parse(projectID); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidProjectID, err)
	}
	payload.ProjectID = projectID

	status := mapBuilderStatus(payload.Status)

	if strings.EqualFold(payload.Stage, "metrics") {
		metadata := mergeMetadata(payload)
		s.handleIngress(ctx, payload, metadata, status)
		return nil
	}

	if normalized := s.normalizeDeploymentURL(ctx, payload); normalized != "" {
		if payload.Metadata == nil {
			payload.Metadata = make(map[string]interface{})
		}
		payload.Metadata["public_url"] = normalized
		payload.URL = normalized
	}

	metadata := mergeMetadata(payload)
	var completedAt *time.Time
	if status == StatusFailed || status == StatusSuccess {
		t := payload.Timestamp
		if t.IsZero() {
			t = time.Now().UTC()
		}
		completedAt = &t
	}

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

func (s Service) normalizeDeploymentURL(ctx context.Context, payload CallbackPayload) string {
	raw := strings.TrimSpace(payload.URL)
	if raw == "" && payload.Metadata != nil {
		if metaURL, ok := payload.Metadata["url"].(string); ok {
			raw = strings.TrimSpace(metaURL)
		}
	}
	hostPortPresent := false
	if payload.Metadata != nil {
		if port, ok := parseHostPort(payload.Metadata["host_port"]); ok && port > 0 {
			hostPortPresent = true
		}
	}
	if raw == "" && !hostPortPresent {
		return ""
	}
	project, err := s.projects.GetProjectByID(ctx, payload.ProjectID)
	if err != nil {
		return raw
	}
	suffix := strings.TrimSpace(s.cfg.IngressDomainSuffix)
	if suffix == "" {
		suffix = ".local.peep"
	}
	host := canonicalProjectHost(*project, suffix)
	if host == "" {
		return raw
	}
	parsed := parseURLWithFallback(raw)
	scheme := defaultSchemeForEnv(s.cfg.Environment)
	if parsed != nil && strings.EqualFold(parsed.Scheme, "https") {
		scheme = "https"
	}
	result := &url.URL{Scheme: scheme, Host: hostWithPort(host, 8080)}
	if parsed != nil {
		result.Path = parsed.Path
		result.RawQuery = parsed.RawQuery
		result.Fragment = parsed.Fragment
	}
	return result.String()
}

func canonicalProjectHost(project domain.Project, suffix string) string {
	slug := projectSlug(project)
	if slug == "" {
		return ""
	}
	return slug + suffix
}

func projectSlug(project domain.Project) string {
	base := strings.ToLower(strings.TrimSpace(project.Name))
	if base == "" {
		base = strings.ToLower(strings.TrimSpace(project.ID))
	}
	base = projectSlugExpr.ReplaceAllString(base, "-")
	base = strings.Trim(base, "-")
	if base == "" {
		base = strings.ToLower(strings.TrimSpace(project.ID))
	}
	return base
}

func parseURLWithFallback(raw string) *url.URL {
	if raw == "" {
		return nil
	}
	parsed, err := url.Parse(raw)
	if err == nil && parsed.Host != "" {
		return parsed
	}
	if !strings.Contains(raw, "://") {
		parsed, err = url.Parse("http://" + raw)
		if err == nil {
			parsed.Scheme = ""
			return parsed
		}
	}
	return nil
}

func defaultSchemeForEnv(env string) string {
	if strings.EqualFold(strings.TrimSpace(env), "production") {
		return "https"
	}
	return "http"
}

func hostWithPort(host string, port int) string {
	host = strings.TrimSpace(host)
	if host == "" || port <= 0 {
		return host
	}
	// Avoid duplicating port if already present.
	if strings.Contains(host, ":") {
		parts := strings.Split(host, ":")
		if len(parts) == 2 && parts[1] != "" {
			return host
		}
	}
	return fmt.Sprintf("%s:%d", host, port)
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
	projectID := strings.TrimSpace(payload.ProjectID)
	if projectID == "" {
		s.logger.Warn("missing project id for deployment log", "deployment_id", payload.DeploymentID)
		return
	}
	if _, err := uuid.Parse(projectID); err != nil {
		s.logger.Warn("invalid project id for deployment log", "deployment_id", payload.DeploymentID, "project_id", payload.ProjectID, "error", err)
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
	entry.ProjectID = projectID
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
	projectID := strings.TrimSpace(payload.ProjectID)
	if projectID == "" {
		s.logger.Warn("missing project id for ingress update", "deployment_id", payload.DeploymentID)
		return
	}
	if _, err := uuid.Parse(projectID); err != nil {
		s.logger.Warn("invalid project id for ingress update", "deployment_id", payload.DeploymentID, "project_id", payload.ProjectID, "error", err)
		return
	}

	opCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	stage := strings.ToLower(payload.Stage)
	if stage == "container_exit" {
		if err := s.containers.DeleteContainer(opCtx, containerID); err != nil {
			s.logger.Warn("failed to remove container metadata", "deployment_id", payload.DeploymentID, "container_id", containerID, "error", err)
		}
		s.syncIngress(opCtx, projectID)
		return
	}

	if status == StatusFailed {
		if err := s.containers.DeleteContainer(opCtx, containerID); err != nil {
			s.logger.Warn("failed to remove container metadata", "deployment_id", payload.DeploymentID, "container_id", containerID, "error", err)
		}
		s.syncIngress(opCtx, projectID)
		return
	}

	hostPort, ok := parseHostPort(metadata["host_port"])
	if !ok && stage != "metrics" {
		s.logger.Warn("missing host port for ingress update", "deployment_id", payload.DeploymentID, "container_id", containerID)
		return
	}
	hostIP, _ := metadata["host_ip"].(string)
	hostIP = strings.TrimSpace(hostIP)

	container := domain.ProjectContainer{
		ProjectID:    projectID,
		DeploymentID: payload.DeploymentID,
		ContainerID:  containerID,
		Status:       status,
		UpdatedAt:    time.Now().UTC(),
	}
	if hostIP != "" {
		container.HostIP = hostIP
	}
	if ok && hostPort > 0 {
		container.HostPort = hostPort
	}
	if cpu, ok := parseFloat(metadata["cpu_percent"]); ok {
		container.CPUPercent = &cpu
	}
	if mem, ok := parseInt64(metadata["memory_bytes"]); ok {
		container.MemoryBytes = &mem
	}
	if uptime, ok := parseInt64(metadata["uptime_seconds"]); ok {
		container.UptimeSeconds = &uptime
	}

	if err := s.containers.UpsertContainer(opCtx, container); err != nil {
		s.logger.Warn("failed to upsert container metadata", "deployment_id", payload.DeploymentID, "container_id", containerID, "error", err)
		return
	}
	if stage == "metrics" {
		return
	}
	if err := s.containers.RemoveStaleContainers(opCtx, projectID, containerID); err != nil {
		s.logger.Warn("failed to prune stale container metadata", "deployment_id", payload.DeploymentID, "container_id", containerID, "error", err)
	}

	s.syncIngress(opCtx, projectID)
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

func parseFloat(value any) (float64, bool) {
	switch v := value.(type) {
	case float64:
		return v, true
	case float32:
		return float64(v), true
	case int:
		return float64(v), true
	case int64:
		return float64(v), true
	case json.Number:
		if f, err := v.Float64(); err == nil {
			return f, true
		}
	case string:
		s := strings.TrimSpace(v)
		if s == "" {
			return 0, false
		}
		if f, err := strconv.ParseFloat(s, 64); err == nil {
			return f, true
		}
	}
	return 0, false
}

func parseInt64(value any) (int64, bool) {
	switch v := value.(type) {
	case int64:
		return v, true
	case int:
		return int64(v), true
	case float64:
		return int64(v), true
	case float32:
		return int64(v), true
	case json.Number:
		if i, err := v.Int64(); err == nil {
			return i, true
		}
		if f, err := v.Float64(); err == nil {
			return int64(f), true
		}
	case string:
		s := strings.TrimSpace(v)
		if s == "" {
			return 0, false
		}
		if i, err := strconv.ParseInt(s, 10, 64); err == nil {
			return i, true
		}
	}
	return 0, false
}
