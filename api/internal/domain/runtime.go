package domain

import "time"

// RuntimeEvent captures live telemetry for a project environment.
type RuntimeEvent struct {
	ID         int64
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
	Metadata   []byte
	OccurredAt time.Time
	IngestedAt time.Time
}

// RuntimeMetricRollup stores aggregated latency and throughput statistics for a time bucket.
type RuntimeMetricRollup struct {
	ProjectID   string
	BucketStart time.Time
	BucketSpan  time.Duration
	Source      string
	EventType   string
	Count       int64
	ErrorCount  int64
	P50MS       *float64
	P90MS       *float64
	P95MS       *float64
	P99MS       *float64
	MaxMS       *float64
	AvgMS       *float64
	UpdatedAt   time.Time
}
