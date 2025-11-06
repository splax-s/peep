package httpx

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/splax/localvercel/api/internal/domain"
	"github.com/splax/localvercel/api/internal/repository"
	"github.com/splax/localvercel/api/internal/service/auth"
	runtimeSvc "github.com/splax/localvercel/api/internal/service/runtime"
	"github.com/splax/localvercel/pkg/config"
	jwtpkg "github.com/splax/localvercel/pkg/jwt"
)

func TestMarshalRuntimeRollups(t *testing.T) {
	now := time.Date(2025, time.November, 5, 12, 34, 56, 0, time.UTC)
	span := 2 * time.Minute
	p50 := 42.0
	avg := 55.5

	rollups := []domain.RuntimeMetricRollup{{
		ProjectID:   "proj-1",
		BucketStart: now,
		BucketSpan:  span,
		Source:      "runtime",
		EventType:   "http_request",
		Count:       10,
		ErrorCount:  2,
		P50MS:       &p50,
		AvgMS:       &avg,
		UpdatedAt:   now.Add(time.Minute),
	}}

	payload := marshalRuntimeRollups(rollups)
	if len(payload) != 1 {
		t.Fatalf("expected one payload item, got %d", len(payload))
	}

	item := payload[0]
	if project, ok := item["project_id"].(string); !ok || project != "proj-1" {
		t.Fatalf("unexpected project_id: %v", item["project_id"])
	}
	expectedStart := rollups[0].BucketStart.UTC().Format(time.RFC3339Nano)
	if start, ok := item["bucket_start"].(string); !ok || start != expectedStart {
		t.Fatalf("unexpected bucket_start: %v", item["bucket_start"])
	}
	if spanSeconds, ok := item["bucket_span_seconds"].(int); !ok || spanSeconds != int(span/time.Second) {
		t.Fatalf("unexpected bucket_span_seconds: %v", item["bucket_span_seconds"])
	}
	ptr, ok := item["p50_ms"].(*float64)
	if !ok || ptr == nil || *ptr != p50 {
		t.Fatalf("unexpected p50_ms: %v", item["p50_ms"])
	}
	avgPtr, ok := item["avg_ms"].(*float64)
	if !ok || avgPtr == nil || *avgPtr != avg {
		t.Fatalf("unexpected avg_ms: %v", item["avg_ms"])
	}
}

func TestHandleRuntimeEventsGetReturnsEvents(t *testing.T) {
	repo := &runtimeRepoStub{}
	base := time.Date(2025, time.January, 2, 15, 4, 5, 0, time.UTC)
	status := 201
	latency := 42.5
	repo.listEventsResp = []domain.RuntimeEvent{{
		ID:         1,
		ProjectID:  "proj-123",
		Source:     "api",
		EventType:  "http_request",
		Level:      "info",
		Message:    "request processed",
		Method:     "GET",
		Path:       "/",
		StatusCode: &status,
		LatencyMS:  &latency,
		OccurredAt: base,
		IngestedAt: base.Add(500 * time.Millisecond),
	}}

	limiter := newRateLimiterStub()
	reset := time.Unix(1_950_000_000, 0)
	limiter.allowFn = func(key string, limit int, window time.Duration) rateDecision {
		return rateDecision{allowed: true, count: 3, windowEnd: reset}
	}

	router, token := setupRouterWithRuntime(t, repo, limiter)

	req := httptest.NewRequest(http.MethodGet, "/runtime/events", nil)
	req.URL.RawQuery = "project_id=%20proj-123%20&event_type=%20http_request%20&limit=17&offset=2"
	req.Header.Set("Authorization", "Bearer "+token)

	rr := httptest.NewRecorder()
	router.handleRuntimeEvents(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rr.Code)
	}
	if got := rr.Header().Get("X-RateLimit-Limit"); got != "240" {
		t.Fatalf("unexpected rate limit header: %q", got)
	}
	if got := rr.Header().Get("X-RateLimit-Remaining"); got != "237" {
		t.Fatalf("unexpected rate remaining header: %q", got)
	}
	if got := rr.Header().Get("X-RateLimit-Reset"); got != "1950000000" {
		t.Fatalf("unexpected rate reset header: %q", got)
	}

	limiter.mu.Lock()
	if len(limiter.calls) != 1 {
		limiter.mu.Unlock()
		t.Fatalf("expected limiter called once, got %d", len(limiter.calls))
	}
	call := limiter.calls[0]
	limiter.mu.Unlock()
	if call.key != "user:user-123" {
		t.Fatalf("unexpected limiter key %q", call.key)
	}
	if call.limit != rateLimitRuntimeRead {
		t.Fatalf("unexpected limiter limit %d", call.limit)
	}

	repo.mu.Lock()
	args := repo.lastListEvents
	repo.mu.Unlock()
	if args.projectID != "proj-123" {
		t.Fatalf("expected project id trimmed, got %q", args.projectID)
	}
	if args.eventType != "http_request" {
		t.Fatalf("expected event type trimmed, got %q", args.eventType)
	}
	if args.limit != 17 || args.offset != 2 {
		t.Fatalf("unexpected pagination args: limit=%d offset=%d", args.limit, args.offset)
	}

	var payload []map[string]any
	if err := json.NewDecoder(rr.Body).Decode(&payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if len(payload) != 1 {
		t.Fatalf("expected one event, got %d", len(payload))
	}
	event := payload[0]
	if event["project_id"] != "proj-123" {
		t.Fatalf("unexpected project_id: %v", event["project_id"])
	}
	if statusCode, ok := event["status_code"].(float64); !ok || int(statusCode) != status {
		t.Fatalf("unexpected status_code: %v", event["status_code"])
	}
	if occurred, ok := event["occurred_at"].(string); !ok || occurred != base.Format(time.RFC3339Nano) {
		t.Fatalf("unexpected occurred_at: %v", event["occurred_at"])
	}
}

func TestHandleRuntimeEventsPostIngestsEvent(t *testing.T) {
	repo := &runtimeRepoStub{}
	limiter := newRateLimiterStub()
	reset := time.Unix(1_950_000_100, 0)
	limiter.allowFn = func(key string, limit int, window time.Duration) rateDecision {
		return rateDecision{allowed: true, count: 10, windowEnd: reset}
	}

	router, _ := setupRouterWithRuntime(t, repo, limiter)
	router.builderToken = "builder-secret"

	body := `{
		"project_id": " proj-123 ",
		"source": " runtime ",
		"event_type": "http_request",
		"message": "ok",
		"method": "GET",
		"path": "/",
		"status_code": 202,
		"latency_ms": 12.5,
		"bytes_in": 321,
		"bytes_out": 654,
		"occurred_at": "2025-01-03T04:05:06Z"
	}`
	req := httptest.NewRequest(http.MethodPost, "/runtime/events", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Builder-Token", "builder-secret")

	rr := httptest.NewRecorder()
	router.handleRuntimeEvents(rr, req)

	if rr.Code != http.StatusAccepted {
		t.Fatalf("expected status 202, got %d", rr.Code)
	}
	if rr.Header().Get("X-RateLimit-Limit") != "1000" {
		t.Fatalf("unexpected rate limit header %q", rr.Header().Get("X-RateLimit-Limit"))
	}
	if rr.Header().Get("X-RateLimit-Remaining") != "990" {
		t.Fatalf("unexpected rate remaining header %q", rr.Header().Get("X-RateLimit-Remaining"))
	}
	if rr.Header().Get("X-RateLimit-Reset") != "1950000100" {
		t.Fatalf("unexpected rate reset header %q", rr.Header().Get("X-RateLimit-Reset"))
	}

	limiter.mu.Lock()
	if len(limiter.calls) != 1 {
		limiter.mu.Unlock()
		t.Fatalf("expected limiter called once, got %d", len(limiter.calls))
	}
	call := limiter.calls[0]
	limiter.mu.Unlock()
	if call.key != "runtime:proj-123" {
		t.Fatalf("unexpected limiter key %q", call.key)
	}
	if call.limit != rateLimitRuntimeWrite {
		t.Fatalf("unexpected limiter limit %d", call.limit)
	}

	events := repo.insertedEvents()
	if len(events) != 1 {
		t.Fatalf("expected one event persisted, got %d", len(events))
	}
	event := events[0]
	if event.ProjectID != "proj-123" {
		t.Fatalf("expected project id trimmed, got %q", event.ProjectID)
	}
	if event.Source != "runtime" {
		t.Fatalf("expected source trimmed, got %q", event.Source)
	}
	if event.EventType != "http_request" {
		t.Fatalf("unexpected event type %q", event.EventType)
	}
	if event.Message != "ok" {
		t.Fatalf("unexpected message %q", event.Message)
	}
	if event.Method != "GET" || event.Path != "/" {
		t.Fatalf("unexpected method/path %q %q", event.Method, event.Path)
	}
	if event.StatusCode == nil || *event.StatusCode != 202 {
		t.Fatalf("unexpected status code %v", event.StatusCode)
	}
	if event.LatencyMS == nil || *event.LatencyMS != 12.5 {
		t.Fatalf("unexpected latency %v", event.LatencyMS)
	}
	if event.BytesIn == nil || *event.BytesIn != 321 {
		t.Fatalf("unexpected bytes_in %v", event.BytesIn)
	}
	if event.BytesOut == nil || *event.BytesOut != 654 {
		t.Fatalf("unexpected bytes_out %v", event.BytesOut)
	}
	expectedTime := time.Date(2025, time.January, 3, 4, 5, 6, 0, time.UTC)
	if !event.OccurredAt.Equal(expectedTime) {
		t.Fatalf("unexpected occurred_at %v", event.OccurredAt)
	}
	if event.IngestedAt.IsZero() {
		t.Fatalf("expected ingested_at set")
	}
}

func TestHandleRuntimeEventsPostRequiresBuilderToken(t *testing.T) {
	repo := &runtimeRepoStub{}
	limiter := newRateLimiterStub()
	router, _ := setupRouterWithRuntime(t, repo, limiter)
	router.builderToken = "builder-secret"

	req := httptest.NewRequest(http.MethodPost, "/runtime/events", strings.NewReader(`{"project_id":"proj-1"}`))
	req.Header.Set("Content-Type", "application/json")

	rr := httptest.NewRecorder()
	router.handleRuntimeEvents(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected status 401, got %d", rr.Code)
	}
	if rr.Header().Get("X-RateLimit-Limit") != "" {
		t.Fatalf("did not expect rate limit headers on auth failure")
	}
	if events := repo.insertedEvents(); len(events) != 0 {
		t.Fatalf("expected no events persisted, got %d", len(events))
	}
	limiter.mu.Lock()
	calls := len(limiter.calls)
	limiter.mu.Unlock()
	if calls != 0 {
		t.Fatalf("expected limiter not called, got %d", calls)
	}
}

func TestHandleRuntimeEventsGetRequiresProjectID(t *testing.T) {
	repo := &runtimeRepoStub{}
	limiter := newRateLimiterStub()
	router, token := setupRouterWithRuntime(t, repo, limiter)

	req := httptest.NewRequest(http.MethodGet, "/runtime/events", nil)
	req.Header.Set("Authorization", "Bearer "+token)

	rr := httptest.NewRecorder()
	router.handleRuntimeEvents(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d", rr.Code)
	}
	limiter.mu.Lock()
	calls := len(limiter.calls)
	limiter.mu.Unlock()
	if calls != 0 {
		t.Fatalf("expected limiter not called, got %d", calls)
	}
}

func TestHandleRuntimeEventsGetRateLimited(t *testing.T) {
	repo := &runtimeRepoStub{}
	limiter := newRateLimiterStub()
	reset := time.Unix(1_960_000_000, 0)
	limiter.allowFn = func(key string, limit int, window time.Duration) rateDecision {
		return rateDecision{allowed: false, count: rateLimitRuntimeRead, windowEnd: reset}
	}
	router, token := setupRouterWithRuntime(t, repo, limiter)

	req := httptest.NewRequest(http.MethodGet, "/runtime/events", nil)
	req.URL.RawQuery = "project_id=proj-123"
	req.Header.Set("Authorization", "Bearer "+token)

	rr := httptest.NewRecorder()
	router.handleRuntimeEvents(rr, req)

	if rr.Code != http.StatusTooManyRequests {
		t.Fatalf("expected status 429, got %d", rr.Code)
	}
	if rr.Header().Get("X-RateLimit-Limit") != "240" {
		t.Fatalf("unexpected limit header %q", rr.Header().Get("X-RateLimit-Limit"))
	}
	if rr.Header().Get("X-RateLimit-Remaining") != "0" {
		t.Fatalf("unexpected remaining header %q", rr.Header().Get("X-RateLimit-Remaining"))
	}
	if rr.Header().Get("X-RateLimit-Reset") != "1960000000" {
		t.Fatalf("unexpected reset header %q", rr.Header().Get("X-RateLimit-Reset"))
	}
	var payload map[string]any
	if err := json.NewDecoder(rr.Body).Decode(&payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if payload["error"] == nil {
		t.Fatalf("expected error payload")
	}
	repo.mu.Lock()
	args := repo.lastListEvents
	repo.mu.Unlock()
	if args.projectID != "" {
		t.Fatalf("expected repository not invoked, got %q", args.projectID)
	}
	limiter.mu.Lock()
	if len(limiter.calls) != 1 {
		limiter.mu.Unlock()
		t.Fatalf("expected limiter called once, got %d", len(limiter.calls))
	}
	call := limiter.calls[0]
	limiter.mu.Unlock()
	if call.key != "user:user-123" {
		t.Fatalf("unexpected limiter key %q", call.key)
	}
}

func TestHandleRuntimeEventsPostRateLimited(t *testing.T) {
	repo := &runtimeRepoStub{}
	limiter := newRateLimiterStub()
	reset := time.Unix(1_960_000_100, 0)
	limiter.allowFn = func(key string, limit int, window time.Duration) rateDecision {
		return rateDecision{allowed: false, count: rateLimitRuntimeWrite, windowEnd: reset}
	}
	router, _ := setupRouterWithRuntime(t, repo, limiter)
	router.builderToken = "builder-secret"

	body := `{"project_id":"proj-123","event_type":"http_request","message":"ok"}`
	req := httptest.NewRequest(http.MethodPost, "/runtime/events", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Builder-Token", "builder-secret")

	rr := httptest.NewRecorder()
	router.handleRuntimeEvents(rr, req)

	if rr.Code != http.StatusTooManyRequests {
		t.Fatalf("expected status 429, got %d", rr.Code)
	}
	if rr.Header().Get("X-RateLimit-Limit") != "1000" {
		t.Fatalf("unexpected limit header %q", rr.Header().Get("X-RateLimit-Limit"))
	}
	if rr.Header().Get("X-RateLimit-Remaining") != "0" {
		t.Fatalf("unexpected remaining header %q", rr.Header().Get("X-RateLimit-Remaining"))
	}
	if rr.Header().Get("X-RateLimit-Reset") != "1960000100" {
		t.Fatalf("unexpected reset header %q", rr.Header().Get("X-RateLimit-Reset"))
	}
	if events := repo.insertedEvents(); len(events) != 0 {
		t.Fatalf("expected no events persisted, got %d", len(events))
	}
}

func TestHandleRuntimeMetricsGetReturnsRollups(t *testing.T) {
	repo := &runtimeRepoStub{}
	base := time.Date(2025, time.January, 4, 10, 0, 0, 0, time.UTC)
	p95 := 90.0
	avg := 45.0
	repo.listRollupsResp = []domain.RuntimeMetricRollup{{
		ProjectID:   "proj-123",
		BucketStart: base,
		BucketSpan:  2 * time.Minute,
		Source:      "runtime",
		EventType:   "http_request",
		Count:       8,
		ErrorCount:  1,
		P95MS:       &p95,
		AvgMS:       &avg,
		UpdatedAt:   base.Add(time.Minute),
	}}

	limiter := newRateLimiterStub()
	reset := time.Unix(1_950_000_200, 0)
	limiter.allowFn = func(key string, limit int, window time.Duration) rateDecision {
		return rateDecision{allowed: true, count: 7, windowEnd: reset}
	}

	router, token := setupRouterWithRuntime(t, repo, limiter)

	req := httptest.NewRequest(http.MethodGet, "/runtime/metrics", nil)
	req.URL.RawQuery = "project_id=proj-123&event_type=http_request&source=runtime&bucket_span=120&limit=3"
	req.Header.Set("Authorization", "Bearer "+token)

	rr := httptest.NewRecorder()
	router.handleRuntimeMetrics(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rr.Code)
	}
	if rr.Header().Get("X-RateLimit-Limit") != "240" {
		t.Fatalf("unexpected rate limit header %q", rr.Header().Get("X-RateLimit-Limit"))
	}
	if rr.Header().Get("X-RateLimit-Remaining") != "233" {
		t.Fatalf("unexpected rate remaining header %q", rr.Header().Get("X-RateLimit-Remaining"))
	}
	if rr.Header().Get("X-RateLimit-Reset") != "1950000200" {
		t.Fatalf("unexpected rate reset header %q", rr.Header().Get("X-RateLimit-Reset"))
	}

	repo.mu.Lock()
	rollupArgs := repo.lastListRollups
	repo.mu.Unlock()
	if rollupArgs.projectID != "proj-123" {
		t.Fatalf("unexpected project id %q", rollupArgs.projectID)
	}
	if rollupArgs.eventType != "http_request" {
		t.Fatalf("unexpected event type %q", rollupArgs.eventType)
	}
	if rollupArgs.source != "runtime" {
		t.Fatalf("unexpected source %q", rollupArgs.source)
	}
	if rollupArgs.bucketSpan != 120*time.Second {
		t.Fatalf("unexpected bucket span %v", rollupArgs.bucketSpan)
	}
	if rollupArgs.limit != 3 {
		t.Fatalf("unexpected limit %d", rollupArgs.limit)
	}

	var payload []map[string]any
	if err := json.NewDecoder(rr.Body).Decode(&payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if len(payload) != 1 {
		t.Fatalf("expected one rollup, got %d", len(payload))
	}
	rollup := payload[0]
	if rollup["project_id"] != "proj-123" {
		t.Fatalf("unexpected project_id: %v", rollup["project_id"])
	}
	if count, ok := rollup["count"].(float64); !ok || int(count) != 8 {
		t.Fatalf("unexpected count: %v", rollup["count"])
	}
	if bucket, ok := rollup["bucket_span_seconds"].(float64); !ok || int(bucket) != 120 {
		t.Fatalf("unexpected bucket_span_seconds: %v", rollup["bucket_span_seconds"])
	}
	if p, ok := rollup["p95_ms"].(float64); !ok || p != p95 {
		t.Fatalf("unexpected p95_ms: %v", rollup["p95_ms"])
	}
	if updated, ok := rollup["updated_at"].(string); !ok || updated != repo.listRollupsResp[0].UpdatedAt.UTC().Format(time.RFC3339Nano) {
		t.Fatalf("unexpected updated_at: %v", rollup["updated_at"])
	}
}

func TestHandleRuntimeMetricsInvalidBucketSpan(t *testing.T) {
	repo := &runtimeRepoStub{}
	limiter := newRateLimiterStub()
	reset := time.Unix(1_950_000_500, 0)
	limiter.allowFn = func(key string, limit int, window time.Duration) rateDecision {
		return rateDecision{allowed: true, count: 4, windowEnd: reset}
	}
	router, token := setupRouterWithRuntime(t, repo, limiter)

	req := httptest.NewRequest(http.MethodGet, "/runtime/metrics", nil)
	req.URL.RawQuery = "project_id=proj-123&bucket_span=invalid"
	req.Header.Set("Authorization", "Bearer "+token)

	rr := httptest.NewRecorder()
	router.handleRuntimeMetrics(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d", rr.Code)
	}
	if rr.Header().Get("X-RateLimit-Limit") != "240" {
		t.Fatalf("expected rate limit header present")
	}
	if rr.Header().Get("X-RateLimit-Remaining") != "236" {
		t.Fatalf("unexpected remaining header %q", rr.Header().Get("X-RateLimit-Remaining"))
	}
	if rr.Header().Get("X-RateLimit-Reset") != "1950000500" {
		t.Fatalf("unexpected reset header %q", rr.Header().Get("X-RateLimit-Reset"))
	}
	repo.mu.Lock()
	calls := repo.lastListRollups
	repo.mu.Unlock()
	if calls.projectID != "" {
		t.Fatalf("expected repository not invoked")
	}
}

func TestHandleRuntimeMetricsPropagatesRepositoryError(t *testing.T) {
	repo := &runtimeRepoStub{}
	repo.listRollupsErr = assertError("boom")
	limiter := newRateLimiterStub()
	limiter.allowFn = func(key string, limit int, window time.Duration) rateDecision {
		return rateDecision{allowed: true, count: 1, windowEnd: time.Unix(1_950_000_600, 0)}
	}
	router, token := setupRouterWithRuntime(t, repo, limiter)

	req := httptest.NewRequest(http.MethodGet, "/runtime/metrics", nil)
	req.URL.RawQuery = "project_id=proj-123"
	req.Header.Set("Authorization", "Bearer "+token)

	rr := httptest.NewRecorder()
	router.handleRuntimeMetrics(rr, req)

	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("expected status 500, got %d", rr.Code)
	}
	var payload map[string]any
	if err := json.NewDecoder(rr.Body).Decode(&payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if payload["error"] == nil {
		t.Fatalf("expected error payload")
	}
}

func TestHandleRuntimeStreamEmitsHeartbeatAndBackfill(t *testing.T) {
	repo := &runtimeRepoStub{}
	base := time.Date(2025, time.January, 6, 9, 0, 0, 0, time.UTC)
	repo.listEventsResp = []domain.RuntimeEvent{{
		ProjectID:  "proj-123",
		Source:     "runtime",
		EventType:  "http_request",
		Message:    "ok",
		OccurredAt: base,
		IngestedAt: base.Add(50 * time.Millisecond),
	}}
	limiter := newRateLimiterStub()
	router, token := setupRouterWithRuntime(t, repo, limiter)

	req := httptest.NewRequest(http.MethodGet, "/runtime/stream?project_id=proj-123", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	ctx := context.WithValue(req.Context(), contextKeyAuth, authInfo{UserID: "user-123"})
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	req = req.WithContext(ctx)

	recorder := newStreamRecorder()
	done := make(chan struct{})
	go func() {
		router.handleRuntimeStream(recorder, req)
		close(done)
	}()

	waitFor(t, 2*time.Second, func() bool {
		return strings.Contains(recorder.body(), ": ping")
	})
	waitFor(t, 2*time.Second, func() bool {
		return strings.Contains(recorder.body(), "data: ")
	})

	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("runtime stream handler did not exit after context cancel")
	}

	if ct := recorder.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("unexpected content type %q", ct)
	}
	if recorder.Header().Get("Cache-Control") != "no-cache" {
		t.Fatalf("expected no-cache header")
	}
	if recorder.flushCount() == 0 {
		t.Fatalf("expected flusher to be invoked")
	}

	payloads, err := extractSSEPayloads(recorder.body())
	if err != nil {
		t.Fatalf("extract sse payloads: %v", err)
	}
	if len(payloads) == 0 {
		t.Fatalf("expected at least one SSE payload")
	}
	last := payloads[len(payloads)-1]
	if last["project_id"] != "proj-123" {
		t.Fatalf("unexpected project_id in payload: %v", last["project_id"])
	}
	repo.mu.Lock()
	args := repo.lastListEvents
	repo.mu.Unlock()
	if args.projectID != "proj-123" {
		t.Fatalf("expected project id trimmed, got %q", args.projectID)
	}
	if args.limit != runtimeStreamBackfillLimit {
		t.Fatalf("unexpected backfill limit %d", args.limit)
	}
	if args.offset != 0 {
		t.Fatalf("unexpected offset %d", args.offset)
	}
}

func TestHandleRuntimeStreamRequiresAuthContext(t *testing.T) {
	repo := &runtimeRepoStub{}
	limiter := newRateLimiterStub()
	router, token := setupRouterWithRuntime(t, repo, limiter)

	req := httptest.NewRequest(http.MethodGet, "/runtime/stream?project_id=proj-123", nil)
	req.Header.Set("Authorization", "Bearer "+token)

	recorder := newStreamRecorder()
	router.handleRuntimeStream(recorder, req)

	if recorder.statusCode() != http.StatusInternalServerError {
		t.Fatalf("expected status 500, got %d", recorder.statusCode())
	}
	if recorder.flushCount() != 0 {
		t.Fatalf("expected no flushes on auth failure")
	}
	if msg := parseError(t, recorder.body()); msg != "authorization context missing" {
		t.Fatalf("unexpected error message %q", msg)
	}
}

func TestHandleRuntimeStreamRequiresProjectID(t *testing.T) {
	repo := &runtimeRepoStub{}
	limiter := newRateLimiterStub()
	router, token := setupRouterWithRuntime(t, repo, limiter)

	req := httptest.NewRequest(http.MethodGet, "/runtime/stream", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	req = req.WithContext(context.WithValue(req.Context(), contextKeyAuth, authInfo{UserID: "user-123"}))

	recorder := newStreamRecorder()
	router.handleRuntimeStream(recorder, req)

	if recorder.statusCode() != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d", recorder.statusCode())
	}
	if recorder.flushCount() != 0 {
		t.Fatalf("expected no flushes when project ID missing")
	}
	if msg := parseError(t, recorder.body()); msg != "project_id query parameter required" {
		t.Fatalf("unexpected error message %q", msg)
	}
}

func TestHandleRuntimeStreamRequiresFlusher(t *testing.T) {
	repo := &runtimeRepoStub{}
	limiter := newRateLimiterStub()
	router, token := setupRouterWithRuntime(t, repo, limiter)

	req := httptest.NewRequest(http.MethodGet, "/runtime/stream?project_id=proj-123", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	req = req.WithContext(context.WithValue(req.Context(), contextKeyAuth, authInfo{UserID: "user-123"}))

	w := newNoFlushRecorder()
	router.handleRuntimeStream(w, req)

	if w.statusCode() != http.StatusInternalServerError {
		t.Fatalf("expected status 500, got %d", w.statusCode())
	}
	if msg := parseError(t, w.body()); msg != "streaming not supported" {
		t.Fatalf("unexpected error message %q", msg)
	}
}

func TestHandleRuntimeStreamUnavailableHub(t *testing.T) {
	repo := &runtimeRepoStub{}
	limiter := newRateLimiterStub()
	router, token := setupRouterWithRuntime(t, repo, limiter)
	router.runtime = &runtimeSvc.TelemetryService{}

	req := httptest.NewRequest(http.MethodGet, "/runtime/stream?project_id=proj-123", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	req = req.WithContext(context.WithValue(req.Context(), contextKeyAuth, authInfo{UserID: "user-123"}))

	recorder := newStreamRecorder()
	router.handleRuntimeStream(recorder, req)

	if recorder.statusCode() != http.StatusInternalServerError {
		t.Fatalf("expected status 500, got %d", recorder.statusCode())
	}
	if msg := parseError(t, recorder.body()); msg != "runtime stream unavailable" {
		t.Fatalf("unexpected error message %q", msg)
	}
}

type rateLimiterStub struct {
	mu      sync.Mutex
	calls   []rateLimitCall
	allowFn func(key string, limit int, window time.Duration) rateDecision
}

type rateLimitCall struct {
	key    string
	limit  int
	window time.Duration
}

func newRateLimiterStub() *rateLimiterStub {
	return &rateLimiterStub{}
}

func (rl *rateLimiterStub) Allow(key string, limit int, window time.Duration) rateDecision {
	rl.mu.Lock()
	rl.calls = append(rl.calls, rateLimitCall{key: key, limit: limit, window: window})
	fn := rl.allowFn
	rl.mu.Unlock()
	if fn != nil {
		return fn(key, limit, window)
	}
	return rateDecision{allowed: true, count: 1, windowEnd: time.Now().Add(window)}
}

func (rl *rateLimiterStub) Close() {}

type runtimeRepoStub struct {
	mu              sync.Mutex
	insertErr       error
	listEventsResp  []domain.RuntimeEvent
	listEventsErr   error
	listRollupsResp []domain.RuntimeMetricRollup
	listRollupsErr  error
	storedEvents    []*domain.RuntimeEvent
	lastListEvents  struct {
		projectID string
		eventType string
		limit     int
		offset    int
	}
	lastListRollups struct {
		projectID  string
		eventType  string
		source     string
		bucketSpan time.Duration
		limit      int
	}
}

func (r *runtimeRepoStub) InsertRuntimeEvent(_ context.Context, event *domain.RuntimeEvent) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.insertErr != nil {
		return r.insertErr
	}
	copy := *event
	r.storedEvents = append(r.storedEvents, &copy)
	return nil
}

func (r *runtimeRepoStub) ListRuntimeEvents(_ context.Context, projectID, eventType string, limit, offset int) ([]domain.RuntimeEvent, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.lastListEvents = struct {
		projectID string
		eventType string
		limit     int
		offset    int
	}{projectID: projectID, eventType: eventType, limit: limit, offset: offset}
	if r.listEventsErr != nil {
		return nil, r.listEventsErr
	}
	out := make([]domain.RuntimeEvent, len(r.listEventsResp))
	copy(out, r.listEventsResp)
	return out, nil
}

func (r *runtimeRepoStub) UpsertRuntimeRollups(_ context.Context, rollups []domain.RuntimeMetricRollup) error {
	return nil
}

func (r *runtimeRepoStub) ListRuntimeRollups(_ context.Context, projectID, eventType, source string, bucketSpan time.Duration, limit int) ([]domain.RuntimeMetricRollup, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.lastListRollups = struct {
		projectID  string
		eventType  string
		source     string
		bucketSpan time.Duration
		limit      int
	}{projectID: projectID, eventType: eventType, source: source, bucketSpan: bucketSpan, limit: limit}
	if r.listRollupsErr != nil {
		return nil, r.listRollupsErr
	}
	out := make([]domain.RuntimeMetricRollup, len(r.listRollupsResp))
	copy(out, r.listRollupsResp)
	return out, nil
}

func (r *runtimeRepoStub) insertedEvents() []domain.RuntimeEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]domain.RuntimeEvent, len(r.storedEvents))
	for i, evt := range r.storedEvents {
		out[i] = *evt
	}
	return out
}

type userRepoStub struct {
	mu    sync.Mutex
	users map[string]*domain.User
}

func newUserRepoStub() *userRepoStub {
	return &userRepoStub{users: make(map[string]*domain.User)}
}

func (u *userRepoStub) CreateUser(_ context.Context, user *domain.User) error {
	u.mu.Lock()
	defer u.mu.Unlock()
	copy := *user
	u.users[user.ID] = &copy
	return nil
}

func (u *userRepoStub) GetUserByEmail(_ context.Context, email string) (*domain.User, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	for _, user := range u.users {
		if user.Email == email {
			copy := *user
			return &copy, nil
		}
	}
	return nil, repository.ErrNotFound
}

func (u *userRepoStub) GetUserByID(_ context.Context, id string) (*domain.User, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if user, ok := u.users[id]; ok {
		copy := *user
		return &copy, nil
	}
	copy := &domain.User{ID: id}
	u.users[id] = copy
	dupe := *copy
	return &dupe, nil
}

func setupRouterWithRuntime(t *testing.T, repo *runtimeRepoStub, limiter *rateLimiterStub) (*Router, string) {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	userRepo := newUserRepoStub()
	userRepo.users["user-123"] = &domain.User{ID: "user-123", Email: "user@example.com"}

	cfg := config.APIConfig{
		JWTSecret:       "test-secret",
		AccessTokenTTL:  time.Hour,
		RefreshTokenTTL: 24 * time.Hour,
	}
	authSvc := auth.New(userRepo, logger, cfg)

	router := &Router{
		logger:       logger,
		auth:         authSvc,
		runtime:      runtimeSvc.NewTelemetryService(repo, nil, logger, time.Minute, 30*time.Second),
		limiter:      limiter,
		builderToken: "builder-secret",
	}

	token, err := jwtpkg.GenerateToken("user-123", "team-xyz", cfg.JWTSecret, time.Hour)
	if err != nil {
		t.Fatalf("generate token: %v", err)
	}
	return router, token
}

type assertError string

func (e assertError) Error() string { return string(e) }

type streamRecorder struct {
	mu     sync.Mutex
	header http.Header
	status int
	buf    bytes.Buffer
	flush  int
}

func newStreamRecorder() *streamRecorder {
	return &streamRecorder{header: make(http.Header)}
}

func (s *streamRecorder) Header() http.Header {
	return s.header
}

func (s *streamRecorder) Write(b []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.status == 0 {
		s.status = http.StatusOK
	}
	return s.buf.Write(b)
}

func (s *streamRecorder) WriteHeader(status int) {
	s.mu.Lock()
	s.status = status
	s.mu.Unlock()
}

func (s *streamRecorder) Flush() {
	s.mu.Lock()
	s.flush++
	s.mu.Unlock()
}

func (s *streamRecorder) body() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}

func (s *streamRecorder) flushCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.flush
}

func (s *streamRecorder) statusCode() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.status == 0 {
		return http.StatusOK
	}
	return s.status
}

func waitFor(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("condition not met within %s", timeout)
}

func extractSSEPayloads(body string) ([]map[string]any, error) {
	lines := strings.Split(body, "\n")
	var payloads []map[string]any
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "data: ") {
			raw := strings.TrimPrefix(line, "data: ")
			var payload map[string]any
			if err := json.Unmarshal([]byte(raw), &payload); err != nil {
				return nil, err
			}
			payloads = append(payloads, payload)
		}
	}
	return payloads, nil
}

type noFlushRecorder struct {
	header http.Header
	status int
	buf    bytes.Buffer
}

func newNoFlushRecorder() *noFlushRecorder {
	return &noFlushRecorder{header: make(http.Header)}
}

func (r *noFlushRecorder) Header() http.Header {
	return r.header
}

func (r *noFlushRecorder) Write(b []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	return r.buf.Write(b)
}

func (r *noFlushRecorder) WriteHeader(status int) {
	r.status = status
}

func (r *noFlushRecorder) body() string {
	return r.buf.String()
}

func (r *noFlushRecorder) statusCode() int {
	if r.status == 0 {
		return http.StatusOK
	}
	return r.status
}

func parseError(t *testing.T, body string) string {
	t.Helper()
	var payload map[string]any
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		t.Fatalf("decode error payload: %v", err)
	}
	v, _ := payload["error"].(string)
	return v
}
