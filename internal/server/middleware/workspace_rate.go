package middleware

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"

	apipkg "velix/internal/api"
)

// inMemoryCounter is a lightweight fallback rate limiter when Redis is unavailable.
type inMemoryCounter struct {
	mu       sync.Mutex
	counters map[string]*memEntry
}

type memEntry struct {
	count   int64
	resetAt time.Time
}

func newInMemoryCounter() *inMemoryCounter {
	c := &inMemoryCounter{counters: make(map[string]*memEntry)}
	// Cleanup stale entries every 60 seconds to prevent memory leak.
	go func() {
		for {
			time.Sleep(60 * time.Second)
			c.mu.Lock()
			now := time.Now()
			for k, e := range c.counters {
				if now.After(e.resetAt) {
					delete(c.counters, k)
				}
			}
			c.mu.Unlock()
		}
	}()
	return c
}

func (c *inMemoryCounter) incr(key string, limit int64) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	e, ok := c.counters[key]
	if !ok || now.After(e.resetAt) {
		c.counters[key] = &memEntry{count: 1, resetAt: now.Add(time.Second)}
		return true
	}
	e.count++
	return e.count <= limit
}

// WorkspaceRateLimit returns middleware that enforces per-workspace rate limits
// using Redis as a distributed counter (fixed-window, 60 req/s).
// Falls back to allowing all requests if Redis is nil or unavailable.
func WorkspaceRateLimit(rdb *redis.Client) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			claims := ClaimsFrom(r.Context())
			if claims == nil || claims.WorkspaceID == "" {
				next.ServeHTTP(w, r)
				return
			}
			if !allowWorkspace(r.Context(), rdb, claims.WorkspaceID, 60) {
				apipkg.WriteError(w, r, apipkg.NewError(apipkg.ErrCodeRateLimit, "workspace rate limit exceeded"))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// LoginRateLimit returns middleware that limits login/register attempts to
// maxAttempts per IP per window (e.g. 10 per 5 minutes). This prevents
// brute-force attacks on the authentication endpoints.
// Falls back to allowing all requests when Redis is unavailable.
func LoginRateLimit(rdb *redis.Client) func(http.Handler) http.Handler {
	const (
		maxAttempts = 10             // max requests per window
		window      = 5 * time.Minute // sliding window size
	)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ip := realIP(r)
			if rdb != nil && ip != "" {
				bucket := time.Now().Truncate(window).Unix()
				key := fmt.Sprintf("rl:login:%s:%d", ip, bucket)

				ctx, cancel := context.WithTimeout(r.Context(), 20*time.Millisecond)
				defer cancel()

				pipe := rdb.Pipeline()
				incr := pipe.Incr(ctx, key)
				pipe.Expire(ctx, key, window+10*time.Second)
				if _, err := pipe.Exec(ctx); err == nil && incr.Val() > maxAttempts {
					w.Header().Set("Retry-After", fmt.Sprintf("%d", int(window.Seconds())))
					apipkg.WriteError(w, r, apipkg.NewError(apipkg.ErrCodeRateLimit,
						"Too many login attempts. Please wait before trying again."))
					return
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}

// realIP returns the client IP from r.RemoteAddr.
// chi's RealIP middleware (applied globally before this) has already resolved
// X-Forwarded-For and updated RemoteAddr. We intentionally do NOT re-read the
// raw XFF header here to prevent rate-limit bypass via header spoofing
// (see GHSA-c2r5-cfqr-c553, GHSA-hm36-ffrh-c77c).
func realIP(r *http.Request) string {
	host, _, _ := strings.Cut(r.RemoteAddr, ":")
	return host
}

// IPRateLimit returns middleware that enforces a per-IP rate limit using Redis as
// a distributed counter. This works correctly across multiple API process replicas,
// unlike the in-process token-bucket limiter which is per-process only.
//
// limit = max requests per second per IP. Falls back to allow all if Redis is nil.
func IPRateLimit(rdb *redis.Client, limit int) func(http.Handler) http.Handler {
	fallback := newInMemoryCounter() // used when Redis is nil or fails

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ip := realIP(r)
			if ip == "" {
				next.ServeHTTP(w, r)
				return
			}

			if rdb == nil {
				// No Redis — use in-memory fallback (per-process only).
				if !fallback.incr("ip:"+ip, int64(limit)) {
					apipkg.WriteError(w, r, apipkg.NewError(apipkg.ErrCodeRateLimit, "Too many requests"))
					return
				}
				next.ServeHTTP(w, r)
				return
			}

			second := time.Now().Unix()
			key := fmt.Sprintf("rl:ip:%s:%d", ip, second)

			ctx, cancel := context.WithTimeout(r.Context(), 15*time.Millisecond)
			defer cancel()

			pipe := rdb.Pipeline()
			incr := pipe.Incr(ctx, key)
			pipe.Expire(ctx, key, 2*time.Second)
			if _, err := pipe.Exec(ctx); err != nil {
				// Redis failed — fall back to in-memory instead of allowing all.
				if !fallback.incr("ip:"+ip, int64(limit)) {
					apipkg.WriteError(w, r, apipkg.NewError(apipkg.ErrCodeRateLimit, "Too many requests"))
					return
				}
				next.ServeHTTP(w, r)
				return
			}
			if incr.Val() > int64(limit) {
				apipkg.WriteError(w, r, apipkg.NewError(apipkg.ErrCodeRateLimit, "Too many requests"))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// allowWorkspace checks a 1-second fixed-window counter in Redis.
// Returns true (allow) on Redis errors to avoid cascading failures.
func allowWorkspace(ctx context.Context, rdb *redis.Client, workspaceID string, rps int) bool {
	if rdb == nil {
		return true
	}

	second := time.Now().Unix()
	key := fmt.Sprintf("rl:ws:%s:%d", workspaceID, second)

	// Use a short deadline — rate limiting must never slow down the hot path.
	ctx, cancel := context.WithTimeout(ctx, 15*time.Millisecond)
	defer cancel()

	pipe := rdb.Pipeline()
	incr := pipe.Incr(ctx, key)
	pipe.Expire(ctx, key, 2*time.Second) // keep the key for 2 windows to avoid races
	if _, err := pipe.Exec(ctx); err != nil {
		return true // fail open
	}

	return incr.Val() <= int64(rps)
}
