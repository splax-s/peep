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
	"sync"
	"time"

	"log/slog"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/splax/localvercel/api/internal/domain"
	"github.com/splax/localvercel/api/internal/repository"
	"github.com/splax/localvercel/api/internal/service/auth"
	"github.com/splax/localvercel/api/internal/service/deploy"
	"github.com/splax/localvercel/api/internal/service/environment"
	"github.com/splax/localvercel/api/internal/service/logs"
	"github.com/splax/localvercel/api/internal/service/project"
	runtimeSvc "github.com/splax/localvercel/api/internal/service/runtime"
	"github.com/splax/localvercel/api/internal/service/team"
	"github.com/splax/localvercel/api/internal/service/webhook"
	"github.com/splax/localvercel/api/internal/ws"
)

// Router wires HTTP endpoints to services.
type Router struct {
	mux                *http.ServeMux
	logger             *slog.Logger
	auth               auth.Service
	team               team.Service
	project            project.Service
	environment        environment.Service
	deploy             deploy.Service
	logs               logs.Service
	runtime            *runtimeSvc.TelemetryService
	webhook            webhook.Service
	upgrader           websocket.Upgrader
	limiter            RateLimiter
	builderToken       string
	dbHealth           func(context.Context) error
	metricsOnce        sync.Once
	metricsInitialized bool
	requestTotal       *prometheus.CounterVec
	requestLatency     *prometheus.HistogramVec
	rateLimitHits      *prometheus.CounterVec
}

const (
	rateWindowDefault          = time.Minute
	rateWindowRealtime         = 30 * time.Second
	rateLimitSignup            = 300
	rateLimitLogin             = 900
	rateLimitUserWrite         = 6000
	rateLimitUserRead          = 12000
	rateLimitWebsocket         = 2400
	rateLimitBuilderCallback   = 4000
	rateLimitBuilderWrite      = 20000
	rateLimitRuntimeWrite      = 64000
	rateLimitRuntimeRead       = 9600
	runtimeStreamHeartbeat     = 20 * time.Second
	runtimeStreamWatchdog      = 2 * runtimeStreamHeartbeat
	runtimeStreamBackfillLimit = 100
	healthCheckTimeout         = 2 * time.Second
	logStreamHeartbeat         = 25 * time.Second
	logStreamWatchdog          = 2 * logStreamHeartbeat
	logStreamBackfillLimit     = 50
)

// NewRouter assembles routes with dependencies.
func NewRouter(logger *slog.Logger, authSvc auth.Service, teamSvc team.Service, projectSvc project.Service, environmentSvc environment.Service, deploySvc deploy.Service, logSvc logs.Service, runtimeTelemetry *runtimeSvc.TelemetryService, webhookSvc webhook.Service, limiter RateLimiter, builderToken string, dbHealth func(context.Context) error) *Router {
	r := &Router{
		mux:         http.NewServeMux(),
		logger:      logger,
		auth:        authSvc,
		team:        teamSvc,
		project:     projectSvc,
		environment: environmentSvc,
		deploy:      deploySvc,
		logs:        logSvc,
		runtime:     runtimeTelemetry,
		webhook:     webhookSvc,
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
	r.initMetrics()
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
	r.mux.Handle("/metrics", promhttp.Handler())
	r.mux.HandleFunc("/healthz", r.audit("/healthz", r.handleHealthz))
	r.mux.HandleFunc("/auth/signup", r.audit("/auth/signup", r.withRateLimit("/auth/signup", rateLimitSignup, rateWindowDefault, rateLimitKeyIP, r.handleSignup)))
	r.mux.HandleFunc("/auth/login", r.audit("/auth/login", r.withRateLimit("/auth/login", rateLimitLogin, rateWindowDefault, rateLimitKeyIP, r.handleLogin)))
	r.mux.HandleFunc("/teams", r.audit("/teams", r.handlerAuthRate("/teams", rateLimitUserWrite, rateWindowDefault, r.handleTeams)))
	r.mux.HandleFunc("/projects", r.audit("/projects", r.handlerAuthRate("/projects", rateLimitUserWrite, rateWindowDefault, r.handleProjects)))
	r.mux.HandleFunc("/projects/", r.audit("/projects/:id", r.handlerAuthRate("/projects/:id", rateLimitUserWrite, rateWindowDefault, r.handleProjectSubroutes)))
	r.mux.HandleFunc("/deploy/", r.audit("/deploy/:id", r.handlerAuthRate("/deploy/:id", rateLimitUserRead, rateWindowDefault, r.handleDeploy)))
	r.mux.HandleFunc("/deployments/", r.audit("/deployments/:id", r.handlerAuthRate("/deployments/:id", rateLimitUserWrite, rateWindowDefault, r.handleDeploymentDelete)))
	r.mux.HandleFunc("/logs/stream", r.audit("/logs/stream", r.handlerAuthRate("/logs/stream", rateLimitUserRead, rateWindowRealtime, r.handleLogsStream)))
	r.mux.HandleFunc("/logs/", r.audit("/logs/:project_id", r.handleLogs))
	r.mux.HandleFunc("/ws/logs", r.audit("/ws/logs", r.handlerAuthRate("/ws/logs", rateLimitWebsocket, rateWindowRealtime, r.handleLogsWS)))
	r.mux.HandleFunc("/webhook/", r.audit("/webhook", r.handleWebhook))
	r.mux.HandleFunc("/builder/callback", r.audit("/builder/callback", r.withRateLimit("/builder/callback", rateLimitBuilderCallback, rateWindowDefault, rateLimitKeyIP, r.handleBuilderCallback)))
	if r.runtime != nil {
		r.mux.HandleFunc("/runtime/events", r.audit("/runtime/events", r.handleRuntimeEvents))
		r.mux.HandleFunc("/runtime/metrics", r.audit("/runtime/metrics", r.handlerAuthRate("/runtime/metrics", rateLimitRuntimeRead, rateWindowRealtime, r.handleRuntimeMetrics)))
		r.mux.HandleFunc("/runtime/stream", r.audit("/runtime/stream", r.handlerAuthRate("/runtime/stream", rateLimitRuntimeRead, rateWindowRealtime, r.handleRuntimeStream)))
	}
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
	switch req.Method {
	case http.MethodPost:
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
	case http.MethodGet:
		info, ok := authInfoFromContext(req.Context())
		if !ok {
			r.logger.Error("auth context missing for team list", "path", req.URL.Path)
			writeError(w, http.StatusInternalServerError, "authorization context missing")
			return
		}
		teams, err := r.team.ListByUser(req.Context(), info.UserID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, teams)
	default:
		r.methodNotAllowed(w)
	}
}

func (r *Router) handleProjects(w http.ResponseWriter, req *http.Request) {
	switch req.Method {
	case http.MethodPost:
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
	case http.MethodGet:
		if _, ok := authInfoFromContext(req.Context()); !ok {
			r.logger.Error("auth context missing for project list", "path", req.URL.Path)
			writeError(w, http.StatusInternalServerError, "authorization context missing")
			return
		}
		teamID := strings.TrimSpace(req.URL.Query().Get("team_id"))
		if teamID == "" {
			writeError(w, http.StatusBadRequest, "team_id query parameter required")
			return
		}
		projects, err := r.project.ListByTeam(req.Context(), teamID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, projects)
	default:
		r.methodNotAllowed(w)
	}
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
	if len(parts) == 1 || parts[1] == "" {
		r.handleProjectResource(w, req, projectID)
		return
	}
	if parts[1] == "environments" {
		r.handleProjectEnvironments(w, req, projectID, parts[2:])
		return
	}
	r.notFound(w)
}

func (r *Router) handleProjectResource(w http.ResponseWriter, req *http.Request, projectID string) {
	switch req.Method {
	case http.MethodGet:
		if _, ok := authInfoFromContext(req.Context()); !ok {
			r.logger.Error("auth context missing for project detail", "path", req.URL.Path)
			writeError(w, http.StatusInternalServerError, "authorization context missing")
			return
		}
		proj, err := r.project.Get(req.Context(), projectID)
		if err != nil {
			if errors.Is(err, repository.ErrNotFound) {
				writeError(w, http.StatusNotFound, err.Error())
				return
			}
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, proj)
	default:
		r.methodNotAllowed(w)
	}
}

func (r *Router) handleProjectEnvironments(w http.ResponseWriter, req *http.Request, projectID string, segments []string) {
	if _, ok := authInfoFromContext(req.Context()); !ok {
		r.logger.Error("auth context missing for environment route", "path", req.URL.Path)
		writeError(w, http.StatusInternalServerError, "authorization context missing")
		return
	}
	if len(segments) == 0 || segments[0] == "" {
		r.handleEnvironmentsCollection(w, req, projectID)
		return
	}
	if segments[0] == "audits" {
		r.handleEnvironmentAudits(w, req, projectID)
		return
	}
	environmentID := strings.TrimSpace(segments[0])
	if environmentID == "" {
		r.notFound(w)
		return
	}
	if len(segments) == 1 || segments[1] == "" {
		r.handleEnvironmentResource(w, req, projectID, environmentID)
		return
	}
	if segments[1] == "versions" {
		r.handleEnvironmentVersions(w, req, projectID, environmentID, segments[2:])
		return
	}
	r.notFound(w)
}

func (r *Router) handleEnvironmentsCollection(w http.ResponseWriter, req *http.Request, projectID string) {
	switch req.Method {
	case http.MethodGet:
		environments, err := r.environment.List(req.Context(), projectID)
		if err != nil {
			r.respondEnvironmentError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, environments)
	case http.MethodPost:
		var payload struct {
			Name            string  `json:"name"`
			Slug            string  `json:"slug"`
			Type            string  `json:"type"`
			Protected       *bool   `json:"protected"`
			Position        *int    `json:"position"`
			ActorIDOverride *string `json:"actor_id"`
		}
		decoder := json.NewDecoder(req.Body)
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&payload); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON body")
			return
		}
		info, _ := authInfoFromContext(req.Context())
		var actorID *string
		if payload.ActorIDOverride != nil && strings.TrimSpace(*payload.ActorIDOverride) != "" {
			override := strings.TrimSpace(*payload.ActorIDOverride)
			actorID = &override
		} else if strings.TrimSpace(info.UserID) != "" {
			user := strings.TrimSpace(info.UserID)
			actorID = &user
		}
		protected := false
		if payload.Protected != nil {
			protected = *payload.Protected
		}
		position := 0
		if payload.Position != nil {
			position = *payload.Position
		}
		input := environment.CreateEnvironmentInput{
			ProjectID:       projectID,
			Name:            payload.Name,
			Slug:            payload.Slug,
			EnvironmentType: payload.Type,
			Protected:       protected,
			Position:        position,
			ActorID:         actorID,
		}
		env, err := r.environment.Create(req.Context(), input)
		if err != nil {
			r.respondEnvironmentError(w, err)
			return
		}
		detail, detErr := r.environment.Detail(req.Context(), env.ID)
		if detErr != nil {
			if r.logger != nil {
				r.logger.Warn("failed to load environment detail after create", "environment_id", env.ID, "error", detErr)
			}
			writeJSON(w, http.StatusCreated, environment.EnvironmentDetails{Environment: *env})
			return
		}
		writeJSON(w, http.StatusCreated, detail)
	default:
		r.methodNotAllowed(w)
	}
}

func (r *Router) handleEnvironmentResource(w http.ResponseWriter, req *http.Request, projectID, environmentID string) {
	switch req.Method {
	case http.MethodGet:
		detail, err := r.environment.Detail(req.Context(), environmentID)
		if err != nil {
			r.respondEnvironmentError(w, err)
			return
		}
		if detail.Environment.ProjectID != projectID {
			r.notFound(w)
			return
		}
		writeJSON(w, http.StatusOK, detail)
	case http.MethodPatch:
		var payload struct {
			Name      *string `json:"name"`
			Slug      *string `json:"slug"`
			Type      *string `json:"type"`
			Protected *bool   `json:"protected"`
			Position  *int    `json:"position"`
		}
		decoder := json.NewDecoder(req.Body)
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&payload); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON body")
			return
		}
		input := environment.UpdateEnvironmentInput{EnvironmentID: environmentID}
		if payload.Name != nil {
			name := strings.TrimSpace(*payload.Name)
			input.Name = &name
		}
		if payload.Slug != nil {
			slug := strings.TrimSpace(*payload.Slug)
			input.Slug = &slug
		}
		if payload.Type != nil {
			typeVal := strings.TrimSpace(*payload.Type)
			input.EnvironmentType = &typeVal
		}
		if payload.Protected != nil {
			protected := *payload.Protected
			input.Protected = &protected
		}
		if payload.Position != nil {
			position := *payload.Position
			input.Position = &position
		}
		updated, err := r.environment.Update(req.Context(), input)
		if err != nil {
			r.respondEnvironmentError(w, err)
			return
		}
		if updated.ProjectID != projectID {
			r.notFound(w)
			return
		}
		detail, err := r.environment.Detail(req.Context(), updated.ID)
		if err != nil {
			r.respondEnvironmentError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, detail)
	default:
		r.methodNotAllowed(w)
	}
}

func (r *Router) handleEnvironmentVersions(w http.ResponseWriter, req *http.Request, projectID, environmentID string, segments []string) {
	if len(segments) == 0 || segments[0] == "" {
		r.handleEnvironmentVersionCollection(w, req, projectID, environmentID)
		return
	}
	versionID := strings.TrimSpace(segments[0])
	if versionID == "" {
		r.notFound(w)
		return
	}
	if len(segments) == 1 || segments[1] == "" {
		r.handleEnvironmentVersionResource(w, req, projectID, environmentID, versionID)
		return
	}
	r.notFound(w)
}

func (r *Router) handleEnvironmentVersionCollection(w http.ResponseWriter, req *http.Request, projectID, environmentID string) {
	switch req.Method {
	case http.MethodGet:
		limit, _ := strconv.Atoi(req.URL.Query().Get("limit"))
		env, err := r.environment.GetEnvironment(req.Context(), environmentID)
		if err != nil {
			r.respondEnvironmentError(w, err)
			return
		}
		if env.ProjectID != projectID {
			r.notFound(w)
			return
		}
		versions, err := r.environment.ListVersions(req.Context(), environmentID, limit)
		if err != nil {
			r.respondEnvironmentError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, versions)
	case http.MethodPost:
		var payload struct {
			Description string `json:"description"`
			Variables   []struct {
				Key   string `json:"key"`
				Value string `json:"value"`
			} `json:"variables"`
		}
		decoder := json.NewDecoder(req.Body)
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&payload); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON body")
			return
		}
		env, err := r.environment.GetEnvironment(req.Context(), environmentID)
		if err != nil {
			r.respondEnvironmentError(w, err)
			return
		}
		if env.ProjectID != projectID {
			r.notFound(w)
			return
		}
		info, _ := authInfoFromContext(req.Context())
		var actorID *string
		if strings.TrimSpace(info.UserID) != "" {
			user := strings.TrimSpace(info.UserID)
			actorID = &user
		}
		variables := make([]environment.VariableInput, 0, len(payload.Variables))
		for _, item := range payload.Variables {
			variables = append(variables, environment.VariableInput{Key: item.Key, Value: item.Value})
		}
		input := environment.CreateVersionInput{
			EnvironmentID: environmentID,
			Description:   payload.Description,
			Variables:     variables,
			ActorID:       actorID,
		}
		version, err := r.environment.CreateVersion(req.Context(), input)
		if err != nil {
			r.respondEnvironmentError(w, err)
			return
		}
		if version.Version.EnvironmentID != environmentID {
			r.notFound(w)
			return
		}
		writeJSON(w, http.StatusCreated, version)
	default:
		r.methodNotAllowed(w)
	}
}

func (r *Router) handleEnvironmentVersionResource(w http.ResponseWriter, req *http.Request, projectID, environmentID, versionID string) {
	if req.Method != http.MethodGet {
		r.methodNotAllowed(w)
		return
	}
	version, err := r.environment.GetVersion(req.Context(), versionID)
	if err != nil {
		r.respondEnvironmentError(w, err)
		return
	}
	if version.Version.EnvironmentID != environmentID {
		r.notFound(w)
		return
	}
	env, err := r.environment.GetEnvironment(req.Context(), environmentID)
	if err != nil {
		r.respondEnvironmentError(w, err)
		return
	}
	if env.ProjectID != projectID {
		r.notFound(w)
		return
	}
	writeJSON(w, http.StatusOK, version)
}

func (r *Router) handleEnvironmentAudits(w http.ResponseWriter, req *http.Request, projectID string) {
	if req.Method != http.MethodGet {
		r.methodNotAllowed(w)
		return
	}
	environmentID := strings.TrimSpace(req.URL.Query().Get("environment_id"))
	limit, _ := strconv.Atoi(req.URL.Query().Get("limit"))
	if environmentID != "" {
		env, err := r.environment.GetEnvironment(req.Context(), environmentID)
		if err != nil {
			r.respondEnvironmentError(w, err)
			return
		}
		if env.ProjectID != projectID {
			r.notFound(w)
			return
		}
	}
	audits, err := r.environment.ListAudits(req.Context(), projectID, environmentID, limit)
	if err != nil {
		r.respondEnvironmentError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, audits)
}

func (r *Router) respondEnvironmentError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, repository.ErrInvalidArgument):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, repository.ErrNotFound):
		writeError(w, http.StatusNotFound, err.Error())
	default:
		writeError(w, http.StatusInternalServerError, err.Error())
	}
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

func (r *Router) handleDeploymentDelete(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodDelete {
		r.methodNotAllowed(w)
		return
	}
	if _, ok := authInfoFromContext(req.Context()); !ok {
		r.logger.Error("auth context missing for deployment delete", "path", req.URL.Path)
		writeError(w, http.StatusInternalServerError, "authorization context missing")
		return
	}
	trimmed := strings.TrimPrefix(req.URL.Path, "/deployments/")
	if trimmed == "" {
		r.notFound(w)
		return
	}
	parts := strings.SplitN(trimmed, "/", 2)
	deploymentID := strings.TrimSpace(parts[0])
	if deploymentID == "" {
		r.notFound(w)
		return
	}
	if len(parts) > 1 && parts[1] != "" {
		r.notFound(w)
		return
	}
	if _, err := uuid.Parse(deploymentID); err != nil {
		writeError(w, http.StatusBadRequest, "invalid deployment id")
		return
	}
	if err := r.deploy.DeleteDeployment(req.Context(), deploymentID); err != nil {
		switch {
		case errors.Is(err, repository.ErrNotFound):
			writeError(w, http.StatusNotFound, err.Error())
		case errors.Is(err, repository.ErrInvalidArgument):
			writeError(w, http.StatusBadRequest, err.Error())
		default:
			writeError(w, http.StatusInternalServerError, err.Error())
		}
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
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
		} else if errors.Is(err, deploy.ErrInvalidProjectID) {
			status = http.StatusBadRequest
		} else if errors.Is(err, repository.ErrInvalidArgument) {
			status = http.StatusBadRequest
		}
		writeError(w, status, err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "received"})
}

func (r *Router) handleRuntimeEvents(w http.ResponseWriter, req *http.Request) {
	if r.runtime == nil {
		writeError(w, http.StatusServiceUnavailable, "runtime telemetry disabled")
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
		projectID := strings.TrimSpace(req.URL.Query().Get("project_id"))
		if projectID == "" {
			writeError(w, http.StatusBadRequest, "project_id query parameter required")
			return
		}
		eventType := strings.TrimSpace(req.URL.Query().Get("event_type"))
		limit, err := strconv.Atoi(req.URL.Query().Get("limit"))
		if err != nil || limit <= 0 {
			limit = 100
		}
		offset, err := strconv.Atoi(req.URL.Query().Get("offset"))
		if err != nil || offset < 0 {
			offset = 0
		}
		key := r.rateLimitKeyUser(req)
		if key == "" {
			key = rateLimitKeyIP(req)
		}
		decision := r.limiter.Allow(key, rateLimitRuntimeRead, rateWindowRealtime)
		r.applyRateHeaders(w, rateLimitRuntimeRead, decision)
		if !decision.allowed {
			r.recordRateLimitHit("/runtime/events", rateMetricKey(key))
			writeError(w, http.StatusTooManyRequests, "rate limit exceeded")
			return
		}
		events, err := r.runtime.ListEvents(req.Context(), projectID, eventType, limit, offset)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		payload := make([]json.RawMessage, 0, len(events))
		for _, event := range events {
			data, err := runtimeSvc.MarshalRuntimeEvent(event)
			if err != nil {
				if r.logger != nil {
					r.logger.Warn("failed to marshal runtime event", "project_id", projectID, "error", err)
				}
				continue
			}
			payload = append(payload, json.RawMessage(data))
		}
		writeJSON(w, http.StatusOK, payload)
	case http.MethodPost:
		if !r.verifyBuilderToken(w, req) {
			return
		}
		var payload struct {
			ProjectID  string          `json:"project_id"`
			Source     string          `json:"source"`
			EventType  string          `json:"event_type"`
			Level      string          `json:"level"`
			Message    string          `json:"message"`
			Method     string          `json:"method"`
			Path       string          `json:"path"`
			StatusCode *int            `json:"status_code"`
			LatencyMS  *float64        `json:"latency_ms"`
			BytesIn    *int64          `json:"bytes_in"`
			BytesOut   *int64          `json:"bytes_out"`
			Metadata   json.RawMessage `json:"metadata"`
			OccurredAt string          `json:"occurred_at"`
		}
		decoder := json.NewDecoder(req.Body)
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&payload); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON body")
			return
		}
		payload.ProjectID = strings.TrimSpace(payload.ProjectID)
		if payload.ProjectID == "" {
			writeError(w, http.StatusBadRequest, "project_id is required")
			return
		}
		builderKey := "runtime:" + payload.ProjectID
		decision := r.limiter.Allow(builderKey, rateLimitRuntimeWrite, rateWindowRealtime)
		r.applyRateHeaders(w, rateLimitRuntimeWrite, decision)
		if !decision.allowed {
			r.recordRateLimitHit("/runtime/events", rateMetricKey(builderKey))
			writeError(w, http.StatusTooManyRequests, "rate limit exceeded")
			return
		}
		var occurredAt time.Time
		if strings.TrimSpace(payload.OccurredAt) != "" {
			parsed, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(payload.OccurredAt))
			if err != nil {
				writeError(w, http.StatusBadRequest, "invalid occurred_at format")
				return
			}
			occurredAt = parsed.UTC()
		}
		event := domain.RuntimeEvent{
			ProjectID:  payload.ProjectID,
			Source:     strings.TrimSpace(payload.Source),
			EventType:  strings.TrimSpace(payload.EventType),
			Level:      strings.TrimSpace(payload.Level),
			Message:    strings.TrimSpace(payload.Message),
			Method:     strings.TrimSpace(payload.Method),
			Path:       strings.TrimSpace(payload.Path),
			StatusCode: payload.StatusCode,
			LatencyMS:  payload.LatencyMS,
			BytesIn:    payload.BytesIn,
			BytesOut:   payload.BytesOut,
			OccurredAt: occurredAt,
		}
		if len(payload.Metadata) > 0 {
			event.Metadata = append([]byte(nil), payload.Metadata...)
		}
		if err := r.runtime.Ingest(req.Context(), event); err != nil {
			switch {
			case errors.Is(err, repository.ErrNotFound):
				writeError(w, http.StatusNotFound, "project not found")
			case errors.Is(err, repository.ErrInvalidArgument):
				writeError(w, http.StatusBadRequest, err.Error())
			default:
				writeError(w, http.StatusInternalServerError, err.Error())
			}
			return
		}
		writeJSON(w, http.StatusAccepted, map[string]string{"status": "queued"})
	default:
		r.methodNotAllowed(w)
	}
}

func (r *Router) handleRuntimeMetrics(w http.ResponseWriter, req *http.Request) {
	if r.runtime == nil {
		writeError(w, http.StatusServiceUnavailable, "runtime telemetry disabled")
		return
	}
	if req.Method != http.MethodGet {
		r.methodNotAllowed(w)
		return
	}
	projectID := strings.TrimSpace(req.URL.Query().Get("project_id"))
	if projectID == "" {
		writeError(w, http.StatusBadRequest, "project_id query parameter required")
		return
	}
	key := r.rateLimitKeyUser(req)
	if key == "" {
		key = rateLimitKeyIP(req)
	}
	decision := r.limiter.Allow(key, rateLimitRuntimeRead, rateWindowRealtime)
	r.applyRateHeaders(w, rateLimitRuntimeRead, decision)
	if !decision.allowed {
		r.recordRateLimitHit("/runtime/metrics", rateMetricKey(key))
		writeError(w, http.StatusTooManyRequests, "rate limit exceeded")
		return
	}
	eventType := strings.TrimSpace(req.URL.Query().Get("event_type"))
	source := strings.TrimSpace(req.URL.Query().Get("source"))
	bucketSpanParam := strings.TrimSpace(req.URL.Query().Get("bucket_span"))
	var bucketSpan time.Duration
	if bucketSpanParam != "" {
		if seconds, err := strconv.Atoi(bucketSpanParam); err == nil && seconds > 0 {
			bucketSpan = time.Duration(seconds) * time.Second
		} else if duration, err := time.ParseDuration(bucketSpanParam); err == nil {
			bucketSpan = duration
		} else {
			writeError(w, http.StatusBadRequest, "invalid bucket_span value")
			return
		}
	}
	limit, err := strconv.Atoi(req.URL.Query().Get("limit"))
	if err != nil || limit <= 0 {
		limit = 120
	}
	rollups, err := r.runtime.ListRollups(req.Context(), projectID, eventType, source, bucketSpan, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, marshalRuntimeRollups(rollups))
}

func (r *Router) handleRuntimeStream(w http.ResponseWriter, req *http.Request) {
	if r.runtime == nil {
		writeError(w, http.StatusServiceUnavailable, "runtime telemetry disabled")
		return
	}
	if req.Method != http.MethodGet {
		r.methodNotAllowed(w)
		return
	}
	if _, ok := authInfoFromContext(req.Context()); !ok {
		r.logger.Error("auth context missing for runtime stream", "path", req.URL.Path)
		writeError(w, http.StatusInternalServerError, "authorization context missing")
		return
	}
	projectID := strings.TrimSpace(req.URL.Query().Get("project_id"))
	if projectID == "" {
		writeError(w, http.StatusBadRequest, "project_id query parameter required")
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		r.logger.Error("http flusher not supported for runtime sse", "path", req.URL.Path)
		writeError(w, http.StatusInternalServerError, "streaming not supported")
		return
	}
	hub := r.runtime.Hub()
	if hub == nil {
		writeError(w, http.StatusInternalServerError, "runtime stream unavailable")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	client := ws.NewSSEClient(w, flusher, r.logger)
	hub.Register(projectID, client)
	defer func() {
		hub.Unregister(projectID, client)
		client.Close()
	}()

	done := req.Context().Done()
	watchdogStop := make(chan struct{})
	go func() {
		ticker := time.NewTicker(runtimeStreamWatchdog)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				if time.Since(client.LastActivity()) > runtimeStreamWatchdog {
					if r.logger != nil {
						r.logger.Info("closing idle runtime stream", "project_id", projectID)
					}
					client.Close()
					return
				}
			case <-done:
				return
			case <-watchdogStop:
				return
			}
		}
	}()
	defer close(watchdogStop)
	if err := client.Heartbeat(); err != nil {
		return
	}
	if err := r.runtimeStreamBackfill(req.Context(), projectID, client); err != nil {
		return
	}
	ticker := time.NewTicker(runtimeStreamHeartbeat)
	defer ticker.Stop()
	for {
		select {
		case <-req.Context().Done():
			return
		case <-ticker.C:
			if err := client.Heartbeat(); err != nil {
				return
			}
		}
	}
}

func (r *Router) runtimeStreamBackfill(ctx context.Context, projectID string, client *ws.SSEClient) error {
	if r.runtime == nil {
		return nil
	}
	events, err := r.runtime.ListEvents(ctx, projectID, "", runtimeStreamBackfillLimit, 0)
	if err != nil {
		if r.logger != nil {
			r.logger.Warn("failed to load runtime backfill", "project_id", projectID, "error", err)
		}
		return nil
	}
	for i := len(events) - 1; i >= 0; i-- {
		data, err := runtimeSvc.MarshalRuntimeEvent(events[i])
		if err != nil {
			if r.logger != nil {
				r.logger.Warn("failed to marshal runtime backfill", "project_id", projectID, "error", err)
			}
			continue
		}
		if err := client.Send(data); err != nil {
			return err
		}
	}
	return nil
}

func marshalRuntimeRollups(rollups []domain.RuntimeMetricRollup) []map[string]any {
	result := make([]map[string]any, 0, len(rollups))
	for _, rollup := range rollups {
		payload := map[string]any{
			"project_id":          rollup.ProjectID,
			"bucket_start":        rollup.BucketStart.UTC().Format(time.RFC3339Nano),
			"bucket_span_seconds": int(rollup.BucketSpan / time.Second),
			"source":              rollup.Source,
			"event_type":          rollup.EventType,
			"count":               rollup.Count,
			"error_count":         rollup.ErrorCount,
			"updated_at":          rollup.UpdatedAt.UTC().Format(time.RFC3339Nano),
		}
		payload["p50_ms"] = rollup.P50MS
		payload["p90_ms"] = rollup.P90MS
		payload["p95_ms"] = rollup.P95MS
		payload["p99_ms"] = rollup.P99MS
		payload["max_ms"] = rollup.MaxMS
		payload["avg_ms"] = rollup.AvgMS
		result = append(result, payload)
	}
	return result
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
			r.recordRateLimitHit("/logs", rateMetricKey(key))
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
			r.recordRateLimitHit("/logs", rateMetricKey(builderKey))
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
			switch {
			case errors.Is(err, repository.ErrInvalidArgument):
				writeError(w, http.StatusBadRequest, "invalid project id")
			case errors.Is(err, repository.ErrNotFound):
				writeError(w, http.StatusNotFound, "project not found")
			default:
				writeError(w, http.StatusInternalServerError, err.Error())
			}
			return
		}
		writeJSON(w, http.StatusAccepted, map[string]string{"status": "queued"})
	default:
		r.methodNotAllowed(w)
	}
}

func (r *Router) handleLogsStream(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodGet {
		r.methodNotAllowed(w)
		return
	}
	if _, ok := authInfoFromContext(req.Context()); !ok {
		r.logger.Error("auth context missing for logs stream", "path", req.URL.Path)
		writeError(w, http.StatusInternalServerError, "authorization context missing")
		return
	}
	projectID := strings.TrimSpace(req.URL.Query().Get("project_id"))
	if projectID == "" {
		writeError(w, http.StatusBadRequest, "project_id query parameter required")
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		r.logger.Error("http flusher not supported for sse", "path", req.URL.Path)
		writeError(w, http.StatusInternalServerError, "streaming not supported")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	client := ws.NewSSEClient(w, flusher, r.logger)
	r.logs.Hub().Register(projectID, client)
	defer func() {
		r.logs.Hub().Unregister(projectID, client)
		client.Close()
	}()

	done := req.Context().Done()
	watchdogStop := make(chan struct{})
	go func() {
		ticker := time.NewTicker(logStreamWatchdog)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				if time.Since(client.LastActivity()) > logStreamWatchdog {
					r.logger.Info("closing idle log stream", "project_id", projectID)
					client.Close()
					return
				}
			case <-done:
				return
			case <-watchdogStop:
				return
			}
		}
	}()
	defer close(watchdogStop)
	if err := client.Heartbeat(); err != nil {
		return
	}
	if err := r.streamBackfill(req.Context(), projectID, client); err != nil {
		return
	}
	ticker := time.NewTicker(logStreamHeartbeat)
	defer ticker.Stop()
	for {
		select {
		case <-req.Context().Done():
			return
		case <-ticker.C:
			if err := client.Heartbeat(); err != nil {
				return
			}
		}
	}
}

func (r *Router) streamBackfill(ctx context.Context, projectID string, client *ws.SSEClient) error {
	entries, err := r.logs.List(ctx, projectID, logStreamBackfillLimit, 0)
	if err != nil {
		r.logger.Warn("failed to load log backfill", "project_id", projectID, "error", err)
		return nil
	}
	for i := len(entries) - 1; i >= 0; i-- {
		data, err := logs.MarshalEntry(entries[i])
		if err != nil {
			r.logger.Warn("failed to marshal log backfill", "project_id", projectID, "error", err)
			continue
		}
		if err := client.Send(data); err != nil {
			return err
		}
	}
	return nil
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
		r.handlerAuthRate("/webhook/:project_id/secret", rateLimitUserWrite, rateWindowDefault, func(w http.ResponseWriter, req *http.Request) {
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

func (r *Router) audit(route string, next http.HandlerFunc) http.HandlerFunc {
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
		label := route
		if label == "" {
			label = req.URL.Path
		}
		r.recordRequestMetrics(req.Method, label, status, duration)
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
