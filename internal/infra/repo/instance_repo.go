// Package repo contains PostgreSQL repository implementations.
package repo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"velix/internal/domain/instance"
	"velix/internal/engine"
)

// InstanceRepo is the PostgreSQL implementation of instance.Repository.
type InstanceRepo struct {
	db *pgxpool.Pool
}

// NewInstanceRepo creates a new PostgreSQL-backed instance repository.
func NewInstanceRepo(db *pgxpool.Pool) *InstanceRepo {
	return &InstanceRepo{db: db}
}

// Create inserts a new instance row and returns the created record.
func (r *InstanceRepo) Create(ctx context.Context, inst *instance.Instance) (*instance.Instance, error) {
	const q = `
		INSERT INTO instances (workspace_id, name, phone_number, status, proxy_url)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, workspace_id, name, COALESCE(phone_number,''), status,
		          COALESCE(jid,''), COALESCE(platform,''), COALESCE(business_name,''),
		          COALESCE(proxy_url,''), settings, last_connected_at, created_at, updated_at`

	row := r.db.QueryRow(ctx, q,
		inst.WorkspaceID,
		inst.Name,
		nullString(inst.PhoneNumber),
		engine.StatusDisconnected,
		nullString(inst.ProxyURL),
	)

	return scanInstance(row)
}

// GetByID retrieves a single instance by its UUID.
func (r *InstanceRepo) GetByID(ctx context.Context, id string) (*instance.Instance, error) {
	const q = `
		SELECT id, workspace_id, name, COALESCE(phone_number,''), status,
		       COALESCE(jid,''), COALESCE(platform,''), COALESCE(business_name,''),
		       COALESCE(proxy_url,''), settings, last_connected_at, created_at, updated_at
		FROM instances WHERE id = $1`

	inst, err := scanInstance(r.db.QueryRow(ctx, q, id))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, instance.ErrNotFound
		}
		return nil, err
	}
	return inst, nil
}

// ListByWorkspace returns instances for a workspace with pagination.
func (r *InstanceRepo) ListByWorkspace(ctx context.Context, workspaceID string, limit, offset int) ([]*instance.Instance, error) {
	const q = `
		SELECT id, workspace_id, name, COALESCE(phone_number,''), status,
		       COALESCE(jid,''), COALESCE(platform,''), COALESCE(business_name,''),
		       COALESCE(proxy_url,''), settings, last_connected_at, created_at, updated_at
		FROM instances WHERE workspace_id = $1
		ORDER BY created_at ASC
		LIMIT $2 OFFSET $3`

	rows, err := r.db.Query(ctx, q, workspaceID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("list instances: %w", err)
	}
	defer rows.Close()

	var results []*instance.Instance
	for rows.Next() {
		inst, err := scanInstance(rows)
		if err != nil {
			return nil, err
		}
		results = append(results, inst)
	}
	return results, rows.Err()
}

// ListAll returns every instance across all workspaces (startup restoration).
func (r *InstanceRepo) ListAll(ctx context.Context) ([]*instance.Instance, error) {
	const q = `
		SELECT id, workspace_id, name, COALESCE(phone_number,''), status,
		       COALESCE(jid,''), COALESCE(platform,''), COALESCE(business_name,''),
		       COALESCE(proxy_url,''), settings, last_connected_at, created_at, updated_at
		FROM instances ORDER BY created_at ASC`

	rows, err := r.db.Query(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("list all instances: %w", err)
	}
	defer rows.Close()

	var results []*instance.Instance
	for rows.Next() {
		inst, err := scanInstance(rows)
		if err != nil {
			return nil, err
		}
		results = append(results, inst)
	}
	return results, rows.Err()
}

// UpdateStatus updates the instance status and sets last_connected_at when transitioning to connected.
func (r *InstanceRepo) UpdateStatus(ctx context.Context, id string, status instance.Status) error {
	var err error
	if status == engine.StatusConnected {
		const q = `UPDATE instances SET status=$1, last_connected_at=NOW(), updated_at=NOW() WHERE id=$2`
		_, err = r.db.Exec(ctx, q, status, id)
	} else {
		const q = `UPDATE instances SET status=$1, updated_at=NOW() WHERE id=$2`
		_, err = r.db.Exec(ctx, q, status, id)
	}
	if err != nil {
		return fmt.Errorf("update status: %w", err)
	}
	return nil
}

// UpdateAfterPair persists the JID, platform, and business name received after pairing.
func (r *InstanceRepo) UpdateAfterPair(ctx context.Context, id, jid, platform, businessName string) error {
	const q = `
		UPDATE instances
		SET jid=$1, platform=$2, business_name=$3, status=$4, updated_at=NOW()
		WHERE id=$5`
	_, err := r.db.Exec(ctx, q, jid, platform, businessName, engine.StatusConnected, id)
	if err != nil {
		return fmt.Errorf("update after pair: %w", err)
	}
	return nil
}

// UpdateSettings persists the JSONB settings column.
func (r *InstanceRepo) UpdateSettings(ctx context.Context, id string, s *instance.Settings) error {
	data, err := json.Marshal(s)
	if err != nil {
		return fmt.Errorf("marshal settings: %w", err)
	}
	const q = `UPDATE instances SET settings = $1, updated_at = NOW() WHERE id = $2`
	_, err = r.db.Exec(ctx, q, data, id)
	if err != nil {
		return fmt.Errorf("update settings: %w", err)
	}
	return nil
}

// Delete removes an instance row.
func (r *InstanceRepo) Delete(ctx context.Context, id string) error {
	const q = `DELETE FROM instances WHERE id=$1`
	_, err := r.db.Exec(ctx, q, id)
	if err != nil {
		return fmt.Errorf("delete instance: %w", err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// rowScanner is satisfied by both pgx.Row and pgx.Rows.
type rowScanner interface {
	Scan(dest ...any) error
}

func scanInstance(row rowScanner) (*instance.Instance, error) {
	inst := &instance.Instance{}
	var lastConn *time.Time
	var settingsJSON []byte
	err := row.Scan(
		&inst.ID,
		&inst.WorkspaceID,
		&inst.Name,
		&inst.PhoneNumber,
		&inst.Status,
		&inst.JID,
		&inst.Platform,
		&inst.BusinessName,
		&inst.ProxyURL,
		&settingsJSON,
		&lastConn,
		&inst.CreatedAt,
		&inst.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	inst.LastConnectedAt = lastConn
	if len(settingsJSON) > 0 {
		_ = json.Unmarshal(settingsJSON, &inst.Settings)
	}
	return inst, nil
}

func nullString(s string) any {
	if s == "" {
		return nil
	}
	return s
}
