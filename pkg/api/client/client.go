package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Client provides typed access to the peep API for interactive tools.
type Client struct {
	baseURL    string
	httpClient *http.Client
}

// Option customises client instantiation.
type Option func(*Client)

// WithHTTPClient overrides the default HTTP client.
func WithHTTPClient(h *http.Client) Option {
	return func(c *Client) {
		if h != nil {
			c.httpClient = h
		}
	}
}

// New constructs a Client pointing at the provided API base URL.
func New(base string, opts ...Option) (*Client, error) {
	trimmed := strings.TrimSpace(base)
	if trimmed == "" {
		trimmed = "http://localhost:4000"
	}
	if !strings.HasPrefix(trimmed, "http://") && !strings.HasPrefix(trimmed, "https://") {
		trimmed = "http://" + trimmed
	}
	if _, err := url.Parse(trimmed); err != nil {
		return nil, fmt.Errorf("invalid api base url: %w", err)
	}
	cli := &Client{
		baseURL:    strings.TrimRight(trimmed, "/"),
		httpClient: &http.Client{Timeout: 15 * time.Second},
	}
	for _, opt := range opts {
		opt(cli)
	}
	return cli, nil
}

// APIError represents an error response from the API.
type APIError struct {
	Status  int
	Message string
}

func (e APIError) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("api request failed with status %d", e.Status)
	}
	return fmt.Sprintf("api request failed (%d): %s", e.Status, e.Message)
}

func (c *Client) do(ctx context.Context, method, path string, body any, token string, v any) error {
	if c == nil {
		return fmt.Errorf("client is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	endpoint := c.baseURL + path
	var reader io.Reader
	if body != nil {
		payload, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encode request body: %w", err)
		}
		reader = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, reader)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if strings.TrimSpace(token) != "" {
		req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(token))
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("perform request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= http.StatusBadRequest {
		msg := extractError(resp.Body)
		return APIError{Status: resp.StatusCode, Message: msg}
	}

	if v == nil {
		return nil
	}
	decoder := json.NewDecoder(resp.Body)
	if err := decoder.Decode(v); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}

func extractError(body io.Reader) string {
	if body == nil {
		return ""
	}
	var payload struct {
		Error string `json:"error"`
	}
	data, err := io.ReadAll(body)
	if err != nil || len(data) == 0 {
		return ""
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return strings.TrimSpace(string(data))
	}
	return strings.TrimSpace(payload.Error)
}

// LoginResponse captures the token payload emitted by the API.
type LoginResponse struct {
	User   User      `json:"user"`
	Tokens TokenPair `json:"tokens"`
}

// User reflects API user payloads.
type User struct {
	ID    string `json:"id"`
	Email string `json:"email"`
}

// TokenPair includes access and refresh tokens.
type TokenPair struct {
	AccessToken  string        `json:"AccessToken"`
	RefreshToken string        `json:"RefreshToken"`
	ExpiresIn    time.Duration `json:"ExpiresIn"`
}

// Login exchanges credentials for a token pair.
func (c *Client) Login(ctx context.Context, email, password string) (LoginResponse, error) {
	body := map[string]string{
		"email":    email,
		"password": password,
	}
	var resp LoginResponse
	if err := c.do(ctx, http.MethodPost, "/auth/login", body, "", &resp); err != nil {
		return LoginResponse{}, err
	}
	return resp, nil
}

// Team represents a collaborative workspace.
type Team struct {
	ID             string    `json:"id"`
	Name           string    `json:"name"`
	OwnerID        string    `json:"owner_id"`
	MaxProjects    int       `json:"max_projects"`
	MaxContainers  int       `json:"max_containers"`
	StorageLimitMB int       `json:"storage_limit_mb"`
	CreatedAt      time.Time `json:"created_at"`
}

// ListTeams returns all teams for the authenticated user.
func (c *Client) ListTeams(ctx context.Context, token string) ([]Team, error) {
	var teams []Team
	if err := c.do(ctx, http.MethodGet, "/teams", nil, token, &teams); err != nil {
		return nil, err
	}
	return teams, nil
}

// Project describes a deployable unit.
type Project struct {
	ID           string    `json:"id"`
	TeamID       string    `json:"team_id"`
	Name         string    `json:"name"`
	RepoURL      string    `json:"repo_url"`
	Type         string    `json:"type"`
	BuildCommand string    `json:"build_command"`
	RunCommand   string    `json:"run_command"`
	CreatedAt    time.Time `json:"created_at"`
}

// ListProjects returns projects for the specified team.
func (c *Client) ListProjects(ctx context.Context, token, teamID string) ([]Project, error) {
	path := fmt.Sprintf("/projects?team_id=%s", url.QueryEscape(teamID))
	var projects []Project
	if err := c.do(ctx, http.MethodGet, path, nil, token, &projects); err != nil {
		return nil, err
	}
	return projects, nil
}

// GetProject fetches detailed information about a project.
func (c *Client) GetProject(ctx context.Context, token, projectID string) (Project, error) {
	path := fmt.Sprintf("/projects/%s", url.PathEscape(projectID))
	var project Project
	if err := c.do(ctx, http.MethodGet, path, nil, token, &project); err != nil {
		return Project{}, err
	}
	return project, nil
}

// CreateProjectInput captures the payload for project creation.
type CreateProjectInput struct {
	TeamID       string `json:"TeamID"`
	Name         string `json:"Name"`
	RepoURL      string `json:"RepoURL"`
	Type         string `json:"Type"`
	BuildCommand string `json:"BuildCommand"`
	RunCommand   string `json:"RunCommand"`
}

// CreateProject provisions a new project.
func (c *Client) CreateProject(ctx context.Context, token string, input CreateProjectInput) (Project, error) {
	var project Project
	if err := c.do(ctx, http.MethodPost, "/projects", input, token, &project); err != nil {
		return Project{}, err
	}
	return project, nil
}

// EnvVar represents a decrypted environment variable.
type EnvVar struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// ListEnvVars returns environment variables for the project.
func (c *Client) ListEnvVars(ctx context.Context, token, projectID string) ([]EnvVar, error) {
	path := fmt.Sprintf("/projects/%s/env", url.PathEscape(projectID))
	var vars []EnvVar
	if err := c.do(ctx, http.MethodGet, path, nil, token, &vars); err != nil {
		return nil, err
	}
	return vars, nil
}

// AddEnvVarInput adds or updates an environment variable for the project.
type AddEnvVarInput struct {
	Key   string `json:"Key"`
	Value string `json:"Value"`
}

// AddEnvVar stores an environment variable for a project.
func (c *Client) AddEnvVar(ctx context.Context, token, projectID string, input AddEnvVarInput) error {
	path := fmt.Sprintf("/projects/%s/env", url.PathEscape(projectID))
	return c.do(ctx, http.MethodPost, path, input, token, nil)
}

// Deployment represents API deployment payloads.
type Deployment struct {
	ID          string          `json:"ID"`
	ProjectID   string          `json:"ProjectID"`
	CommitSHA   string          `json:"CommitSHA"`
	Status      string          `json:"Status"`
	Stage       string          `json:"Stage"`
	Message     string          `json:"Message"`
	URL         string          `json:"URL"`
	Error       string          `json:"Error"`
	Metadata    json.RawMessage `json:"Metadata"`
	StartedAt   time.Time       `json:"StartedAt"`
	CompletedAt *time.Time      `json:"CompletedAt"`
	UpdatedAt   time.Time       `json:"UpdatedAt"`
}

// TriggerDeployment requests a new deployment for the project.
func (c *Client) TriggerDeployment(ctx context.Context, token, projectID, commit string) (Deployment, error) {
	body := map[string]string{}
	if strings.TrimSpace(commit) != "" {
		body["commit"] = commit
	}
	path := fmt.Sprintf("/deploy/%s", url.PathEscape(projectID))
	var deployment Deployment
	if err := c.do(ctx, http.MethodPost, path, body, token, &deployment); err != nil {
		return Deployment{}, err
	}
	return deployment, nil
}

// ListDeployments fetches recent deployments for a project.
func (c *Client) ListDeployments(ctx context.Context, token, projectID string, limit int) ([]Deployment, error) {
	query := ""
	if limit > 0 {
		query = fmt.Sprintf("?limit=%d", limit)
	}
	path := fmt.Sprintf("/deploy/%s%s", url.PathEscape(projectID), query)
	var deployments []Deployment
	if err := c.do(ctx, http.MethodGet, path, nil, token, &deployments); err != nil {
		return nil, err
	}
	return deployments, nil
}

// DeleteDeployment removes a deployment and associated runtime state.
func (c *Client) DeleteDeployment(ctx context.Context, token, deploymentID string) error {
	path := fmt.Sprintf("/deployments/%s", url.PathEscape(deploymentID))
	return c.do(ctx, http.MethodDelete, path, nil, token, nil)
}

// LogEntry models a project log entry.
type LogEntry struct {
	ID        int64           `json:"ID"`
	ProjectID string          `json:"ProjectID"`
	Source    string          `json:"Source"`
	Level     string          `json:"Level"`
	Message   string          `json:"Message"`
	Metadata  json.RawMessage `json:"Metadata"`
	CreatedAt time.Time       `json:"CreatedAt"`
}

// FetchLogs returns recent logs for the project.
func (c *Client) FetchLogs(ctx context.Context, token, projectID string, limit int) ([]LogEntry, error) {
	query := ""
	if limit > 0 {
		query = fmt.Sprintf("?limit=%d", limit)
	}
	path := fmt.Sprintf("/logs/%s%s", url.PathEscape(projectID), query)
	var logs []LogEntry
	if err := c.do(ctx, http.MethodGet, path, nil, token, &logs); err != nil {
		return nil, err
	}
	return logs, nil
}
