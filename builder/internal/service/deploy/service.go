package deploy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"log/slog"

	"github.com/docker/go-connections/nat"
	"github.com/google/uuid"

	"github.com/splax/localvercel/builder/internal/docker"
	"github.com/splax/localvercel/builder/internal/git"
	"github.com/splax/localvercel/builder/internal/workspace"
	"github.com/splax/localvercel/pkg/config"
)

const defaultAppPort = nat.Port("3000/tcp")

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
	s.notifyStatus(req, "queued", "queued", "deployment queued", imageTag, "", map[string]any{"deployment_id": req.DeploymentID}, nil)

	go s.execute(context.Background(), req, imageTag)

	return Result{
		DeploymentID: req.DeploymentID,
		Status:       "queued",
		Image:        imageTag,
		Timestamp:    time.Now().UTC(),
	}, nil
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

	s.notifyStatus(req, "building", "workspace", "preparing workspace", imageTag, "", map[string]any{"deployment_id": req.DeploymentID}, nil)
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

	s.notifyStatus(req, "building", "clone", "cloning repository", imageTag, "", nil, nil)
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

	if strings.TrimSpace(req.BuildCommand) != "" {
		s.notifyStatus(req, "building", "build", "running build command", imageTag, "", nil, nil)
		output, err := runCommand(ctx, req.BuildCommand, workdir, s.logger.With("deployment_id", req.DeploymentID, "stage", "build"))
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

	s.notifyStatus(req, "building", "docker_build", "building container image", imageTag, "", nil, nil)
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

	s.notifyStatus(req, "starting", "container", "starting container", imageTag, "", nil, nil)
	s.emitLog(req, "info", "starting container", map[string]any{"image": imageTag})

	info, err := s.docker.RunContainer(ctx, req.DeploymentID, imageTag, cmd, nil, ports)
	if err != nil {
		s.fail(req, imageTag, "container_start", err)
		return
	}

	url := s.resolveAccessURL(info)
	s.logger.Info("deployment completed", "deployment_id", req.DeploymentID, "image", imageTag, "url", url)
	readyMeta := map[string]any{
		"container_id": info.ID,
		"image":        imageTag,
	}
	if info.PortBinding != nil {
		if bindings := info.PortBinding[defaultAppPort]; len(bindings) > 0 {
			readyMeta["host_port"] = bindings[0].HostPort
			readyMeta["host_ip"] = bindings[0].HostIP
		}
	}
	if len(buildTail) > 0 {
		readyMeta["build_log_tail"] = buildTail
	}
	s.notifyStatus(req, "running", "ready", "deployment is running", imageTag, url, readyMeta, nil)
	s.emitLog(req, "info", "deployment is running", map[string]any{"url": url, "container_id": info.ID})
	if strings.TrimSpace(info.ID) != "" {
		go s.watchContainer(req, info.ID, imageTag)
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
	s.notifyStatus(req, "failed", stage, err.Error(), imageTag, "", nil, err)
	s.emitLog(req, "error", fmt.Sprintf("%s failed: %v", stage, err), map[string]any{
		"stage": stage,
		"error": err.Error(),
	})
}

func (s Service) watchContainer(req Request, containerID, image string) {
	ctx := context.Background()
	exitCode, err := s.docker.WaitForStop(ctx, containerID)
	if err != nil {
		s.logger.Warn("container wait failed", "deployment_id", req.DeploymentID, "container_id", containerID, "error", err)
		s.notifyStatus(req, "failed", "container_exit", "failed to monitor container", image, "", map[string]any{
			"container_id": containerID,
		}, err)
		return
	}
	metadata := map[string]any{
		"deployment_id": req.DeploymentID,
		"project_id":    req.ProjectID,
		"container_id":  containerID,
		"exit_code":     exitCode,
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
	s.notifyStatus(req, status, "container_exit", message, image, "", metadata, notifyErr)
	s.emitLog(req, logLevel, "container exited", map[string]any{
		"container_id": containerID,
		"exit_code":    exitCode,
	})
	if err := s.docker.RemoveContainer(ctx, containerID); err != nil {
		s.logger.Warn("post-exit container cleanup failed", "deployment_id", req.DeploymentID, "container_id", containerID, "error", err)
	}
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

func (s Service) notifyStatus(req Request, status, stage, message, image, url string, metadata map[string]any, err error) {
	if s.statusClient == nil || s.cfg.DeployCallbackURL == "" {
		return
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
		return
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
		return
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := s.statusClient.Do(httpReq)
	if err != nil {
		s.logger.Warn("callback request failed", "deployment_id", req.DeploymentID, "error", err)
		return
	}
	defer resp.Body.Close()
	if _, copyErr := io.Copy(io.Discard, resp.Body); copyErr != nil {
		s.logger.Debug("discard callback response failed", "deployment_id", req.DeploymentID, "error", copyErr)
	}
	if resp.StatusCode >= http.StatusMultipleChoices {
		s.logger.Warn("callback response status", "deployment_id", req.DeploymentID, "status_code", resp.StatusCode)
	}
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
		return
	}
	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := s.logClient.Do(httpReq)
	if err != nil {
		s.logger.Warn("log callback failed", "deployment_id", req.DeploymentID, "error", err)
		return
	}
	defer resp.Body.Close()
	if _, copyErr := io.Copy(io.Discard, resp.Body); copyErr != nil {
		s.logger.Debug("discard log callback response failed", "deployment_id", req.DeploymentID, "error", copyErr)
	}
	if resp.StatusCode >= http.StatusMultipleChoices {
		s.logger.Warn("log callback response status", "deployment_id", req.DeploymentID, "status_code", resp.StatusCode)
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
