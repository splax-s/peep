package runtime

import (
	"testing"
	"time"

	"github.com/splax/localvercel/api/internal/domain"
)

func TestRollupAggregatorFlushBefore(t *testing.T) {
	now := time.Date(2025, time.November, 5, 12, 0, 0, 0, time.UTC)
	agg := newRollupAggregator(time.Minute, 8, func() time.Time { return now })

	latencyFast := 50.0
	latencySlow := 150.0
	statusError := 502

	agg.add(domain.RuntimeEvent{
		ProjectID:  "proj-1",
		Source:     "api",
		EventType:  "http_request",
		Level:      "info",
		LatencyMS:  &latencyFast,
		OccurredAt: now.Add(-30 * time.Second),
	})

	agg.add(domain.RuntimeEvent{
		ProjectID:  "proj-1",
		Source:     "api",
		EventType:  "http_request",
		Level:      "info",
		StatusCode: &statusError,
		LatencyMS:  &latencySlow,
		OccurredAt: now.Add(-20 * time.Second),
	})

	rollups := agg.flushBefore(now.Add(time.Minute))
	if len(rollups) != 1 {
		t.Fatalf("expected a single rollup, got %d", len(rollups))
	}

	rollup := rollups[0]
	if rollup.ProjectID != "proj-1" {
		t.Fatalf("unexpected project id %s", rollup.ProjectID)
	}
	if rollup.Count != 2 {
		t.Fatalf("expected count 2, got %d", rollup.Count)
	}
	if rollup.ErrorCount != 1 {
		t.Fatalf("expected error count 1, got %d", rollup.ErrorCount)
	}
	expectedAvg := (latencyFast + latencySlow) / 2
	if rollup.AvgMS == nil || *rollup.AvgMS != expectedAvg {
		t.Fatalf("expected average latency %.1f, got %v", expectedAvg, rollup.AvgMS)
	}
	if rollup.MaxMS == nil || *rollup.MaxMS != latencySlow {
		t.Fatalf("expected max latency %.1f, got %v", latencySlow, rollup.MaxMS)
	}
	expectedP50 := expectedAvg
	if rollup.P50MS == nil || *rollup.P50MS != expectedP50 {
		t.Fatalf("expected p50 %.1f, got %v", expectedP50, rollup.P50MS)
	}
	if rollup.BucketSpan != time.Minute {
		t.Fatalf("expected bucket span %s, got %s", time.Minute, rollup.BucketSpan)
	}
}

func TestIsRuntimeError(t *testing.T) {
	if !isRuntimeError(domain.RuntimeEvent{Level: "ERROR"}) {
		t.Fatalf("expected uppercase level to be treated as error")
	}
	status := 501
	if !isRuntimeError(domain.RuntimeEvent{StatusCode: &status}) {
		t.Fatalf("expected 5xx status to be treated as error")
	}
	if isRuntimeError(domain.RuntimeEvent{Level: "info"}) {
		t.Fatalf("did not expect info level to be treated as error")
	}
}
