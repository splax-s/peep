package httpx

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

const rateLimiterSweepInterval = 5 * time.Minute

type RateLimiter interface {
	Allow(key string, limit int, window time.Duration) rateDecision
	Close()
}

type rateDecision struct {
	allowed   bool
	count     int
	windowEnd time.Time
}

type memoryRateLimiter struct {
	mu      sync.Mutex
	entries map[string]rateState
	stopCh  chan struct{}
	once    sync.Once
}

type rateState struct {
	count     int
	windowEnd time.Time
}

func NewMemoryRateLimiter() RateLimiter {
	rl := &memoryRateLimiter{
		entries: make(map[string]rateState),
		stopCh:  make(chan struct{}),
	}
	go rl.sweepLoop()
	return rl
}

func (rl *memoryRateLimiter) Allow(key string, limit int, window time.Duration) rateDecision {
	if limit <= 0 {
		return rateDecision{allowed: true}
	}
	if window <= 0 {
		window = time.Minute
	}
	now := time.Now()
	rl.mu.Lock()
	defer rl.mu.Unlock()

	state, ok := rl.entries[key]
	if !ok || now.After(state.windowEnd) {
		state = rateState{count: 1, windowEnd: now.Add(window)}
		rl.entries[key] = state
		return rateDecision{allowed: true, count: state.count, windowEnd: state.windowEnd}
	}
	if state.count >= limit {
		return rateDecision{allowed: false, count: state.count, windowEnd: state.windowEnd}
	}
	state.count++
	rl.entries[key] = state
	return rateDecision{allowed: true, count: state.count, windowEnd: state.windowEnd}
}

func (rl *memoryRateLimiter) sweepLoop() {
	ticker := time.NewTicker(rateLimiterSweepInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			rl.cleanup(time.Now())
		case <-rl.stopCh:
			return
		}
	}
}

func (rl *memoryRateLimiter) cleanup(now time.Time) {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	for key, state := range rl.entries {
		if now.After(state.windowEnd) {
			delete(rl.entries, key)
		}
	}
}

func (rl *memoryRateLimiter) Close() {
	rl.once.Do(func() {
		close(rl.stopCh)
	})
}

func (r *Router) withRateLimit(route string, limit int, window time.Duration, keyFn func(*http.Request) string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		if limit <= 0 || r.limiter == nil {
			next(w, req)
			return
		}
		key := keyFn(req)
		if key == "" {
			key = rateLimitKeyIP(req)
		}
		decision := r.limiter.Allow(key, limit, window)
		r.applyRateHeaders(w, limit, decision)
		if !decision.allowed {
			label := route
			if label == "" {
				label = req.URL.Path
			}
			r.recordRateLimitHit(label, rateMetricKey(key))
			writeError(w, http.StatusTooManyRequests, "rate limit exceeded")
			return
		}
		next(w, req)
	}
}

func (r *Router) handlerAuthRate(route string, limit int, window time.Duration, next http.HandlerFunc) http.HandlerFunc {
	return r.requireAuth(r.withRateLimit(route, limit, window, r.rateLimitKeyUser, next))
}

func (r *Router) rateLimitKeyUser(req *http.Request) string {
	if info, ok := authInfoFromContext(req.Context()); ok && info.UserID != "" {
		return "user:" + info.UserID
	}
	return ""
}

func rateLimitKeyIP(req *http.Request) string {
	host, _, err := net.SplitHostPort(req.RemoteAddr)
	if err != nil {
		host = req.RemoteAddr
	}
	if host == "" {
		host = "unknown"
	}
	return "ip:" + host
}

func rateMetricKey(key string) string {
	if key == "" {
		return "unknown"
	}
	if idx := strings.IndexRune(key, ':'); idx > 0 {
		return key[:idx]
	}
	if strings.HasPrefix(key, "ip:") {
		return "ip"
	}
	return key
}
