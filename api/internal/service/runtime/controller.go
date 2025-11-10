package runtime

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/splax/localvercel/api/internal/domain"
	"github.com/splax/localvercel/api/internal/repository"
	"github.com/splax/localvercel/api/internal/service/deploy"
	"github.com/splax/localvercel/pkg/config"
)

const (
	defaultInterval     = 30 * time.Second
	reconcileTimeout    = 15 * time.Second
	runtimeTimeoutStage = "runtime_timeout"
)

// IngressApplier updates routing configuration for a project.
type IngressApplier interface {
	Apply(ctx context.Context, project domain.Project, containers []domain.ProjectContainer) error
}

// Controller enforces runtime policies for active deployments.
type Controller struct {
	projects    repository.ProjectRepository
	containers  repository.ContainerRepository
	deployments repository.DeploymentRepository
	ingress     IngressApplier
	logger      *slog.Logger
	builder     *http.Client
	builderURL  string
	builderTok  string

	interval         time.Duration
	containerTTL     time.Duration
	deploymentTTL    time.Duration
	cpuLimit         float64
	memoryLimitBytes int64

	now func() time.Time
}

// New constructs a runtime controller. It returns nil when no runtime guards are enabled.
func New(projects repository.ProjectRepository, containers repository.ContainerRepository, deployments repository.DeploymentRepository, ingressSvc IngressApplier, logger *slog.Logger, cfg config.APIConfig) *Controller {
	if projects == nil || containers == nil || deployments == nil {
		return nil
	}

	interval := cfg.RuntimeReconcileInterval
	if interval <= 0 {
		interval = defaultInterval
	}

	containerTTL := cfg.RuntimeContainerTTL
	deploymentTTL := cfg.RuntimeDeploymentTTL
	cpuLimit := float64(cfg.RuntimeCPULimitPercent)
	memoryLimit := int64(cfg.RuntimeMemoryLimitMB) * 1024 * 1024

	if containerTTL <= 0 && deploymentTTL <= 0 && cpuLimit <= 0 && memoryLimit <= 0 {
		return nil
	}

	var builderClient *http.Client
	builderURL := strings.TrimSpace(cfg.BuilderURL)
	if builderURL != "" {
		builderClient = &http.Client{Timeout: 10 * time.Second}
	}

	ctrl := &Controller{
		projects:         projects,
		containers:       containers,
		deployments:      deployments,
		ingress:          ingressSvc,
		logger:           logger,
		interval:         interval,
		containerTTL:     containerTTL,
		deploymentTTL:    deploymentTTL,
		cpuLimit:         cpuLimit,
		memoryLimitBytes: memoryLimit,
		now:              time.Now,
		builder:          builderClient,
		builderURL:       builderURL,
		builderTok:       strings.TrimSpace(cfg.BuilderAuthToken),
	}

	if ctrl.logger != nil {
		ctrl.logger = ctrl.logger.With("component", "runtime")
	}

	return ctrl
}

// Run executes the reconciliation loop until the context is cancelled.
func (c *Controller) Run(ctx context.Context) {
	if c == nil {
		return
	}
	ticker := time.NewTicker(c.interval)
	defer ticker.Stop()

	c.logger.Info("runtime controller started", "interval", c.interval)
	c.runIteration(ctx)

	for {
		select {
		case <-ctx.Done():
			c.logger.Info("runtime controller stopped")
			return
		case <-ticker.C:
			c.runIteration(ctx)
		}
	}
}

func (c *Controller) runIteration(parent context.Context) {
	if c == nil {
		return
	}
	timeout := reconcileTimeout
	if c.interval > 0 && c.interval < timeout {
		timeout = c.interval
	}
	opCtx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()

	now := c.now()

	containers, err := c.containers.ListContainers(opCtx)
	if err != nil {
		c.logger.Warn("failed to list containers", "error", err)
	}

	touched := make(map[string]struct{})
	for projectID := range c.handleContainers(opCtx, now, containers) {
		touched[projectID] = struct{}{}
	}
	for projectID := range c.handleDeployments(opCtx, now) {
		touched[projectID] = struct{}{}
	}

	if len(touched) == 0 || c.ingress == nil {
		return
	}
	c.refreshIngress(opCtx, touched)
}

func (c *Controller) handleContainers(ctx context.Context, now time.Time, containers []domain.ProjectContainer) map[string]struct{} {
	touched := make(map[string]struct{})
	if len(containers) == 0 {
		return touched
	}
	removed := make(map[string]struct{})
	for _, container := range containers {
		if _, already := removed[container.ContainerID]; already {
			continue
		}
		var heartbeatInfo, ttlInfo string
		if container.LastHeartbeatAt != nil && !container.LastHeartbeatAt.IsZero() {
			heartbeatInfo = container.LastHeartbeatAt.UTC().Format(time.RFC3339Nano)
		}
		if container.TTLExpiresAt != nil && !container.TTLExpiresAt.IsZero() {
			ttlInfo = container.TTLExpiresAt.UTC().Format(time.RFC3339Nano)
		}
		expired := false
		if container.TTLExpiresAt != nil && !container.TTLExpiresAt.IsZero() {
			expired = !container.TTLExpiresAt.After(now)
		} else if c.containerTTL > 0 {
			ref := container.UpdatedAt
			if container.LastHeartbeatAt != nil && !container.LastHeartbeatAt.IsZero() {
				ref = container.LastHeartbeatAt.UTC()
			}
			if ref.IsZero() {
				ref = container.UpdatedAt
			}
			expired = !ref.Add(c.containerTTL).After(now)
		}
		c.logger.Info("runtime container ttl check", "project_id", container.ProjectID, "container_id", container.ContainerID, "last_heartbeat_at", heartbeatInfo, "ttl_expires_at", ttlInfo, "expired", expired)
		if expired {
			if c.removeContainer(ctx, container, "expired", fmt.Sprintf("container exceeded ttl %s", formatDuration(c.containerTTL))) {
				removed[container.ContainerID] = struct{}{}
				touched[container.ProjectID] = struct{}{}
			}
			continue
		}
		if c.cpuLimit > 0 && container.CPUPercent != nil && *container.CPUPercent > c.cpuLimit {
			detail := fmt.Sprintf("cpu=%.2f%% limit=%.0f%%", *container.CPUPercent, c.cpuLimit)
			if c.removeContainer(ctx, container, "cpu_limit_exceeded", detail) {
				removed[container.ContainerID] = struct{}{}
				touched[container.ProjectID] = struct{}{}
			}
			continue
		}
		if c.memoryLimitBytes > 0 && container.MemoryBytes != nil && *container.MemoryBytes > c.memoryLimitBytes {
			detail := fmt.Sprintf("memory=%s limit=%s", formatBytes(*container.MemoryBytes), formatBytes(c.memoryLimitBytes))
			if c.removeContainer(ctx, container, "memory_limit_exceeded", detail) {
				removed[container.ContainerID] = struct{}{}
				touched[container.ProjectID] = struct{}{}
			}
		}
	}
	return touched
}

func (c *Controller) handleDeployments(ctx context.Context, now time.Time) map[string]struct{} {
	touched := make(map[string]struct{})
	if c.deploymentTTL <= 0 {
		return touched
	}
	cutoff := now.Add(-c.deploymentTTL)
	deployments, err := c.deployments.ListDeploymentsWithStatusUpdatedBefore(ctx, deploy.StatusRunning, cutoff)
	if err != nil {
		c.logger.Warn("failed to list stale deployments", "error", err)
		return touched
	}
	for _, dep := range deployments {
		msg := fmt.Sprintf("deployment timed out after %s", formatDuration(c.deploymentTTL))
		c.cancelDeployment(ctx, dep.ID)
		if err := c.containers.DeleteContainersByDeployment(ctx, dep.ID); err != nil {
			c.logger.Warn("failed to remove containers for timed out deployment", "deployment_id", dep.ID, "error", err)
		}
		c.failDeployment(ctx, dep.ProjectID, dep.ID, runtimeTimeoutStage, msg)
		touched[dep.ProjectID] = struct{}{}
		c.logger.Info("deployment marked failed after runtime timeout", "deployment_id", dep.ID, "project_id", dep.ProjectID)
	}
	return touched
}

func (c *Controller) refreshIngress(ctx context.Context, projects map[string]struct{}) {
	for projectID := range projects {
		project, err := c.projects.GetProjectByID(ctx, projectID)
		if err != nil {
			c.logger.Warn("failed to load project during runtime reconcile", "project_id", projectID, "error", err)
			continue
		}
		containers, err := c.containers.ListProjectContainers(ctx, projectID)
		if err != nil {
			c.logger.Warn("failed to load project containers during runtime reconcile", "project_id", projectID, "error", err)
			continue
		}
		if err := c.ingress.Apply(ctx, *project, containers); err != nil {
			c.logger.Warn("failed to apply ingress after runtime reconcile", "project_id", projectID, "error", err)
		}
	}
}

func (c *Controller) removeContainer(ctx context.Context, container domain.ProjectContainer, reason, detail string) bool {
	if err := c.containers.DeleteContainer(ctx, container.ContainerID); err != nil {
		c.logger.Warn("failed to deregister container", "project_id", container.ProjectID, "container_id", container.ContainerID, "reason", reason, "error", err)
		return false
	}
	deploymentID := strings.TrimSpace(container.DeploymentID)
	if deploymentID != "" {
		c.cancelDeployment(ctx, deploymentID)
		if err := c.containers.DeleteContainersByDeployment(ctx, deploymentID); err != nil {
			c.logger.Warn("failed to cleanup deployment containers", "project_id", container.ProjectID, "deployment_id", deploymentID, "error", err)
		}
		stage := "runtime_" + reason
		if detail == "" {
			detail = reason
		}
		c.failDeployment(ctx, container.ProjectID, deploymentID, stage, detail)
	}
	c.logger.Info("container deregistered", "project_id", container.ProjectID, "container_id", container.ContainerID, "reason", reason, "detail", detail)
	return true
}

func (c *Controller) failDeployment(ctx context.Context, projectID, deploymentID, stage, message string) {
	if strings.TrimSpace(deploymentID) == "" {
		return
	}
	completedAt := c.now()
	update := domain.DeploymentStatusUpdate{
		DeploymentID: deploymentID,
		Status:       deploy.StatusFailed,
		Stage:        stage,
		Message:      message,
		Error:        message,
		CompletedAt:  &completedAt,
	}
	if err := c.deployments.UpdateDeploymentStatus(ctx, update); err != nil {
		c.logger.Warn("failed to mark deployment failed", "deployment_id", deploymentID, "project_id", projectID, "stage", stage, "error", err)
	}
}

func (c *Controller) cancelDeployment(ctx context.Context, deploymentID string) {
	if c.builder == nil || strings.TrimSpace(deploymentID) == "" {
		return
	}
	endpoint := c.builderEndpoint("/deploy/" + strings.TrimSpace(deploymentID))
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, endpoint, nil)
	if err != nil {
		c.logger.Warn("failed to build builder cancel request", "deployment_id", deploymentID, "error", err)
		return
	}
	if c.builderTok != "" {
		req.Header.Set("X-Builder-Token", c.builderTok)
	}
	resp, err := c.builder.Do(req)
	if err != nil {
		c.logger.Warn("builder cancel request failed", "deployment_id", deploymentID, "error", err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode >= http.StatusBadRequest {
		c.logger.Warn("builder cancel returned error", "deployment_id", deploymentID, "status", resp.Status)
	}
}

func (c *Controller) builderEndpoint(path string) string {
	base := strings.TrimSpace(c.builderURL)
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

func formatDuration(d time.Duration) string {
	if d <= 0 {
		return "0s"
	}
	if d%time.Second == 0 {
		return fmt.Sprintf("%ds", int(d/time.Second))
	}
	if d%time.Millisecond == 0 {
		return fmt.Sprintf("%dms", int(d/time.Millisecond))
	}
	return d.String()
}

func formatBytes(bytes int64) string {
	if bytes <= 0 {
		return "0B"
	}
	const (
		kb = 1024
		mb = 1024 * kb
		gb = 1024 * mb
	)
	switch {
	case bytes >= gb:
		return fmt.Sprintf("%.2fGB", float64(bytes)/float64(gb))
	case bytes >= mb:
		return fmt.Sprintf("%.2fMB", float64(bytes)/float64(mb))
	case bytes >= kb:
		return fmt.Sprintf("%.2fKB", float64(bytes)/float64(kb))
	default:
		return fmt.Sprintf("%dB", bytes)
	}
}
