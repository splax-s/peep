package runtime

import (
	"context"
	"encoding/json"
	"math"
	"sync"
	"testing"
	"time"

	"github.com/splax/localvercel/api/internal/domain"
	"github.com/splax/localvercel/api/internal/ws"
)

func TestTelemetryServiceIngestPersistsAndBroadcasts(t *testing.T) {
	repo := &stubRuntimeRepo{}
	hub := ws.NewHub()
	svc := NewTelemetryService(repo, hub, nil, time.Minute, 30*time.Second)
	base := time.Date(2025, time.November, 5, 12, 34, 56, 0, time.UTC)
	svc.now = func() time.Time { return base }

	subscriber := newTestSubscriber()
	hub.Register("proj-123", subscriber)

	latency := 123.456
	status := 503
	err := svc.Ingest(context.Background(), domain.RuntimeEvent{
		ProjectID:  " proj-123 ",
		Source:     "api",
		EventType:  "http_request",
		Message:    "request completed",
		StatusCode: &status,
		LatencyMS:  &latency,
	})
	if err != nil {
		t.Fatalf("ingest: %v", err)
	}

	repoEvents := repo.eventsSnapshot()
	if len(repoEvents) != 1 {
		t.Fatalf("expected 1 event persisted, got %d", len(repoEvents))
	}
	persisted := repoEvents[0]
	if persisted.ProjectID != "proj-123" {
		t.Fatalf("expected project id trimmed, got %q", persisted.ProjectID)
	}
	if persisted.OccurredAt != base {
		t.Fatalf("expected occurred_at to default to now, got %v", persisted.OccurredAt)
	}
	if persisted.IngestedAt != base {
		t.Fatalf("expected ingested_at to default to now, got %v", persisted.IngestedAt)
	}

	select {
	case payload := <-subscriber.ch:
		var msg map[string]any
		if err := json.Unmarshal(payload, &msg); err != nil {
			t.Fatalf("unmarshal broadcast: %v", err)
		}
		if msg["project_id"] != "proj-123" {
			t.Fatalf("expected broadcast project id proj-123, got %v", msg["project_id"])
		}
		if v, ok := msg["status_code"].(float64); !ok || int(v) != status {
			t.Fatalf("expected status_code %d, got %v", status, msg["status_code"])
		}
		if msg["occurred_at"] != base.Format(time.RFC3339Nano) {
			t.Fatalf("unexpected occurred_at %v", msg["occurred_at"])
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("expected runtime event broadcast")
	}

	svc.flushAll(context.Background())

	rollups := repo.rollupsSnapshot()
	if len(rollups) != 1 {
		t.Fatalf("expected 1 rollup persisted, got %d", len(rollups))
	}
	rollup := rollups[0]
	if rollup.ProjectID != "proj-123" {
		t.Fatalf("unexpected rollup project id %q", rollup.ProjectID)
	}
	if rollup.Count != 1 {
		t.Fatalf("expected rollup count 1, got %d", rollup.Count)
	}
	if rollup.ErrorCount != 1 {
		t.Fatalf("expected rollup error count 1, got %d", rollup.ErrorCount)
	}
	if rollup.BucketStart != base.Truncate(time.Minute) {
		t.Fatalf("unexpected bucket start %v", rollup.BucketStart)
	}
	if rollup.BucketSpan != time.Minute {
		t.Fatalf("expected bucket span 1m, got %v", rollup.BucketSpan)
	}
	assertFloatPtrEqual(t, rollup.AvgMS, latency, "avg_ms")
	assertFloatPtrEqual(t, rollup.P50MS, latency, "p50_ms")
	assertFloatPtrEqual(t, rollup.P90MS, latency, "p90_ms")
	assertFloatPtrEqual(t, rollup.P95MS, latency, "p95_ms")
	assertFloatPtrEqual(t, rollup.P99MS, latency, "p99_ms")
	assertFloatPtrEqual(t, rollup.MaxMS, latency, "max_ms")
	if rollup.UpdatedAt.IsZero() {
		t.Fatalf("expected updated_at to be populated")
	}
}

func TestTelemetryServiceIngestRequiresProjectID(t *testing.T) {
	repo := &stubRuntimeRepo{}
	svc := NewTelemetryService(repo, nil, nil, 0, 0)
	if err := svc.Ingest(context.Background(), domain.RuntimeEvent{}); err == nil {
		t.Fatal("expected validation error when project_id missing")
	}
	if len(repo.eventsSnapshot()) != 0 {
		t.Fatalf("expected no events persisted when validation fails")
	}
}

func TestTelemetryServiceFlushAllPersistsRollups(t *testing.T) {
	repo := &stubRuntimeRepo{}
	svc := NewTelemetryService(repo, nil, nil, 2*time.Minute, 30*time.Second)
	base := time.Date(2025, time.November, 5, 8, 15, 0, 0, time.UTC)
	svc.now = func() time.Time { return base }

	latency := 42.0
	svc.aggregator.add(domain.RuntimeEvent{
		ProjectID:  "proj-rollup",
		Source:     "runtime",
		EventType:  "http_request",
		LatencyMS:  &latency,
		OccurredAt: base,
	})

	svc.flushAll(context.Background())

	rollups := repo.rollupsSnapshot()
	if len(rollups) != 1 {
		t.Fatalf("expected 1 rollup persisted, got %d", len(rollups))
	}
	rollup := rollups[0]
	if rollup.ProjectID != "proj-rollup" {
		t.Fatalf("unexpected project id %q", rollup.ProjectID)
	}
	if rollup.BucketSpan != 2*time.Minute {
		t.Fatalf("expected bucket span 2m, got %v", rollup.BucketSpan)
	}
	assertFloatPtrEqual(t, rollup.AvgMS, latency, "avg_ms")
	if rollup.UpdatedAt.IsZero() {
		t.Fatalf("expected updated_at to be populated")
	}
}

type stubRuntimeRepo struct {
	mu      sync.Mutex
	events  []*domain.RuntimeEvent
	rollups []domain.RuntimeMetricRollup
}

func (r *stubRuntimeRepo) InsertRuntimeEvent(_ context.Context, event *domain.RuntimeEvent) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	copy := *event
	r.events = append(r.events, &copy)
	return nil
}

func (r *stubRuntimeRepo) ListRuntimeEvents(context.Context, string, string, int, int) ([]domain.RuntimeEvent, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	result := make([]domain.RuntimeEvent, len(r.events))
	for i, evt := range r.events {
		result[i] = *evt
	}
	return result, nil
}

func (r *stubRuntimeRepo) UpsertRuntimeRollups(_ context.Context, rollups []domain.RuntimeMetricRollup) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, rollup := range rollups {
		copy := rollup
		r.rollups = append(r.rollups, copy)
	}
	return nil
}

func (r *stubRuntimeRepo) ListRuntimeRollups(context.Context, string, string, string, time.Duration, int) ([]domain.RuntimeMetricRollup, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	result := make([]domain.RuntimeMetricRollup, len(r.rollups))
	copy(result, r.rollups)
	return result, nil
}

func (r *stubRuntimeRepo) eventsSnapshot() []*domain.RuntimeEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	snapshot := make([]*domain.RuntimeEvent, len(r.events))
	for i, evt := range r.events {
		copy := *evt
		snapshot[i] = &copy
	}
	return snapshot
}

func (r *stubRuntimeRepo) rollupsSnapshot() []domain.RuntimeMetricRollup {
	r.mu.Lock()
	defer r.mu.Unlock()
	snapshot := make([]domain.RuntimeMetricRollup, len(r.rollups))
	copy(snapshot, r.rollups)
	return snapshot
}

type testSubscriber struct {
	ch chan []byte
}

func newTestSubscriber() *testSubscriber {
	return &testSubscriber{ch: make(chan []byte, 1)}
}

func (s *testSubscriber) Send(payload []byte) error {
	select {
	case s.ch <- append([]byte(nil), payload...):
	default:
	}
	return nil
}

func (s *testSubscriber) Close() {}

func assertFloatPtrEqual(t *testing.T, value *float64, expected float64, field string) {
	t.Helper()
	if value == nil {
		t.Fatalf("expected %s to be set", field)
	}
	if math.Abs(*value-expected) > 1e-6 {
		t.Fatalf("expected %s %.3f, got %.3f", field, expected, *value)
	}
}
