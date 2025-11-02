package deploy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"log/slog"

	"github.com/docker/go-connections/nat"
	"github.com/google/uuid"

	"github.com/splax/localvercel/builder/internal/docker"
	"github.com/splax/localvercel/builder/internal/git"
	"github.com/splax/localvercel/builder/internal/workspace"
	"github.com/splax/localvercel/pkg/config"
)

const (
	defaultAppPort      = nat.Port("3000/tcp")
	defaultHostFallback = "host.docker.internal"
)

// Request contains deployment parameters from the API.
type Request struct {
	DeploymentID string `json:"deployment_id"`
	ProjectID    string `json:"project_id"`
	RepoURL      string `json:"repo_url"`
	BuildCommand string `json:"build_command"`
	RunCommand   string `json:"run_command"`
	ProjectType  string `json:"project_type"`
}

// Result summarizes deployment outcome.
type Result struct {
	DeploymentID string    `json:"deployment_id"`
	Status       string    `json:"status"`
	Image        string    `json:"image"`
	Timestamp    time.Time `json:"timestamp"`
}

// Service coordinates build and run operations using Docker.
type Service struct {
	docker          *docker.Client
	workspace       *workspace.Manager
	logger          *slog.Logger
	cfg             config.BuilderConfig
	statusClient    *http.Client
	logClient       *http.Client
	callbackTimeout time.Duration
	logSuppressed   *sync.Map
}

type suppressionEntry struct {
	expires time.Time
}

func (s Service) shouldSuppress(projectKey string) bool {
	projectKey = strings.TrimSpace(projectKey)
	if projectKey == "" || s.logSuppressed == nil {
		return false
	}
	value, ok := s.logSuppressed.Load(projectKey)
	if !ok {
		return false
	}
	entry, ok := value.(suppressionEntry)
	if !ok {
		s.logSuppressed.Delete(projectKey)
		return false
	}
	if entry.expires.IsZero() {
		return true
	}
	if time.Now().Before(entry.expires) {
		return true
	}
	s.logSuppressed.Delete(projectKey)
	return false
}

func (s Service) suppress(projectKey string) {
	projectKey = strings.TrimSpace(projectKey)
	if projectKey == "" || s.logSuppressed == nil {
		return
	}
	entry := suppressionEntry{}
	ttl := s.cfg.CallbackSuppressionTTL
	if ttl > 0 {
		entry.expires = time.Now().Add(ttl)
	}
	s.logSuppressed.Store(projectKey, entry)
}

func (s Service) clearSuppression(projectKey string) {
	projectKey = strings.TrimSpace(projectKey)
	if projectKey == "" || s.logSuppressed == nil {
		return
	}
	s.logSuppressed.Delete(projectKey)
}

func (s Service) attachBuilderToken(req *http.Request) {
	if req == nil {
		return
	}
	token := strings.TrimSpace(s.cfg.BuilderAuthToken)
	if token == "" {
		return
	}
	req.Header.Set("X-Builder-Token", token)
}

// New creates a deployment service.
func New(cli *docker.Client, ws *workspace.Manager, logger *slog.Logger, cfg config.BuilderConfig) Service {
	timeout := cfg.DeployCallbackTimeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	var statusClient *http.Client
	if cfg.DeployCallbackURL != "" {
		statusClient = &http.Client{Timeout: timeout}
	}
	var logClient *http.Client
	if cfg.LogCallbackURL != "" {
		logClient = &http.Client{Timeout: timeout}
	}
	return Service{
		docker:          cli,
		workspace:       ws,
		logger:          logger,
		cfg:             cfg,
		statusClient:    statusClient,
		logClient:       logClient,
		callbackTimeout: timeout,
		logSuppressed:   &sync.Map{},
	}
}

// Handle executes the deployment workflow.
func (s Service) Handle(ctx context.Context, req Request) (Result, error) {
	if req.DeploymentID == "" {
		req.DeploymentID = uuid.NewString()
	}
	if err := s.validateRequest(req); err != nil {
		return Result{}, err
	}
	s.clearSuppression(req.ProjectID)
	if err := s.docker.Ping(ctx); err != nil {
		return Result{}, err
	}
	if s.workspace == nil {
		return Result{}, fmt.Errorf("workspace manager not initialised")
	}
	s.logger.Info("deployment received", "deployment_id", req.DeploymentID, "project_id", req.ProjectID, "repo_url", req.RepoURL)
	payload, _ := json.Marshal(req)
	s.logger.Debug("deployment request payload", "deployment_id", req.DeploymentID, "payload", string(payload))

	imageTag := s.imageTag(req)
	_ = s.notifyStatus(req, "queued", "queued", "deployment queued", imageTag, "", map[string]any{"deployment_id": req.DeploymentID}, nil)

	go s.execute(context.Background(), req, imageTag)

	return Result{
		DeploymentID: req.DeploymentID,
		Status:       "queued",
		Image:        imageTag,
		Timestamp:    time.Now().UTC(),
	}, nil
}

// Health verifies builder dependencies are reachable.
func (s Service) Health(ctx context.Context) error {
	if s.docker == nil {
		return errors.New("docker client not initialised")
	}
	return s.docker.Ping(ctx)
}

// Cancel stops a running deployment and cleans up related workspace state.
func (s Service) Cancel(ctx context.Context, deploymentID string) error {
	id := strings.TrimSpace(deploymentID)
	if id == "" {
		return fmt.Errorf("deployment id required")
	}
	if s.docker == nil {
		return fmt.Errorf("docker client not initialised")
	}
	if err := s.docker.RemoveContainer(ctx, id); err != nil {
		return err
	}
	if s.workspace != nil {
		if err := s.workspace.CleanupByID(id); err != nil {
			if s.logger != nil {
				s.logger.Warn("workspace cleanup failed", "deployment_id", id, "error", err)
			}
			return err
		}
	}
	return nil
}

func (s Service) validateRequest(req Request) error {
	if strings.TrimSpace(req.RepoURL) == "" {
		return fmt.Errorf("repository url required")
	}
	if strings.TrimSpace(req.ProjectID) == "" {
		return fmt.Errorf("project id required")
	}
	return nil
}

func (s Service) execute(rootCtx context.Context, req Request, imageTag string) {
	ctx, cancel := context.WithTimeout(rootCtx, s.cfg.BuildTimeout)
	defer cancel()

	_ = s.notifyStatus(req, "building", "workspace", "preparing workspace", imageTag, "", map[string]any{"deployment_id": req.DeploymentID}, nil)
	s.emitLog(req, "info", "preparing workspace", map[string]any{"deployment_id": req.DeploymentID})

	workdir, err := s.workspace.Prepare(req.DeploymentID)
	if err != nil {
		s.fail(req, imageTag, "workspace", err)
		return
	}
	defer func() {
		if err := s.workspace.Cleanup(workdir); err != nil {
			s.logger.Error("workspace cleanup failed", "deployment_id", req.DeploymentID, "error", err)
		}
	}()

	_ = s.notifyStatus(req, "building", "clone", "cloning repository", imageTag, "", nil, nil)
	gitCtx, cancelGit := context.WithTimeout(ctx, s.cfg.GitTimeout)
	defer cancelGit()
	if err := git.Clone(gitCtx, req.RepoURL, workdir); err != nil {
		s.fail(req, imageTag, "clone", err)
		return
	}
	s.emitLog(req, "info", "repository cloned", map[string]any{"repo_url": req.RepoURL})

	if err := ensureBuildContext(workdir); err != nil {
		s.emitLog(req, "error", "invalid build context", map[string]any{"error": err.Error()})
		s.fail(req, imageTag, "build_context", err)
		return
	}

	runtimePrep, err := s.prepareRuntime(req, workdir)
	if err != nil {
		s.fail(req, imageTag, "runtime_prepare", err)
		return
	}
	if runtimePrep.Name != "" {
		meta := map[string]any{"runtime": runtimePrep.Name}
		if runtimePrep.DockerfileGenerated {
			meta["dockerfile_generated"] = true
		}
		if runtimePrep.BuildScriptEmbedded {
			meta["build_script_embedded"] = true
		}
		if runtimePrep.PackageManager != "" {
			meta["package_manager"] = runtimePrep.PackageManager
		}
		if runtimePrep.BuildTool != "" {
			meta["build_tool"] = runtimePrep.BuildTool
		}
		s.emitLog(req, "info", "runtime prepared", meta)
	}

	buildCommand := strings.TrimSpace(req.BuildCommand)
	if runtimePrep.SkipHostBuild {
		buildCommand = ""
	}

	if buildCommand != "" {
		_ = s.notifyStatus(req, "building", "build", "running build command", imageTag, "", nil, nil)
		output, err := runCommand(ctx, buildCommand, workdir, s.logger.With("deployment_id", req.DeploymentID, "stage", "build"))
		if err != nil {
			s.fail(req, imageTag, "build", err)
			if output != "" {
				s.emitLog(req, "error", "build command failed", map[string]any{"output": truncateForMetadata(output)})
			}
			return
		}
		if output != "" {
			s.emitLog(req, "info", "build command output", map[string]any{"output": truncateForMetadata(output)})
		}
	}

	if err := ensureDockerfile(workdir); err != nil {
		s.emitLog(req, "error", "missing dockerfile", map[string]any{"error": err.Error()})
		s.fail(req, imageTag, "docker_build", err)
		return
	}

	_ = s.notifyStatus(req, "building", "docker_build", "building container image", imageTag, "", nil, nil)
	aggregator := newBuildLogAggregator(func(msg string) {
		s.logger.Debug("docker build output", "deployment_id", req.DeploymentID, "line", msg)
		s.emitLog(req, "info", "docker build output", map[string]any{
			"stage": "docker_build",
			"line":  msg,
		})
	})
	var buildTail []string
	buildLog := func(line string) {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			return
		}
		aggregator.Add(truncateForMetadata(trimmed))
	}
	if err := s.docker.BuildImage(ctx, workdir, imageTag, nil, buildLog); err != nil {
		aggregator.Flush()
		if tail := aggregator.Snapshot(40); len(tail) > 0 {
			buildTail = tail
			s.emitLog(req, "error", "docker build tail", map[string]any{
				"stage": "docker_build",
				"lines": tail,
			})
		}
		s.fail(req, imageTag, "docker_build", err)
		return
	}
	aggregator.Flush()
	if tail := aggregator.Snapshot(40); len(tail) > 0 {
		buildTail = tail
		s.emitLog(req, "info", "docker build tail", map[string]any{
			"stage": "docker_build",
			"lines": tail,
		})
	}
	s.emitLog(req, "info", "docker image built", map[string]any{"image": imageTag})

	if err := s.docker.RemoveContainer(ctx, req.DeploymentID); err != nil {
		s.logger.Warn("remove existing container failed", "deployment_id", req.DeploymentID, "error", err)
	}

	cmd, err := parseCommand(req.RunCommand)
	if err != nil {
		s.fail(req, imageTag, "run_command", err)
		return
	}

	ports := nat.PortMap{
		defaultAppPort: []nat.PortBinding{{HostIP: "127.0.0.1", HostPort: ""}},
	}

	_ = s.notifyStatus(req, "starting", "container", "starting container", imageTag, "", nil, nil)
	s.emitLog(req, "info", "starting container", map[string]any{"image": imageTag})

	info, err := s.docker.RunContainer(ctx, req.DeploymentID, imageTag, cmd, nil, ports)
	if err != nil {
		s.fail(req, imageTag, "container_start", err)
		return
	}
	startedAt := time.Now().UTC()

	url := s.resolveAccessURL(info)
	s.logger.Info("deployment completed", "deployment_id", req.DeploymentID, "image", imageTag, "url", url)
	readyMeta := map[string]any{
		"container_id": info.ID,
		"image":        imageTag,
	}
	hostIP := ""
	hostPort := ""
	if info.PortBinding != nil {
		if bindings := info.PortBinding[defaultAppPort]; len(bindings) > 0 {
			hostPort = strings.TrimSpace(bindings[0].HostPort)
			hostIP = strings.TrimSpace(bindings[0].HostIP)
			if hostIP == "" || hostIP == "0.0.0.0" || hostIP == "127.0.0.1" {
				hostIP = defaultHostFallback
			}
			readyMeta["host_port"] = hostPort
			readyMeta["host_ip"] = hostIP
		}
	}
	if len(buildTail) > 0 {
		readyMeta["build_log_tail"] = buildTail
	}
	_ = s.notifyStatus(req, "running", "ready", "deployment is running", imageTag, url, readyMeta, nil)
	s.emitLog(req, "info", "deployment is running", map[string]any{"url": url, "container_id": info.ID})
	if strings.TrimSpace(info.ID) != "" {
		go s.watchContainer(req, info.ID, imageTag, hostIP, hostPort, startedAt)
	}
}

func (s Service) imageTag(req Request) string {
	registry := strings.TrimSuffix(s.cfg.Registry, "/")
	if registry == "" {
		registry = "local" // deterministic fallback
	}
	return filepath.ToSlash(fmt.Sprintf("%s/%s:%s", registry, req.ProjectID, req.DeploymentID))
}

func runCommand(ctx context.Context, command, dir string, log *slog.Logger) (string, error) {
	args, err := parseCommand(command)
	if err != nil {
		return "", err
	}
	if len(args) == 0 {
		return "", nil
	}
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	cmd.Dir = dir
	cmd.Env = os.Environ()
	output, err := cmd.CombinedOutput()
	if len(output) > 0 && log != nil {
		log.Debug("command output", "output", string(output))
	}
	if err != nil {
		return string(output), fmt.Errorf("command %s failed: %w", command, err)
	}
	return string(output), nil
}

func ensureDockerfile(dir string) error {
	candidates := []string{"Dockerfile", "dockerfile"}
	for _, name := range candidates {
		path := filepath.Join(dir, name)
		info, err := os.Stat(path)
		if err == nil && !info.IsDir() {
			return nil
		}
		if err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("check dockerfile: %w", err)
		}
	}
	return fmt.Errorf("dockerfile not found in repository root (expected Dockerfile)")
}

func ensureBuildContext(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("read build context: %w", err)
	}
	files := 0
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		files++
	}
	if files == 0 {
		return fmt.Errorf("build context is empty")
	}
	return nil
}

func truncateForMetadata(s string) string {
	s = strings.TrimSpace(s)
	const limit = 4096
	if len(s) <= limit {
		return s
	}
	return s[:limit] + "..." + fmt.Sprintf(" (%d bytes truncated)", len(s)-limit)
}

func parseCommand(command string) ([]string, error) {
	command = strings.TrimSpace(command)
	if command == "" {
		return nil, nil
	}
	var (
		tokens   []string
		current  strings.Builder
		inSingle bool
		inDouble bool
		escape   bool
	)

	for _, r := range command {
		switch {
		case escape:
			current.WriteRune(r)
			escape = false
		case r == '\\':
			escape = true
		case r == '\'':
			if !inDouble {
				inSingle = !inSingle
				continue
			}
			current.WriteRune(r)
		case r == '"':
			if !inSingle {
				inDouble = !inDouble
				continue
			}
			current.WriteRune(r)
		case (r == ' ' || r == '\t' || r == '\n' || r == '\r') && !inSingle && !inDouble:
			if current.Len() > 0 {
				tokens = append(tokens, current.String())
				current.Reset()
			}
		default:
			current.WriteRune(r)
		}
	}

	if escape || inSingle || inDouble {
		return nil, fmt.Errorf("unterminated quoted string in command: %s", command)
	}
	if current.Len() > 0 {
		tokens = append(tokens, current.String())
	}

	return tokens, nil
}

func (s Service) fail(req Request, imageTag, stage string, err error) {
	s.logger.Error("deployment stage failed", "deployment_id", req.DeploymentID, "stage", stage, "error", err)
	_ = s.notifyStatus(req, "failed", stage, err.Error(), imageTag, "", nil, err)
	s.emitLog(req, "error", fmt.Sprintf("%s failed: %v", stage, err), map[string]any{
		"stage": stage,
		"error": err.Error(),
	})
}

func (s Service) watchContainer(req Request, containerID, image, hostIP, hostPort string, startedAt time.Time) {
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	if s.shouldSampleMetrics() {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.sampleContainerMetrics(ctx, req, containerID, image, hostIP, hostPort, startedAt)
		}()
	}

	exitCode, err := s.docker.WaitForStop(ctx, containerID)
	cancel()
	wg.Wait()
	if err != nil {
		s.logger.Warn("container wait failed", "deployment_id", req.DeploymentID, "container_id", containerID, "error", err)
		_ = s.notifyStatus(req, "failed", "container_exit", "failed to monitor container", image, "", map[string]any{
			"container_id": containerID,
		}, err)
		return
	}
	uptimeSeconds := int64(0)
	if !startedAt.IsZero() {
		uptimeSeconds = int64(time.Since(startedAt).Seconds())
		if uptimeSeconds < 0 {
			uptimeSeconds = 0
		}
	}
	metadata := map[string]any{
		"deployment_id":  req.DeploymentID,
		"project_id":     req.ProjectID,
		"container_id":   containerID,
		"exit_code":      exitCode,
		"uptime_seconds": uptimeSeconds,
	}
	if hostIP != "" {
		metadata["host_ip"] = hostIP
	}
	if hostPort != "" {
		metadata["host_port"] = hostPort
	}
	message := fmt.Sprintf("container exited with status %d", exitCode)
	status := "stopped"
	logLevel := "info"
	var notifyErr error
	if exitCode != 0 {
		status = "failed"
		logLevel = "error"
		notifyErr = fmt.Errorf("container exited with status %d", exitCode)
	}
	_ = s.notifyStatus(req, status, "container_exit", message, image, "", metadata, notifyErr)
	s.emitLog(req, logLevel, "container exited", map[string]any{
		"container_id": containerID,
		"exit_code":    exitCode,
	})
	if err := s.docker.RemoveContainer(ctx, containerID); err != nil {
		s.logger.Warn("post-exit container cleanup failed", "deployment_id", req.DeploymentID, "container_id", containerID, "error", err)
	}
}

func (s Service) shouldSampleMetrics() bool {
	return s.statusClient != nil && s.cfg.DeployCallbackURL != "" && s.cfg.MetricsSampleEvery > 0
}

func (s Service) sampleContainerMetrics(ctx context.Context, req Request, containerID, image, hostIP, hostPort string, startedAt time.Time) {
	interval := s.cfg.MetricsSampleEvery
	if interval <= 0 {
		return
	}

	if !s.sendMetricsSample(ctx, req, containerID, image, hostIP, hostPort, startedAt) {
		return
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if !s.sendMetricsSample(ctx, req, containerID, image, hostIP, hostPort, startedAt) {
				return
			}
		}
	}
}

func (s Service) sendMetricsSample(ctx context.Context, req Request, containerID, image, hostIP, hostPort string, startedAt time.Time) bool {
	sampleCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	metrics, err := s.docker.ContainerMetrics(sampleCtx, containerID)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, docker.ErrNotFound) {
			return false
		}
		s.logger.Warn("container metrics sample failed", "deployment_id", req.DeploymentID, "container_id", containerID, "error", err)
		return true
	}

	cpuPercent := math.Round(metrics.CPUPercent*100) / 100
	uptimeSeconds := int64(0)
	if !startedAt.IsZero() {
		uptimeSeconds = int64(time.Since(startedAt).Seconds())
		if uptimeSeconds < 0 {
			uptimeSeconds = 0
		}
	}

	memBytes := int64(0)
	if metrics.MemoryUsage > 0 {
		if metrics.MemoryUsage > math.MaxInt64 {
			memBytes = math.MaxInt64
		} else {
			memBytes = int64(metrics.MemoryUsage)
		}
	}

	metadata := map[string]any{
		"deployment_id":  req.DeploymentID,
		"project_id":     req.ProjectID,
		"container_id":   containerID,
		"cpu_percent":    cpuPercent,
		"memory_bytes":   memBytes,
		"uptime_seconds": uptimeSeconds,
	}
	if metrics.MemoryLimit > 0 {
		if metrics.MemoryLimit > math.MaxInt64 {
			metadata["memory_limit_bytes"] = int64(math.MaxInt64)
		} else {
			metadata["memory_limit_bytes"] = int64(metrics.MemoryLimit)
		}
	}
	if hostIP != "" {
		metadata["host_ip"] = hostIP
	}
	if hostPort != "" {
		metadata["host_port"] = hostPort
	}

	if !s.notifyStatus(req, "running", "metrics", "container metrics sample", image, "", metadata, nil) {
		return false
	}
	return true
}

type statusPayload struct {
	DeploymentID string                 `json:"deployment_id"`
	ProjectID    string                 `json:"project_id"`
	Status       string                 `json:"status"`
	Stage        string                 `json:"stage,omitempty"`
	Message      string                 `json:"message,omitempty"`
	Image        string                 `json:"image,omitempty"`
	URL          string                 `json:"url,omitempty"`
	Error        string                 `json:"error,omitempty"`
	Timestamp    time.Time              `json:"timestamp"`
	Metadata     map[string]interface{} `json:"metadata,omitempty"`
}

func (s Service) notifyStatus(req Request, status, stage, message, image, url string, metadata map[string]any, err error) bool {
	if s.statusClient == nil || s.cfg.DeployCallbackURL == "" {
		return true
	}
	projectKey := strings.TrimSpace(req.ProjectID)
	if s.shouldSuppress(projectKey) {
		return false
	}
	payload := statusPayload{
		DeploymentID: req.DeploymentID,
		ProjectID:    req.ProjectID,
		Status:       status,
		Stage:        stage,
		Message:      message,
		Image:        image,
		URL:          url,
		Timestamp:    time.Now().UTC(),
		Metadata:     metadata,
	}
	if err != nil {
		payload.Error = err.Error()
	}

	body, marshalErr := json.Marshal(payload)
	if marshalErr != nil {
		s.logger.Error("marshal callback payload failed", "deployment_id", req.DeploymentID, "error", marshalErr)
		return false
	}

	ctx := context.Background()
	var cancel context.CancelFunc
	if s.callbackTimeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, s.callbackTimeout)
	}
	if cancel != nil {
		defer cancel()
	}

	reqBody := bytes.NewReader(body)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, s.cfg.DeployCallbackURL, reqBody)
	if err != nil {
		s.logger.Warn("create callback request failed", "deployment_id", req.DeploymentID, "error", err)
		return false
	}
	httpReq.Header.Set("Content-Type", "application/json")
	s.attachBuilderToken(httpReq)

	resp, err := s.statusClient.Do(httpReq)
	if err != nil {
		s.logger.Warn("callback request failed", "deployment_id", req.DeploymentID, "error", err)
		return false
	}
	defer resp.Body.Close()
	if _, copyErr := io.Copy(io.Discard, resp.Body); copyErr != nil {
		s.logger.Debug("discard callback response failed", "deployment_id", req.DeploymentID, "error", copyErr)
	}
	if resp.StatusCode >= http.StatusMultipleChoices {
		s.logger.Warn("callback response status", "deployment_id", req.DeploymentID, "status_code", resp.StatusCode)
		if resp.StatusCode >= http.StatusBadRequest && resp.StatusCode < http.StatusInternalServerError {
			s.suppress(projectKey)
			return false
		}
	}
	return true
}

func (s Service) resolveAccessURL(info docker.ContainerInfo) string {
	if info.PortBinding == nil {
		return ""
	}
	bindings := info.PortBinding[defaultAppPort]
	if len(bindings) == 0 {
		return ""
	}
	binding := bindings[0]
	host := binding.HostIP
	if host == "" || host == "0.0.0.0" {
		host = "127.0.0.1"
	}
	if binding.HostPort == "" {
		return ""
	}
	return fmt.Sprintf("http://%s:%s", host, binding.HostPort)
}

func (s Service) emitLog(req Request, level, message string, metadata map[string]any) {
	if s.logClient == nil || s.cfg.LogCallbackURL == "" {
		return
	}
	projectKey := strings.TrimSpace(req.ProjectID)
	if s.shouldSuppress(projectKey) {
		return
	}
	payload := map[string]any{
		"source":    "builder",
		"level":     level,
		"message":   message,
		"metadata":  metadata,
		"timestamp": time.Now().UTC().Format(time.RFC3339Nano),
	}
	body, err := json.Marshal(payload)
	if err != nil {
		s.logger.Warn("marshal log payload failed", "deployment_id", req.DeploymentID, "error", err)
		return
	}
	endpoint := strings.TrimRight(s.cfg.LogCallbackURL, "/") + "/" + req.ProjectID
	ctx := context.Background()
	var cancel context.CancelFunc
	if s.callbackTimeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, s.callbackTimeout)
	}
	if cancel != nil {
		defer cancel()
	}
	reqBody := bytes.NewReader(body)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, reqBody)
	if err != nil {
		s.logger.Warn("create log callback request failed", "deployment_id", req.DeploymentID, "error", err)
		s.suppress(projectKey)
		return
	}
	httpReq.Header.Set("Content-Type", "application/json")
	s.attachBuilderToken(httpReq)
	resp, err := s.logClient.Do(httpReq)
	if err != nil {
		s.logger.Warn("log callback failed", "deployment_id", req.DeploymentID, "error", err)
		s.suppress(projectKey)
		return
	}
	defer resp.Body.Close()
	if _, copyErr := io.Copy(io.Discard, resp.Body); copyErr != nil {
		s.logger.Debug("discard log callback response failed", "deployment_id", req.DeploymentID, "error", copyErr)
	}
	if resp.StatusCode >= http.StatusMultipleChoices {
		s.logger.Warn("log callback response status", "deployment_id", req.DeploymentID, "status_code", resp.StatusCode)
		if resp.StatusCode >= http.StatusBadRequest && resp.StatusCode < http.StatusInternalServerError {
			s.suppress(projectKey)
		}
	}
}

const (
	buildLogRepeatFlushInterval = 5 * time.Second
	buildLogBufferSize          = 100
)

type buildLogAggregator struct {
	emit     func(string)
	last     string
	repeats  int
	lastEmit time.Time
	maxDelay time.Duration
	buffer   []string
	bufSize  int
}

func newBuildLogAggregator(emit func(string)) *buildLogAggregator {
	return &buildLogAggregator{
		emit:     emit,
		maxDelay: buildLogRepeatFlushInterval,
		bufSize:  buildLogBufferSize,
	}
}

func (a *buildLogAggregator) Add(line string) {
	if a == nil || line == "" {
		return
	}
	now := time.Now()
	if a.last == "" {
		a.last = line
		a.repeats = 0
		a.emitLine(line, now)
		return
	}
	if line == a.last {
		a.repeats++
		if a.maxDelay > 0 && now.Sub(a.lastEmit) >= a.maxDelay {
			a.flushRepeatsAt(now)
		}
		return
	}
	a.flushRepeatsAt(now)
	a.last = line
	a.repeats = 0
	a.emitLine(line, now)
}

func (a *buildLogAggregator) Flush() {
	if a == nil {
		return
	}
	a.flushRepeatsAt(time.Now())
}

func (a *buildLogAggregator) flushRepeatsAt(now time.Time) {
	if a.repeats == 0 || a.last == "" {
		return
	}
	msg := fmt.Sprintf("%s (repeated %d more times)", a.last, a.repeats)
	a.repeats = 0
	a.emitLine(msg, now)
}

func (a *buildLogAggregator) emitSafe(line string) {
	if a.emit == nil || line == "" {
		return
	}
	a.emit(line)
}

func (a *buildLogAggregator) emitLine(line string, now time.Time) {
	a.emitSafe(line)
	a.record(line)
	a.lastEmit = now
}

func (a *buildLogAggregator) record(line string) {
	if a.bufSize <= 0 || line == "" {
		return
	}
	if len(a.buffer) < a.bufSize {
		a.buffer = append(a.buffer, line)
		return
	}
	a.buffer = append(a.buffer[1:], line)
}

func (a *buildLogAggregator) Snapshot(limit int) []string {
	if a == nil || len(a.buffer) == 0 {
		return nil
	}
	if limit <= 0 || limit >= len(a.buffer) {
		return append([]string(nil), a.buffer...)
	}
	return append([]string(nil), a.buffer[len(a.buffer)-limit:]...)
}
