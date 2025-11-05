package runtime

import (
	"math"
	"math/rand"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/splax/localvercel/api/internal/domain"
)

type bucketKey struct {
	projectID string
	source    string
	eventType string
	start     time.Time
}

type rollupBucket struct {
	count        int64
	errorCount   int64
	latencies    []float64
	latencyCount int64
	latencySum   float64
	latencyMax   float64
	hasLatency   bool
}

type rollupAggregator struct {
	mu          sync.Mutex
	span        time.Duration
	maxSamples  int
	buckets     map[bucketKey]*rollupBucket
	now         func() time.Time
	random      *rand.Rand
}

const defaultRollupSamples = 512

func newRollupAggregator(span time.Duration, maxSamples int, now func() time.Time) *rollupAggregator {
	if span <= 0 {
		span = time.Minute
	}
	if maxSamples <= 0 {
		maxSamples = defaultRollupSamples
	}
	if now == nil {
		now = time.Now
	}
	return &rollupAggregator{
		span:       span,
		maxSamples: maxSamples,
		buckets:    make(map[bucketKey]*rollupBucket),
		now:        now,
		random:     rand.New(rand.NewSource(now().UnixNano())),
	}
}

func (a *rollupAggregator) add(event domain.RuntimeEvent) {
	if a == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()

	start := event.OccurredAt.Truncate(a.span)
	key := bucketKey{
		projectID: event.ProjectID,
		source:    strings.TrimSpace(event.Source),
		eventType: strings.TrimSpace(event.EventType),
		start:     start,
	}
	if key.source == "" {
		key.source = "runtime"
	}
	if key.eventType == "" {
		key.eventType = "http_request"
	}
	bucket := a.buckets[key]
	if bucket == nil {
		bucket = &rollupBucket{}
		a.buckets[key] = bucket
	}
	bucket.count++
	if isRuntimeError(event) {
		bucket.errorCount++
	}
	if event.LatencyMS != nil {
		lat := *event.LatencyMS
		bucket.latencyCount++
		bucket.latencySum += lat
		if !bucket.hasLatency || lat > bucket.latencyMax {
			bucket.latencyMax = lat
			bucket.hasLatency = true
		}
		if len(bucket.latencies) < a.maxSamples {
			bucket.latencies = append(bucket.latencies, lat)
		} else if a.maxSamples > 0 {
			idx := a.random.Intn(a.maxSamples)
			bucket.latencies[idx] = lat
		}
	}
}

func (a *rollupAggregator) flushBefore(cutoff time.Time) []domain.RuntimeMetricRollup {
	if a == nil {
		return nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()

	if len(a.buckets) == 0 {
		return nil
	}
	rollups := make([]domain.RuntimeMetricRollup, 0)
	for key, bucket := range a.buckets {
		if key.start.Add(a.span).After(cutoff) {
			continue
		}
		rollups = append(rollups, bucket.toRollup(key, a.span, a.now()))
		delete(a.buckets, key)
	}
	return rollups
}

func (a *rollupAggregator) flushAll() []domain.RuntimeMetricRollup {
	if a == nil {
		return nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()

	if len(a.buckets) == 0 {
		return nil
	}
	now := a.now()
	rollups := make([]domain.RuntimeMetricRollup, 0, len(a.buckets))
	for key, bucket := range a.buckets {
		rollups = append(rollups, bucket.toRollup(key, a.span, now))
		delete(a.buckets, key)
	}
	return rollups
}

func (b *rollupBucket) toRollup(key bucketKey, span time.Duration, now time.Time) domain.RuntimeMetricRollup {
	r := domain.RuntimeMetricRollup{
		ProjectID:   key.projectID,
		BucketStart: key.start,
		BucketSpan:  span,
		Source:      key.source,
		EventType:   key.eventType,
		Count:       b.count,
		ErrorCount:  b.errorCount,
		UpdatedAt:   now,
	}
	if b.latencyCount > 0 {
		avg := b.latencySum / float64(b.latencyCount)
		r.AvgMS = &avg
	}
	if b.hasLatency {
		max := b.latencyMax
		r.MaxMS = &max
	}
	if len(b.latencies) > 0 {
		sorted := append([]float64(nil), b.latencies...)
		sort.Float64s(sorted)
		p50 := percentile(sorted, 0.50)
		p90 := percentile(sorted, 0.90)
		p95 := percentile(sorted, 0.95)
		p99 := percentile(sorted, 0.99)
		r.P50MS = &p50
		r.P90MS = &p90
		r.P95MS = &p95
		r.P99MS = &p99
	}
	return r
}

func percentile(values []float64, p float64) float64 {
	if len(values) == 0 {
		return 0
	}
	if p <= 0 {
		return values[0]
	}
	if p >= 1 {
		return values[len(values)-1]
	}
	pos := p * float64(len(values)-1)
	lower := int(math.Floor(pos))
	upper := int(math.Ceil(pos))
	if lower == upper {
		return values[lower]
	}
	weight := pos - float64(lower)
	return values[lower]*(1-weight) + values[upper]*weight
}

func isRuntimeError(event domain.RuntimeEvent) bool {
	if strings.EqualFold(event.Level, "error") || strings.EqualFold(event.Level, "fatal") {
		return true
	}
	if event.StatusCode != nil && *event.StatusCode >= 500 {
		return true
	}
	return false
}