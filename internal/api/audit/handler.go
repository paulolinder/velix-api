// Package audit contains HTTP handlers for audit log endpoints.
package audit

import (
	"net/http"

	apipkg "velix/internal/api"
	"velix/internal/infra/repo"
)

// Handler serves audit log queries.
type Handler struct {
	repo *repo.AuditRepo
}

// NewHandler creates an audit handler.
func NewHandler(r *repo.AuditRepo) *Handler {
	return &Handler{repo: r}
}

// List handles GET /v1/audit-logs?action=&resource_type=&limit=50&offset=0
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	wsID := apipkg.WorkspaceID(r)
	pg := apipkg.ParsePagination(r)
	action := r.URL.Query().Get("action")
	resourceType := r.URL.Query().Get("resource_type")

	logs, err := h.repo.List(r.Context(), wsID, action, resourceType, pg.Limit, pg.Offset)
	if err != nil {
		apipkg.LogAndFail(w, r, err, "list audit logs")
		return
	}
	apipkg.WriteJSON(w, r, http.StatusOK, logs)
}
