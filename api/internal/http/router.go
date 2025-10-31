package httpx

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
	mux      *http.ServeMux
	logger   *slog.Logger
	auth     auth.Service
	team     team.Service
	project  project.Service
	deploy   deploy.Service
	logs     logs.Service
	webhook  webhook.Service
	upgrader websocket.Upgrader
}

// NewRouter assembles routes with dependencies.
func NewRouter(logger *slog.Logger, authSvc auth.Service, teamSvc team.Service, projectSvc project.Service, deploySvc deploy.Service, logSvc logs.Service, webhookSvc webhook.Service) *Router {
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
	}
	r.register()
	return r
}

// ServeHTTP delegates to underlying mux.
func (r *Router) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	r.mux.ServeHTTP(w, req)
}

func (r *Router) register() {
	r.mux.HandleFunc("/auth/signup", r.handleSignup)
	r.mux.HandleFunc("/auth/login", r.handleLogin)
	r.mux.HandleFunc("/teams", r.handleTeams)
	r.mux.HandleFunc("/projects", r.handleProjects)
	r.mux.HandleFunc("/projects/", r.handleProjectSubroutes)
	r.mux.HandleFunc("/deploy/", r.handleDeploy)
	r.mux.HandleFunc("/logs/", r.handleLogs)
	r.mux.HandleFunc("/ws/logs", r.handleLogsWS)
	r.mux.HandleFunc("/webhook/", r.handleWebhook)
	r.mux.HandleFunc("/builder/callback", r.handleBuilderCallback)
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
		OwnerID string      `json:"owner_id"`
		Name    string      `json:"name"`
		Limits  team.Limits `json:"limits"`
	}
	if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	team, err := r.team.Create(req.Context(), payload.OwnerID, payload.Name, payload.Limits)
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
		r.handleWebhookSecret(w, req, projectID)
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

func (r *Router) methodNotAllowed(w http.ResponseWriter) {
	writeError(w, http.StatusMethodNotAllowed, "method not allowed")
}

func (r *Router) notFound(w http.ResponseWriter) {
	writeError(w, http.StatusNotFound, "not found")
}
