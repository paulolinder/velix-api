// Package admin provides the /v1/admin API endpoints for the admin panel.
package admin

import (
	"net/http"
	"time"

	apipkg "velix/internal/api"
	"velix/internal/domain/auth"
	"velix/internal/domain/instance"
	"velix/internal/engine"
	"velix/internal/metrics"
	"velix/internal/server/middleware"
)

// StatsResponse is the payload for GET /v1/admin/stats.
type StatsResponse struct {
	TotalInstances     int `json:"total_instances"`
	ConnectedInstances int `json:"connected_instances"`
	TotalAPIKeys       int `json:"total_api_keys"`
	ActiveAPIKeys      int `json:"active_api_keys"`

	// Process-level metrics
	UptimeSeconds   int64 `json:"uptime_seconds"`
	MessagesSent    int64 `json:"messages_sent"`
	MessagesInbound int64 `json:"messages_inbound"`
	WSClients       int64 `json:"ws_clients"`
}

// Stats returns a handler for GET /v1/admin/stats.
func Stats(
	instanceSvc *instance.Service,
	authSvc *auth.Service,
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claims := middleware.ClaimsFrom(r.Context())
		if claims == nil {
			apipkg.WriteError(w, r, apipkg.ErrUnauthorized)
			return
		}

		ctx := r.Context()
		var stats StatsResponse

		if instances, err := instanceSvc.List(ctx, claims.WorkspaceID, 1000, 0); err == nil {
			stats.TotalInstances = len(instances)
			for _, inst := range instances {
				if inst.Status == engine.StatusConnected {
					stats.ConnectedInstances++
				}
			}
		}

		if keys, err := authSvc.ListAPIKeys(ctx, claims.WorkspaceID); err == nil {
			stats.TotalAPIKeys = len(keys)
			for _, k := range keys {
				if k.IsValid() {
					stats.ActiveAPIKeys++
				}
			}
		}

		stats.UptimeSeconds = int64(time.Since(metrics.M.Started()).Seconds())
		stats.MessagesSent = metrics.M.MessagesSent.Load()
		stats.MessagesInbound = metrics.M.MessagesInbound.Load()
		stats.WSClients = metrics.M.WSClients.Load()

		apipkg.WriteJSON(w, r, http.StatusOK, stats)
	}
}
