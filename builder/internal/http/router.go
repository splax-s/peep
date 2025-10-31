package httpx

import (
	"encoding/json"
	"net/http"

	"log/slog"

	"github.com/splax/localvercel/builder/internal/service/deploy"
)

// Router exposes HTTP endpoints for the builder service.
type Router struct {
	mux     *http.ServeMux
	logger  *slog.Logger
	deploy  deploy.Service
}

// New creates and registers handlers.
func New(logger *slog.Logger, deploySvc deploy.Service) *Router {
	r := &Router{
		mux:    http.NewServeMux(),
		logger: logger,
		deploy: deploySvc,
	}
	r.routes()
	return r
}

// ServeHTTP satisfies http.Handler.
func (r *Router) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	r.mux.ServeHTTP(w, req)
}

func (r *Router) routes() {
	r.mux.HandleFunc("/deploy", r.handleDeploy)
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
		r.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	r.writeJSON(w, http.StatusAccepted, result)
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
