package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/splax/localvercel/api/internal/domain"
	"github.com/splax/localvercel/api/internal/repository"
	"github.com/splax/localvercel/api/internal/ws"
)

const (
	defaultBucketSpan    = time.Minute
	defaultFlushInterval = 30 * time.Second
)

// TelemetryService ingests runtime events and maintains aggregated rollups.
type TelemetryService struct {
	repo          repository.RuntimeEventRepository
	hub           *ws.Hub
	aggregator    *rollupAggregator
	bucketSpan    time.Duration
	flushInterval time.Duration
	logger        *slog.Logger
	now           func() time.Time
	once          sync.Once
}

// NewTelemetryService constructs a TelemetryService with sane defaults.
func NewTelemetryService(repo repository.RuntimeEventRepository, hub *ws.Hub, logger *slog.Logger, bucketSpan, flushInterval time.Duration) *TelemetryService {
	if bucketSpan <= 0 {
		bucketSpan = defaultBucketSpan
	}
	if flushInterval <= 0 {
		flushInterval = defaultFlushInterval
	}
	if flushInterval > bucketSpan {
		flushInterval = bucketSpan
	}
	if hub == nil {
		hub = ws.NewHub()
	}
	if logger != nil {
		logger = logger.With("component", "runtime_telemetry")
	}
	now := time.Now
	return &TelemetryService{
		repo:          repo,
		hub:           hub,
		aggregator:    newRollupAggregator(bucketSpan, 0, now),
		bucketSpan:    bucketSpan,
		flushInterval: flushInterval,
		logger:        logger,
		now:           now,
	}
}

// Run starts the background rollup flusher. It blocks until the context is cancelled.
func (s *TelemetryService) Run(ctx context.Context) {
	if s == nil {
		return
	}
	s.once.Do(func() {
		if s.logger != nil {
			s.logger.Info("runtime telemetry service started", "bucket_span", s.bucketSpan, "flush_interval", s.flushInterval)
		}
	})
	ticker := time.NewTicker(s.flushInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			s.flushAll(context.Background())
			if s.logger != nil {
				s.logger.Info("runtime telemetry service stopped")
			}
			return
		case <-ticker.C:
			s.flushStale(ctx)
		}
	}
}

// Ingest persists a runtime event, updates rollups, and broadcasts to subscribers.
func (s *TelemetryService) Ingest(ctx context.Context, event domain.RuntimeEvent) error {
	if s == nil {
		return errors.New("telemetry service not initialised")
	}
	event.ProjectID = strings.TrimSpace(event.ProjectID)
	if event.ProjectID == "" {
		return errors.New("project_id required")
	}
	if event.OccurredAt.IsZero() {
		event.OccurredAt = s.now().UTC()
	} else {
		event.OccurredAt = event.OccurredAt.UTC()
	}
	if event.IngestedAt.IsZero() {
		event.IngestedAt = s.now().UTC()
	}
	copyEvent := event
	if err := s.repo.InsertRuntimeEvent(ctx, &copyEvent); err != nil {
		return err
	}
	s.aggregator.add(copyEvent)
	s.broadcast(copyEvent)
	return nil
}

// ListEvents returns recent runtime events for a project.
func (s *TelemetryService) ListEvents(ctx context.Context, projectID string, eventType string, limit, offset int) ([]domain.RuntimeEvent, error) {
	if s == nil {
		return nil, errors.New("telemetry service not initialised")
	}
	return s.repo.ListRuntimeEvents(ctx, strings.TrimSpace(projectID), strings.TrimSpace(eventType), limit, offset)
}

// ListRollups returns aggregated metrics for a project.
func (s *TelemetryService) ListRollups(ctx context.Context, projectID string, eventType string, source string, bucketSpan time.Duration, limit int) ([]domain.RuntimeMetricRollup, error) {
	if s == nil {
		return nil, errors.New("telemetry service not initialised")
	}
	if bucketSpan <= 0 {
		bucketSpan = s.bucketSpan
	}
	return s.repo.ListRuntimeRollups(ctx, strings.TrimSpace(projectID), strings.TrimSpace(eventType), strings.TrimSpace(source), bucketSpan, limit)
}

// Hub exposes the SSE/WebSocket hub for runtime consumers.
func (s *TelemetryService) Hub() *ws.Hub {
	if s == nil {
		return nil
	}
	return s.hub
}

func (s *TelemetryService) flushStale(ctx context.Context) {
	cutoff := s.now().Add(-s.bucketSpan)
	rollups := s.aggregator.flushBefore(cutoff)
	s.persistRollups(ctx, rollups)
}

func (s *TelemetryService) flushAll(ctx context.Context) {
	rollups := s.aggregator.flushAll()
	s.persistRollups(ctx, rollups)
}

func (s *TelemetryService) persistRollups(ctx context.Context, rollups []domain.RuntimeMetricRollup) {
	if len(rollups) == 0 {
		return
	}
	if err := s.repo.UpsertRuntimeRollups(ctx, rollups); err != nil {
		if s.logger != nil {
			s.logger.Warn("failed to persist runtime rollups", "error", err, "count", len(rollups))
		}
	}
}

func (s *TelemetryService) broadcast(event domain.RuntimeEvent) {
	if s.hub == nil {
		return
	}
	payload, err := MarshalRuntimeEvent(event)
	if err != nil {
		if s.logger != nil {
			s.logger.Warn("failed to marshal runtime event", "error", err)
		}
		return
	}
	s.hub.Broadcast(event.ProjectID, payload)
}

// MarshalRuntimeEvent encodes a runtime event for SSE/WebSocket clients.
func MarshalRuntimeEvent(event domain.RuntimeEvent) ([]byte, error) {
	var metadata any
	if len(event.Metadata) > 0 {
		metadata = json.RawMessage(event.Metadata)
	}
	payload := map[string]any{
		"id":          event.ID,
		"project_id":  event.ProjectID,
		"source":      event.Source,
		"event_type":  event.EventType,
		"level":       event.Level,
		"message":     event.Message,
		"method":      event.Method,
		"path":        event.Path,
		"status_code": event.StatusCode,
		"latency_ms":  event.LatencyMS,
		"bytes_in":    event.BytesIn,
		"bytes_out":   event.BytesOut,
		"metadata":    metadata,
		"occurred_at": event.OccurredAt.UTC().Format(time.RFC3339Nano),
		"ingested_at": event.IngestedAt.UTC().Format(time.RFC3339Nano),
	}
	return json.Marshal(payload)
}