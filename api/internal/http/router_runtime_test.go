package httpx

import (
	"testing"
	"time"

	"github.com/splax/localvercel/api/internal/domain"
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
