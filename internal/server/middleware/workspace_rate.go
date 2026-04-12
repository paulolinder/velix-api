package middleware

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/redis/go-redis/v9"

	apipkg "velix/internal/api"
)

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
