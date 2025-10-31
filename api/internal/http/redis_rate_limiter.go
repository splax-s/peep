package httpx

import (
	"context"
	"time"

	"log/slog"

	redis "github.com/redis/go-redis/v9"
)

type redisRateLimiter struct {
	client  *redis.Client
	logger  *slog.Logger
	prefix  string
	timeout time.Duration
}

// NewRedisRateLimiter constructs a Redis backed rate limiter.
func NewRedisRateLimiter(addr, password string, db int, logger *slog.Logger) (RateLimiter, error) {
	opts := &redis.Options{Addr: addr, Password: password, DB: db}
	client := redis.NewClient(opts)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		client.Close()
		return nil, err
	}
	return &redisRateLimiter{
		client:  client,
		logger:  logger,
		prefix:  "peep:ratelimit:",
		timeout: 250 * time.Millisecond,
	}, nil
}

func (rl *redisRateLimiter) Allow(key string, limit int, window time.Duration) rateDecision {
	if limit <= 0 {
		return rateDecision{allowed: true}
	}
	if window <= 0 {
		window = time.Minute
	}
	ctx, cancel := context.WithTimeout(context.Background(), rl.timeout)
	defer cancel()

	redisKey := rl.prefix + key
	counter, err := rl.client.Incr(ctx, redisKey).Result()
	if err != nil {
		rl.logRedisError("incr", err)
		return rateDecision{allowed: true}
	}
	if counter == 1 {
		if err := rl.client.Expire(ctx, redisKey, window).Err(); err != nil {
			rl.logRedisError("expire", err)
		}
	}
	ttl, err := rl.client.TTL(ctx, redisKey).Result()
	if err != nil || ttl <= 0 {
		ttl = window
	}
	allowed := int(counter) <= limit
	return rateDecision{
		allowed:   allowed,
		count:     int(counter),
		windowEnd: time.Now().Add(ttl),
	}
}

func (rl *redisRateLimiter) Close() {
	if rl.client != nil {
		_ = rl.client.Close()
	}
}

func (rl *redisRateLimiter) logRedisError(op string, err error) {
	if rl.logger == nil {
		return
	}
	rl.logger.Error("redis rate limiter error", "op", op, "error", err)
}
