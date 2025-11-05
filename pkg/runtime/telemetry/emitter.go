package telemetry

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	defaultTimeout   = 5 * time.Second
	maxErrorBodySize = 4096
)

// ErrUnauthorized indicates the API rejected authentication for runtime telemetry requests.
var ErrUnauthorized = errors.New("runtime telemetry unauthorized")

// ErrInvalidResponse indicates the API returned a malformed response payload.
var ErrInvalidResponse = errors.New("runtime telemetry invalid response")

// ErrInvalidArgument indicates the API rejected the payload with validation errors.
var ErrInvalidArgument = errors.New("runtime telemetry invalid argument")

// ErrNotFound indicates the API could not locate the referenced project.
var ErrNotFound = errors.New("runtime telemetry project not found")

// Emitter sends runtime telemetry events to the peep API.
type Emitter struct {
	baseURL string
	token   string
	client  *http.Client
	now     func() time.Time
}

// Event represents a runtime telemetry payload for the API.
type Event struct {
	ProjectID  string
	Source     string
	EventType  string
	Level      string
	Message    string
	Method     string
	Path       string
	StatusCode *int
	LatencyMS  *float64
	BytesIn    *int64
	BytesOut   *int64
	Metadata   json.RawMessage
	OccurredAt time.Time
}

// NewEmitter creates a telemetry emitter using the provided API base URL and builder token.
func NewEmitter(baseURL, builderToken string, client *http.Client) (*Emitter, error) {
	trimmed := strings.TrimSpace(baseURL)
	if trimmed == "" {
		return nil, errors.New("runtime telemetry base url required")
	}
	trimmed = strings.TrimRight(trimmed, "/")
	if client == nil {
		client = &http.Client{Timeout: defaultTimeout}
	} else if client.Timeout == 0 {
		client.Timeout = defaultTimeout
	}
	return &Emitter{
		baseURL: trimmed,
		token:   strings.TrimSpace(builderToken),
		client:  client,
		now:     time.Now,
	}, nil
}

// Emit sends the supplied event to the runtime telemetry ingestion endpoint.
func (e *Emitter) Emit(ctx context.Context, event Event) error {
	if e == nil {
		return errors.New("runtime telemetry emitter not initialised")
	}
	projectID := strings.TrimSpace(event.ProjectID)
	if projectID == "" {
		return errors.New("runtime telemetry requires project_id")
	}
	payload := buildPayload(projectID, event, e.now)
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal telemetry event: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.baseURL+"/runtime/events", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build telemetry request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if e.token != "" {
		req.Header.Set("X-Builder-Token", e.token)
	}
	resp, err := e.client.Do(req)
	if err != nil {
		return fmt.Errorf("send telemetry request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= http.StatusBadRequest {
		return e.errorForStatus(resp)
	}
	return nil
}

func (e *Emitter) errorForStatus(resp *http.Response) error {
	limited := io.LimitReader(resp.Body, maxErrorBodySize)
	buf, _ := io.ReadAll(limited)
	summary := strings.TrimSpace(string(buf))
	if summary == "" {
		summary = resp.Status
	}
	switch resp.StatusCode {
	case http.StatusUnauthorized, http.StatusForbidden:
		return fmt.Errorf("%w: %s", ErrUnauthorized, summary)
	case http.StatusBadRequest:
		return fmt.Errorf("%w: %s", ErrInvalidArgument, summary)
	case http.StatusNotFound:
		return fmt.Errorf("%w: %s", ErrNotFound, summary)
	default:
		return fmt.Errorf("telemetry request failed: %s", summary)
	}
}

func buildPayload(projectID string, event Event, nowFn func() time.Time) map[string]any {
	occurred := event.OccurredAt
	if occurred.IsZero() {
		occurred = nowFn().UTC()
	} else {
		occurred = occurred.UTC()
	}
	source := strings.TrimSpace(event.Source)
	if source == "" {
		source = "runtime"
	}
	eventType := strings.TrimSpace(event.EventType)
	if eventType == "" {
		eventType = "http_request"
	}
	level := strings.TrimSpace(event.Level)
	if level == "" {
		level = "info"
	}
	payload := map[string]any{
		"project_id":  projectID,
		"source":      source,
		"event_type":  eventType,
		"level":       level,
		"message":     strings.TrimSpace(event.Message),
		"method":      strings.TrimSpace(event.Method),
		"path":        strings.TrimSpace(event.Path),
		"status_code": event.StatusCode,
		"latency_ms":  event.LatencyMS,
		"bytes_in":    event.BytesIn,
		"bytes_out":   event.BytesOut,
		"metadata":    event.Metadata,
		"occurred_at": occurred.Format(time.RFC3339Nano),
	}
	return payload
}
