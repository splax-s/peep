package httpx

import (
	"bufio"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"log/slog"

	"github.com/gorilla/websocket"
	"github.com/splax/localvercel/api/internal/domain"
	"github.com/splax/localvercel/api/internal/repository"
	"github.com/splax/localvercel/api/internal/service/auth"
	"github.com/splax/localvercel/api/internal/service/deploy"
	"github.com/splax/localvercel/api/internal/service/logs"
	"github.com/splax/localvercel/api/internal/service/project"
	"github.com/splax/localvercel/api/internal/service/team"
	"github.com/splax/localvercel/api/internal/service/webhook"
	"github.com/splax/localvercel/api/internal/ws"
)

// Router wires HTTP endpoints to services.
type Router struct {
	mux          *http.ServeMux
	logger       *slog.Logger
	auth         auth.Service
	team         team.Service
	project      project.Service
	deploy       deploy.Service
	logs         logs.Service
	webhook      webhook.Service
	upgrader     websocket.Upgrader
	limiter      RateLimiter
	builderToken string
	dbHealth     func(context.Context) error
}

const (
	rateWindowDefault        = time.Minute
	rateWindowRealtime       = 30 * time.Second
	rateLimitSignup          = 5
	rateLimitLogin           = 12
	rateLimitUserWrite       = 60
	rateLimitUserRead        = 120
	rateLimitWebsocket       = 30
	rateLimitBuilderCallback = 60
	rateLimitBuilderWrite    = 600
	healthCheckTimeout       = 2 * time.Second
)

// NewRouter assembles routes with dependencies.
func NewRouter(logger *slog.Logger, authSvc auth.Service, teamSvc team.Service, projectSvc project.Service, deploySvc deploy.Service, logSvc logs.Service, webhookSvc webhook.Service, limiter RateLimiter, builderToken string, dbHealth func(context.Context) error) *Router {
	r := &Router{
		mux:     http.NewServeMux(),
		logger:  logger,
		auth:    authSvc,
		team:    teamSvc,
		project: projectSvc,
		deploy:  deploySvc,
		logs:    logSvc,
		webhook: webhookSvc,
		upgrader: websocket.Upgrader{
			CheckOrigin: func(r *http.Request) bool { return true },
		},
		limiter:      limiter,
		builderToken: strings.TrimSpace(builderToken),
		dbHealth:     dbHealth,
	}
	if r.limiter == nil {
		r.limiter = NewMemoryRateLimiter()
	}
	r.register()
	return r
}

// ServeHTTP delegates to underlying mux.
func (r *Router) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	r.mux.ServeHTTP(w, req)
}

// Close releases background resources.
func (r *Router) Close() {
	if r.limiter != nil {
		r.limiter.Close()
	}
}

func (r *Router) register() {
	r.mux.HandleFunc("/healthz", r.audit(r.handleHealthz))
	r.mux.HandleFunc("/auth/signup", r.audit(r.withRateLimit(rateLimitSignup, rateWindowDefault, rateLimitKeyIP, r.handleSignup)))
	r.mux.HandleFunc("/auth/login", r.audit(r.withRateLimit(rateLimitLogin, rateWindowDefault, rateLimitKeyIP, r.handleLogin)))
	r.mux.HandleFunc("/teams", r.audit(r.handlerAuthRate(rateLimitUserWrite, rateWindowDefault, r.handleTeams)))
	r.mux.HandleFunc("/projects", r.audit(r.handlerAuthRate(rateLimitUserWrite, rateWindowDefault, r.handleProjects)))
	r.mux.HandleFunc("/projects/", r.audit(r.handlerAuthRate(rateLimitUserWrite, rateWindowDefault, r.handleProjectSubroutes)))
	r.mux.HandleFunc("/deploy/", r.audit(r.handlerAuthRate(rateLimitUserRead, rateWindowDefault, r.handleDeploy)))
	r.mux.HandleFunc("/logs/", r.audit(r.handleLogs))
	r.mux.HandleFunc("/ws/logs", r.audit(r.handlerAuthRate(rateLimitWebsocket, rateWindowRealtime, r.handleLogsWS)))
	r.mux.HandleFunc("/webhook/", r.audit(r.handleWebhook))
	r.mux.HandleFunc("/builder/callback", r.audit(r.withRateLimit(rateLimitBuilderCallback, rateWindowDefault, rateLimitKeyIP, r.handleBuilderCallback)))
}

func (r *Router) handleSignup(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		r.methodNotAllowed(w)
		return
	}
	var payload struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	user, tokens, err := r.auth.Signup(req.Context(), payload.Email, payload.Password)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"user": map[string]any{
			"id":    user.ID,
			"email": user.Email,
		},
		"tokens": tokens,
	})
}

func (r *Router) handleLogin(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		r.methodNotAllowed(w)
		return
	}
	var payload struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	user, tokens, err := r.auth.Login(req.Context(), payload.Email, payload.Password)
	if err != nil {
		writeError(w, http.StatusUnauthorized, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"user": map[string]any{
			"id":    user.ID,
			"email": user.Email,
		},
		"tokens": tokens,
	})
}

func (r *Router) handleTeams(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		r.methodNotAllowed(w)
		return
	}
	var payload struct {
		Name   string      `json:"name"`
		Limits team.Limits `json:"limits"`
	}
	if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	info, ok := authInfoFromContext(req.Context())
	if !ok {
		r.logger.Error("auth context missing for team creation", "path", req.URL.Path)
		writeError(w, http.StatusInternalServerError, "authorization context missing")
		return
	}
	team, err := r.team.Create(req.Context(), info.UserID, payload.Name, payload.Limits)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, team)
}

func (r *Router) handleProjects(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		r.methodNotAllowed(w)
		return
	}
	var payload project.CreateInput
	if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if _, ok := authInfoFromContext(req.Context()); !ok {
		r.logger.Error("auth context missing for project creation", "path", req.URL.Path)
		writeError(w, http.StatusInternalServerError, "authorization context missing")
		return
	}
	proj, err := r.project.Create(req.Context(), payload)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, proj)
}

func (r *Router) handleProjectSubroutes(w http.ResponseWriter, req *http.Request) {
	trimmed := strings.TrimPrefix(req.URL.Path, "/projects/")
	parts := strings.Split(trimmed, "/")
	if len(parts) < 1 {
		r.notFound(w)
		return
	}
	projectID := parts[0]
	if projectID == "" {
		r.notFound(w)
		return
	}
	if len(parts) == 2 && parts[1] == "env" {
		r.handleProjectEnv(w, req, projectID)
		return
	}
	r.notFound(w)
}

func (r *Router) handleProjectEnv(w http.ResponseWriter, req *http.Request, projectID string) {
	if req.Method != http.MethodPost {
		r.methodNotAllowed(w)
		return
	}
	var payload project.EnvVarInput
	if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if _, ok := authInfoFromContext(req.Context()); !ok {
		r.logger.Error("auth context missing for env var mutation", "path", req.URL.Path)
		writeError(w, http.StatusInternalServerError, "authorization context missing")
		return
	}
	payload.ProjectID = projectID
	if err := r.project.AddEnvVar(req.Context(), payload); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"status": "stored"})
}

func (r *Router) handleDeploy(w http.ResponseWriter, req *http.Request) {
	projectID := strings.TrimPrefix(req.URL.Path, "/deploy/")
	if projectID == "" {
		r.notFound(w)
		return
	}
	if _, ok := authInfoFromContext(req.Context()); !ok {
		r.logger.Error("auth context missing for deploy route", "path", req.URL.Path)
		writeError(w, http.StatusInternalServerError, "authorization context missing")
		return
	}
	switch req.Method {
	case http.MethodPost:
		var payload struct {
			Commit string `json:"commit"`
		}
		_ = json.NewDecoder(req.Body).Decode(&payload)
		deployment, err := r.deploy.Trigger(req.Context(), projectID, payload.Commit)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusAccepted, deployment)
	case http.MethodGet:
		limit, _ := strconv.Atoi(req.URL.Query().Get("limit"))
		deployments, err := r.deploy.ListByProject(req.Context(), projectID, limit)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, deployments)
	default:
		r.methodNotAllowed(w)
	}
}

func (r *Router) handleBuilderCallback(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		r.methodNotAllowed(w)
		return
	}
	if !r.verifyBuilderToken(w, req) {
		return
	}
	var payload deploy.CallbackPayload
	if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if err := r.deploy.ProcessCallback(req.Context(), payload); err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, repository.ErrNotFound) {
			status = http.StatusNotFound
		}
		writeError(w, status, err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "received"})
}

func (r *Router) handleLogs(w http.ResponseWriter, req *http.Request) {
	projectID := strings.TrimPrefix(req.URL.Path, "/logs/")
	if projectID == "" {
		r.notFound(w)
		return
	}
	switch req.Method {
	case http.MethodGet:
		ctx, _, ok := r.ensureAuth(w, req)
		if !ok {
			return
		}
		if setter, ok := w.(contextSetter); ok {
			setter.SetContext(ctx)
		}
		req = req.WithContext(ctx)
		key := r.rateLimitKeyUser(req)
		if key == "" {
			key = rateLimitKeyIP(req)
		}
		decision := r.limiter.Allow(key, rateLimitUserRead, rateWindowDefault)
		r.applyRateHeaders(w, rateLimitUserRead, decision)
		if !decision.allowed {
			writeError(w, http.StatusTooManyRequests, "rate limit exceeded")
			return
		}
		limit, _ := strconv.Atoi(req.URL.Query().Get("limit"))
		if limit <= 0 {
			limit = 100
		}
		offset, _ := strconv.Atoi(req.URL.Query().Get("offset"))
		if offset < 0 {
			offset = 0
		}
		entries, err := r.logs.List(req.Context(), projectID, limit, offset)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, entries)
	case http.MethodPost:
		if !r.verifyBuilderToken(w, req) {
			return
		}
		builderKey := "builder:" + projectID
		decision := r.limiter.Allow(builderKey, rateLimitBuilderWrite, rateWindowDefault)
		r.applyRateHeaders(w, rateLimitBuilderWrite, decision)
		if !decision.allowed {
			writeError(w, http.StatusTooManyRequests, "rate limit exceeded")
			return
		}
		var payload struct {
			Source    string          `json:"source"`
			Level     string          `json:"level"`
			Message   string          `json:"message"`
			Stage     string          `json:"stage"`
			Metadata  json.RawMessage `json:"metadata"`
			Timestamp string          `json:"timestamp"`
		}
		if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON body")
			return
		}
		payload.Message = strings.TrimSpace(payload.Message)
		if payload.Message == "" {
			writeError(w, http.StatusBadRequest, "message is required")
			return
		}
		source := strings.TrimSpace(payload.Source)
		if source == "" {
			source = "builder"
		}
		level := strings.TrimSpace(payload.Level)
		if level == "" {
			level = "info"
		}
		timestamp := time.Now().UTC()
		if payload.Timestamp != "" {
			parsed, err := time.Parse(time.RFC3339Nano, payload.Timestamp)
			if err != nil {
				writeError(w, http.StatusBadRequest, "invalid timestamp format")
				return
			}
			timestamp = parsed.UTC()
		}
		entry := domain.ProjectLog{
			ProjectID: projectID,
			Source:    source,
			Level:     level,
			Message:   payload.Message,
			Metadata:  payload.Metadata,
			CreatedAt: timestamp,
		}
		if payload.Stage != "" {
			entry.Message = fmt.Sprintf("[%s] %s", payload.Stage, entry.Message)
		}
		if err := r.logs.Append(req.Context(), entry); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusAccepted, map[string]string{"status": "queued"})
	default:
		r.methodNotAllowed(w)
	}
}

func (r *Router) handleLogsWS(w http.ResponseWriter, req *http.Request) {
	if _, ok := authInfoFromContext(req.Context()); !ok {
		r.logger.Error("auth context missing for logs websocket", "path", req.URL.Path)
		writeError(w, http.StatusInternalServerError, "authorization context missing")
		return
	}
	projectID := req.URL.Query().Get("project_id")
	if projectID == "" {
		writeError(w, http.StatusBadRequest, "project_id query parameter required")
		return
	}
	conn, err := r.upgrader.Upgrade(w, req, nil)
	if err != nil {
		r.logger.Error("websocket upgrade failed", "error", err)
		return
	}
	client := ws.NewClient(conn, r.logger)
	r.logs.Hub().Register(projectID, client)
	go func() {
		defer func() {
			r.logs.Hub().Unregister(projectID, client)
			client.Close()
		}()
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				break
			}
		}
	}()
}

func (r *Router) handleWebhook(w http.ResponseWriter, req *http.Request) {
	trimmed := strings.TrimPrefix(req.URL.Path, "/webhook/")
	if trimmed == "" {
		r.notFound(w)
		return
	}
	parts := strings.Split(trimmed, "/")
	projectID := parts[0]
	if projectID == "" {
		r.notFound(w)
		return
	}
	if len(parts) == 2 && parts[1] == "secret" {
		r.handlerAuthRate(rateLimitUserWrite, rateWindowDefault, func(w http.ResponseWriter, req *http.Request) {
			r.handleWebhookSecret(w, req, projectID)
		})(w, req)
		return
	}
	if len(parts) > 1 {
		r.notFound(w)
		return
	}
	if req.Method != http.MethodPost {
		r.methodNotAllowed(w)
		return
	}
	body, err := io.ReadAll(req.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "could not read body")
		return
	}
	signature := req.Header.Get("X-Webhook-Signature")
	if err := r.webhook.CheckSignature(req.Context(), projectID, body, signature); err != nil {
		writeError(w, http.StatusUnauthorized, err.Error())
		return
	}
	var event struct {
		After string `json:"after"`
	}
	_ = json.Unmarshal(body, &event)
	if _, err := r.deploy.Trigger(req.Context(), projectID, event.After); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "queued"})
}

func (r *Router) handleWebhookSecret(w http.ResponseWriter, req *http.Request, projectID string) {
	if req.Method != http.MethodPost {
		r.methodNotAllowed(w)
		return
	}
	if _, ok := authInfoFromContext(req.Context()); !ok {
		r.logger.Error("auth context missing for webhook secret", "path", req.URL.Path)
		writeError(w, http.StatusInternalServerError, "authorization context missing")
		return
	}
	var payload struct {
		Secret string `json:"secret"`
	}
	if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	payload.Secret = strings.TrimSpace(payload.Secret)
	if payload.Secret == "" {
		writeError(w, http.StatusBadRequest, "secret is required")
		return
	}
	if err := r.webhook.UpsertSecret(req.Context(), projectID, payload.Secret); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"status": "stored"})
}

func (r *Router) handleHealthz(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodGet {
		r.methodNotAllowed(w)
		return
	}
	components := make(map[string]any)
	status := "ok"
	if r.dbHealth != nil {
		ctx, cancel := context.WithTimeout(req.Context(), healthCheckTimeout)
		defer cancel()
		if err := r.dbHealth(ctx); err != nil {
			status = "degraded"
			components["database"] = map[string]any{
				"status": "down",
				"error":  err.Error(),
			}
		} else {
			components["database"] = map[string]any{"status": "up"}
		}
	}
	payload := map[string]any{
		"status":     status,
		"components": components,
		"timestamp":  time.Now().UTC().Format(time.RFC3339Nano),
	}
	code := http.StatusOK
	if status != "ok" {
		code = http.StatusServiceUnavailable
	}
	writeJSON(w, code, payload)
}

func (r *Router) audit(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		recorder := &statusRecorder{ResponseWriter: w}
		start := time.Now()
		next(recorder, req)

		status := recorder.status
		if status == 0 {
			status = http.StatusOK
		}
		ctx := recorder.ctx
		if ctx == nil {
			ctx = req.Context()
		}
		duration := time.Since(start)
		actor := "anonymous"
		fields := []any{
			"method", req.Method,
			"path", req.URL.Path,
			"status", status,
			"bytes", recorder.bytes,
			"duration_ms", duration.Milliseconds(),
		}
		if ip := clientIP(req); ip != "" {
			fields = append(fields, "ip", ip)
		}
		if reqID := strings.TrimSpace(req.Header.Get("X-Request-ID")); reqID != "" {
			fields = append(fields, "request_id", reqID)
		}
		if info, ok := authInfoFromContext(ctx); ok {
			actor = "user"
			fields = append(fields, "user_id", info.UserID)
			if info.TeamID != "" {
				fields = append(fields, "team_id", info.TeamID)
			}
		} else if strings.HasPrefix(req.URL.Path, "/builder/") {
			actor = "builder"
		}
		fields = append(fields, "actor", actor)

		switch {
		case status >= http.StatusInternalServerError:
			r.logger.Error("http_request", fields...)
		case status >= http.StatusBadRequest:
			r.logger.Warn("http_request", fields...)
		default:
			r.logger.Info("http_request", fields...)
		}
	}
}

type statusRecorder struct {
	http.ResponseWriter
	status int
	bytes  int
	ctx    context.Context
}

func (sr *statusRecorder) WriteHeader(code int) {
	sr.status = code
	sr.ResponseWriter.WriteHeader(code)
}

func (sr *statusRecorder) Write(b []byte) (int, error) {
	if sr.status == 0 {
		sr.status = http.StatusOK
	}
	n, err := sr.ResponseWriter.Write(b)
	sr.bytes += n
	return n, err
}

func (sr *statusRecorder) SetContext(ctx context.Context) {
	sr.ctx = ctx
}

func (sr *statusRecorder) Flush() {
	if f, ok := sr.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (sr *statusRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if h, ok := sr.ResponseWriter.(http.Hijacker); ok {
		return h.Hijack()
	}
	return nil, nil, errors.New("hijacker not supported")
}

func (sr *statusRecorder) Push(target string, opts *http.PushOptions) error {
	if p, ok := sr.ResponseWriter.(http.Pusher); ok {
		return p.Push(target, opts)
	}
	return http.ErrNotSupported
}

func clientIP(req *http.Request) string {
	if forwarded := strings.TrimSpace(req.Header.Get("X-Forwarded-For")); forwarded != "" {
		parts := strings.Split(forwarded, ",")
		if len(parts) > 0 {
			ip := strings.TrimSpace(parts[0])
			if ip != "" {
				return ip
			}
		}
	}
	host, _, err := net.SplitHostPort(strings.TrimSpace(req.RemoteAddr))
	if err != nil {
		return strings.TrimSpace(req.RemoteAddr)
	}
	return host
}

func (r *Router) applyRateHeaders(w http.ResponseWriter, limit int, decision rateDecision) {
	if limit <= 0 {
		return
	}
	remaining := limit - decision.count
	if remaining < 0 {
		remaining = 0
	}
	headers := w.Header()
	headers.Set("X-RateLimit-Limit", strconv.Itoa(limit))
	headers.Set("X-RateLimit-Remaining", strconv.Itoa(remaining))
	if !decision.windowEnd.IsZero() {
		headers.Set("X-RateLimit-Reset", strconv.FormatInt(decision.windowEnd.Unix(), 10))
	}
}

// verifyBuilderToken ensures builder callbacks include configured secret.
func (r *Router) verifyBuilderToken(w http.ResponseWriter, req *http.Request) bool {
	expected := r.builderToken
	if expected == "" {
		r.logger.Error("builder token not configured", "path", req.URL.Path)
		writeError(w, http.StatusInternalServerError, "builder authentication misconfigured")
		return false
	}
	token := strings.TrimSpace(req.Header.Get("X-Builder-Token"))
	if token == "" {
		token = strings.TrimSpace(req.URL.Query().Get("builder_token"))
	}
	if len(token) != len(expected) || subtle.ConstantTimeCompare([]byte(token), []byte(expected)) != 1 {
		r.logger.Warn("builder token mismatch", "path", req.URL.Path)
		writeError(w, http.StatusUnauthorized, "invalid builder token")
		return false
	}
	return true
}

func (r *Router) methodNotAllowed(w http.ResponseWriter) {
	writeError(w, http.StatusMethodNotAllowed, "method not allowed")
}

func (r *Router) notFound(w http.ResponseWriter) {
	writeError(w, http.StatusNotFound, "not found")
}
