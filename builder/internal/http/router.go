package httpx

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"

	"log/slog"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/splax/localvercel/builder/internal/service/deploy"
)

// Router exposes HTTP endpoints for the builder service.
type Router struct {
	mux                *http.ServeMux
	logger             *slog.Logger
	deploy             deploy.Service
	metricsOnce        sync.Once
	metricsInitialized bool
	requestTotal       *prometheus.CounterVec
	requestDuration    *prometheus.HistogramVec
	deployResults      *prometheus.CounterVec
}

const healthCheckTimeout = 2 * time.Second

// New creates and registers handlers.
func New(logger *slog.Logger, deploySvc deploy.Service) *Router {
	r := &Router{
		mux:    http.NewServeMux(),
		logger: logger,
		deploy: deploySvc,
	}
	r.initMetrics()
	r.routes()
	return r
}

// ServeHTTP satisfies http.Handler.
func (r *Router) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	r.mux.ServeHTTP(w, req)
}

func (r *Router) routes() {
	r.mux.HandleFunc("/metrics", promhttp.Handler().ServeHTTP)
	r.mux.HandleFunc("/healthz", r.instrument("/healthz", r.handleHealth))
	r.mux.HandleFunc("/deploy", r.instrument("/deploy", r.handleDeploy))
	r.mux.HandleFunc("/deploy/", r.instrument("/deploy/:id", r.handleDeployDelete))
}

func (r *Router) handleHealth(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodGet {
		r.writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	ctx, cancel := context.WithTimeout(req.Context(), healthCheckTimeout)
	defer cancel()
	component := map[string]any{"status": "up"}
	status := "ok"
	if err := r.deploy.Health(ctx); err != nil {
		status = "degraded"
		component = map[string]any{
			"status": "down",
			"error":  err.Error(),
		}
	}
	payload := map[string]any{
		"status": status,
		"components": map[string]any{
			"docker": component,
		},
		"timestamp": time.Now().UTC().Format(time.RFC3339Nano),
	}
	code := http.StatusOK
	if status != "ok" {
		code = http.StatusServiceUnavailable
	}
	r.writeJSON(w, code, payload)
}

func (r *Router) handleDeploy(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		r.writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var payload deploy.Request
	if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
		r.writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	result, err := r.deploy.Handle(req.Context(), payload)
	if err != nil {
		r.recordDeployResult("failure")
		r.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	r.recordDeployResult("success")
	r.writeJSON(w, http.StatusAccepted, result)
}

func (r *Router) handleDeployDelete(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodDelete {
		r.writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	deploymentID := strings.TrimPrefix(req.URL.Path, "/deploy/")
	deploymentID = strings.Trim(deploymentID, "/")
	if deploymentID == "" {
		r.writeError(w, http.StatusBadRequest, "deployment id required")
		return
	}
	if err := r.deploy.Cancel(req.Context(), deploymentID); err != nil {
		r.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	r.writeJSON(w, http.StatusAccepted, map[string]string{"status": "cancelling"})
}

func (r *Router) writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		r.logger.Error("failed to encode response", "error", err)
	}
}

func (r *Router) writeError(w http.ResponseWriter, status int, msg string) {
	r.writeJSON(w, status, map[string]string{"error": msg})
}
