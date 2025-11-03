package server

import (
	"context"
	"errors"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/splax/localvercel/dashboard/internal/config"
	"github.com/splax/localvercel/dashboard/internal/session"
	apiclient "github.com/splax/localvercel/pkg/api/client"
)

// Server hosts the dashboard web UI.
type Server struct {
	cfg       config.Config
	api       *apiclient.Client
	sessions  session.Manager
	templates *template.Template
	mux       *http.ServeMux
	logger    *slog.Logger
}

// New constructs a configured server ready to serve HTTP traffic.
func New(cfg config.Config) (*Server, error) {
	if strings.TrimSpace(cfg.SessionSecret) == "" {
		return nil, errors.New("SESSION_SECRET must be configured for the dashboard")
	}
	apiClient, err := apiclient.New(cfg.APIBaseURL)
	if err != nil {
		return nil, err
	}
	sessionMgr, err := session.New(cfg.SessionSecret, cfg.CookieName, cfg.CookieSecure)
	if err != nil {
		return nil, err
	}
	tmplFS, err := fs.Sub(templateFS, "internal/server/templates")
	if err != nil {
		return nil, err
	}
	templates, err := template.New("base").Funcs(template.FuncMap{}).ParseFS(tmplFS, "*.html")
	if err != nil {
		return nil, err
	}
	srv := &Server{
		cfg:       cfg,
		api:       apiClient,
		sessions:  sessionMgr,
		templates: templates,
		mux:       http.NewServeMux(),
		logger:    slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})),
	}
	srv.registerRoutes()
	return srv, nil
}

// ServeHTTP conforms to http.Handler.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

func (s *Server) registerRoutes() {
	s.mux.HandleFunc("/login", s.handleLogin)
	s.mux.HandleFunc("/logout", s.requireAuth(s.handleLogout))
	s.mux.HandleFunc("/", s.requireAuth(s.handleHome))
	s.mux.HandleFunc("/projects", s.requireAuth(s.handleProjectCreate))
	s.mux.HandleFunc("/projects/", s.requireAuth(s.handleProjectSubroutes))
	s.mux.HandleFunc("/deployments/", s.requireAuth(s.handleDeploymentDelete))
}

func (s *Server) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, err := s.sessions.TokenFromRequest(r); err != nil {
			if errors.Is(err, http.ErrNoCookie) {
				http.Redirect(w, r, "/login", http.StatusSeeOther)
				return
			}
			s.logger.Warn("session validation failed", "error", err)
			http.Redirect(w, r, "/login?flash=please+sign+in", http.StatusSeeOther)
			return
		}
		next(w, r)
	}
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		if token, err := s.sessions.TokenFromRequest(r); err == nil && strings.TrimSpace(token) != "" {
			http.Redirect(w, r, "/", http.StatusSeeOther)
			return
		}
		data := map[string]any{
			"Title":      "Sign in",
			"Flash":      flashFromRequest(r),
			"HideChrome": true,
		}
		s.render(w, r, "login", data)
	case http.MethodPost:
		if err := r.ParseForm(); err != nil {
			s.renderError(w, r, http.StatusBadRequest, "invalid form payload")
			return
		}
		email := r.PostFormValue("email")
		password := r.PostFormValue("password")
		resp, err := s.api.Login(r.Context(), email, password)
		if err != nil {
			s.logger.Warn("login failed", "error", err)
			s.render(w, r, "login", map[string]any{
				"Title":      "Sign in",
				"Flash":      "login failed: " + err.Error(),
				"HideChrome": true,
				"Email":      email,
			})
			return
		}
		cookie, err := s.sessions.MakeCookie(resp.Tokens.AccessToken, resp.Tokens.ExpiresIn)
		if err != nil {
			s.renderError(w, r, http.StatusInternalServerError, "session issuance failed")
			return
		}
		http.SetCookie(w, cookie)
		http.Redirect(w, r, "/", http.StatusSeeOther)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	http.SetCookie(w, s.sessions.ExpireCookie())
	http.Redirect(w, r, "/login?flash=Signed+out", http.StatusSeeOther)
}

func (s *Server) handleHome(w http.ResponseWriter, r *http.Request) {
	token, _ := s.sessions.TokenFromRequest(r)
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	teams, err := s.api.ListTeams(ctx, token)
	if err != nil {
		s.renderError(w, r, http.StatusBadGateway, "failed to load teams")
		return
	}

	selected := strings.TrimSpace(r.URL.Query().Get("team"))
	if selected == "" && len(teams) > 0 {
		selected = teams[0].ID
	}

	var projects []apiclient.Project
	if selected != "" {
		projects, err = s.api.ListProjects(ctx, token, selected)
		if err != nil {
			s.renderError(w, r, http.StatusBadGateway, "failed to load projects")
			return
		}
	}

	data := map[string]any{
		"Title":        "Projects",
		"Flash":        flashFromRequest(r),
		"Teams":        teams,
		"SelectedTeam": selected,
		"Projects":     projects,
	}
	s.render(w, r, "home", data)
}

func (s *Server) handleProjectCreate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		s.renderError(w, r, http.StatusBadRequest, "invalid form payload")
		return
	}
	token, _ := s.sessions.TokenFromRequest(r)
	input := apiclient.CreateProjectInput{
		TeamID:       r.PostFormValue("team_id"),
		Name:         r.PostFormValue("name"),
		RepoURL:      r.PostFormValue("repo_url"),
		Type:         r.PostFormValue("type"),
		BuildCommand: r.PostFormValue("build_command"),
		RunCommand:   r.PostFormValue("run_command"),
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	if _, err := s.api.CreateProject(ctx, token, input); err != nil {
		s.logger.Warn("project create failed", "error", err)
		redirectWithFlash(w, r, "/?team="+url.QueryEscape(input.TeamID), "project creation failed: "+err.Error())
		return
	}
	redirectWithFlash(w, r, "/?team="+url.QueryEscape(input.TeamID), "project created")
}

func (s *Server) handleProjectSubroutes(w http.ResponseWriter, r *http.Request) {
	trimmed := strings.TrimPrefix(r.URL.Path, "/projects/")
	parts := strings.Split(trimmed, "/")
	if len(parts) == 0 {
		http.NotFound(w, r)
		return
	}
	projectID := strings.TrimSpace(parts[0])
	if projectID == "" {
		http.NotFound(w, r)
		return
	}
	if len(parts) == 1 {
		s.handleProjectDetail(w, r, projectID)
		return
	}
	switch parts[1] {
	case "env":
		s.handleProjectEnv(w, r, projectID)
	case "deploy":
		s.handleProjectDeploy(w, r, projectID)
	default:
		http.NotFound(w, r)
	}
}

func (s *Server) handleProjectDetail(w http.ResponseWriter, r *http.Request, projectID string) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	token, _ := s.sessions.TokenFromRequest(r)
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	project, err := s.api.GetProject(ctx, token, projectID)
	if err != nil {
		s.renderError(w, r, http.StatusBadGateway, "failed to load project")
		return
	}
	envVars, err := s.api.ListEnvVars(ctx, token, projectID)
	if err != nil {
		s.renderError(w, r, http.StatusBadGateway, "failed to load env vars")
		return
	}
	deployments, err := s.api.ListDeployments(ctx, token, projectID, 10)
	if err != nil {
		s.renderError(w, r, http.StatusBadGateway, "failed to load deployments")
		return
	}
	logs, err := s.api.FetchLogs(ctx, token, projectID, 50)
	if err != nil {
		s.renderError(w, r, http.StatusBadGateway, "failed to load logs")
		return
	}

	data := map[string]any{
		"Title":       project.Name,
		"Flash":       flashFromRequest(r),
		"Project":     project,
		"EnvVars":     envVars,
		"Deployments": deployments,
		"Logs":        logs,
	}
	s.render(w, r, "project", data)
}

func (s *Server) handleProjectEnv(w http.ResponseWriter, r *http.Request, projectID string) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		s.renderError(w, r, http.StatusBadRequest, "invalid form payload")
		return
	}
	token, _ := s.sessions.TokenFromRequest(r)
	input := apiclient.AddEnvVarInput{
		Key:   r.PostFormValue("key"),
		Value: r.PostFormValue("value"),
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	if err := s.api.AddEnvVar(ctx, token, projectID, input); err != nil {
		redirectWithFlash(w, r, "/projects/"+url.PathEscape(projectID), "failed to store env var: "+err.Error())
		return
	}
	redirectWithFlash(w, r, "/projects/"+url.PathEscape(projectID), "environment variable stored")
}

func (s *Server) handleProjectDeploy(w http.ResponseWriter, r *http.Request, projectID string) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		s.renderError(w, r, http.StatusBadRequest, "invalid form payload")
		return
	}
	token, _ := s.sessions.TokenFromRequest(r)
	commit := r.PostFormValue("commit")
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	if _, err := s.api.TriggerDeployment(ctx, token, projectID, commit); err != nil {
		redirectWithFlash(w, r, "/projects/"+url.PathEscape(projectID), "failed to trigger deployment: "+err.Error())
		return
	}
	redirectWithFlash(w, r, "/projects/"+url.PathEscape(projectID), "deployment triggered")
}

func (s *Server) handleDeploymentDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	trimmed := strings.TrimPrefix(r.URL.Path, "/deployments/")
	trimmed = strings.TrimSuffix(trimmed, "/delete")
	deploymentID := strings.TrimSpace(trimmed)
	if deploymentID == "" {
		http.NotFound(w, r)
		return
	}
	token, _ := s.sessions.TokenFromRequest(r)
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	if err := s.api.DeleteDeployment(ctx, token, deploymentID); err != nil {
		redirectWithFlash(w, r, r.Header.Get("Referer"), "failed to delete deployment: "+err.Error())
		return
	}
	referer := r.Header.Get("Referer")
	if strings.TrimSpace(referer) == "" {
		referer = "/"
	}
	redirectWithFlash(w, r, referer, "deployment deleted")
}

func (s *Server) render(w http.ResponseWriter, r *http.Request, tpl string, data map[string]any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.templates.ExecuteTemplate(w, tpl, data); err != nil {
		s.logger.Error("template render failed", "template", tpl, "error", err)
		http.Error(w, "template error", http.StatusInternalServerError)
	}
}

func (s *Server) renderError(w http.ResponseWriter, r *http.Request, status int, message string) {
	s.logger.Warn("dashboard error", "status", status, "message", message)
	http.Error(w, message, status)
}

func flashFromRequest(r *http.Request) string {
	return strings.TrimSpace(r.URL.Query().Get("flash"))
}

func redirectWithFlash(w http.ResponseWriter, r *http.Request, target, message string) {
	if strings.TrimSpace(target) == "" {
		target = "/"
	}
	if strings.TrimSpace(message) == "" {
		http.Redirect(w, r, target, http.StatusSeeOther)
		return
	}
	u, err := url.Parse(target)
	if err != nil {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	q := u.Query()
	q.Set("flash", message)
	u.RawQuery = q.Encode()
	http.Redirect(w, r, u.String(), http.StatusSeeOther)
}
