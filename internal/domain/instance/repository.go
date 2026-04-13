package instance

import "context"

// Repository defines the persistence contract for WhatsApp instances.
type Repository interface {
	// Create inserts a new instance and returns it with generated ID and timestamps.
	Create(ctx context.Context, inst *Instance) (*Instance, error)

	// GetByID retrieves a single instance.
	GetByID(ctx context.Context, id string) (*Instance, error)

	// ListByWorkspace returns instances belonging to a workspace with pagination.
	ListByWorkspace(ctx context.Context, workspaceID string, limit, offset int) ([]*Instance, error)

	// UpdateStatus persists a status change (and sets last_connected_at when connected).
	UpdateStatus(ctx context.Context, id string, status Status) error

	// UpdateAfterPair persists the JID, platform, and business name received after QR pairing.
	UpdateAfterPair(ctx context.Context, id, jid, platform, businessName string) error

	// Delete removes an instance from the database.
	Delete(ctx context.Context, id string) error

	// ListAll returns every instance across all workspaces (used at startup for reconnection).
	ListAll(ctx context.Context) ([]*Instance, error)

	// UpdateSettings persists the JSONB settings column for an instance.
	UpdateSettings(ctx context.Context, id string, s *Settings) error

	// GetByChatwootInboxID returns the instance whose Chatwoot inbox ID matches.
	// Returns ErrNotFound if no enabled instance has that inbox ID.
	GetByChatwootInboxID(ctx context.Context, inboxID int64) (*Instance, error)

	// CountByWorkspace returns the number of instances for a workspace.
	CountByWorkspace(ctx context.Context, workspaceID string) (int, error)
}
