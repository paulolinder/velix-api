package server

import (
	"context"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

// HealthChecker holds live infrastructure clients for readiness checks.
type HealthChecker struct {
	db  *pgxpool.Pool
	rdb *redis.Client
}

// NewHealthChecker creates a HealthChecker with the given clients.
func NewHealthChecker(db *pgxpool.Pool, rdb *redis.Client) *HealthChecker {
	return &HealthChecker{db: db, rdb: rdb}
}

// ReadyHandler is a readiness probe — returns 200 only when DB and Redis are reachable.
func (h *HealthChecker) ReadyHandler(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()

	checks := map[string]string{}
	allOK := true

	// PostgreSQL ping.
	if err := h.db.Ping(ctx); err != nil {
		checks["postgres"] = "unhealthy: " + err.Error()
		allOK = false
	} else {
		checks["postgres"] = "ok"
	}

	// Redis ping.
	if err := h.rdb.Ping(ctx).Err(); err != nil {
		checks["redis"] = "unhealthy: " + err.Error()
		allOK = false
	} else {
		checks["redis"] = "ok"
	}

	status := "ready"
	code := http.StatusOK
	if !allOK {
		status = "degraded"
		code = http.StatusServiceUnavailable
	}

	writeJSON(w, code, map[string]any{
		"status":    status,
		"checks":    checks,
		"timestamp": time.Now().UTC(),
	})
}
