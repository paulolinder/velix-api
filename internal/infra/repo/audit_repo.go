package repo

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// AuditLog represents a single audit log entry.
type AuditLog struct {
	ID           string         `json:"id"`
	WorkspaceID  string         `json:"workspace_id"`
	ActorType    string         `json:"actor_type"`
	ActorID      string         `json:"actor_id,omitempty"`
	Action       string         `json:"action"`
	ResourceType string         `json:"resource_type,omitempty"`
	ResourceID   string         `json:"resource_id,omitempty"`
	Metadata     map[string]any `json:"metadata,omitempty"`
	IPAddress    string         `json:"ip_address,omitempty"`
	CreatedAt    time.Time      `json:"created_at"`
}

// AuditRepo provides read access to the audit_logs table.
type AuditRepo struct {
	db *pgxpool.Pool
}

// NewAuditRepo creates a new audit repository.
func NewAuditRepo(db *pgxpool.Pool) *AuditRepo {
	return &AuditRepo{db: db}
}

// List returns audit log entries for a workspace with optional filters.
func (r *AuditRepo) List(ctx context.Context, workspaceID, action, resourceType string, limit, offset int) ([]*AuditLog, error) {
	q := `SELECT id, workspace_id, actor_type, COALESCE(actor_id::text,''),
	             action, COALESCE(resource_type,''), COALESCE(resource_id::text,''),
	             metadata, COALESCE(ip_address::text,''), created_at
	      FROM audit_logs
	      WHERE workspace_id = $1
	        AND ($2 = '' OR action = $2)
	        AND ($3 = '' OR resource_type = $3)
	      ORDER BY created_at DESC
	      LIMIT $4 OFFSET $5`

	rows, err := r.db.Query(ctx, q, workspaceID, action, resourceType, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("list audit logs: %w", err)
	}
	defer rows.Close()

	var results []*AuditLog
	for rows.Next() {
		l := &AuditLog{}
		if err := rows.Scan(&l.ID, &l.WorkspaceID, &l.ActorType, &l.ActorID,
			&l.Action, &l.ResourceType, &l.ResourceID,
			&l.Metadata, &l.IPAddress, &l.CreatedAt); err != nil {
			return nil, err
		}
		results = append(results, l)
	}
	return results, rows.Err()
}
