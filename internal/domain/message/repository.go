package message

import (
	"context"
	"time"
)

// Repository defines the persistence contract for messages.
type Repository interface {
	// Create inserts a new message row and returns it with generated ID.
	Create(ctx context.Context, msg *Message) (*Message, error)

	// GetByID retrieves a message by its internal UUID.
	GetByID(ctx context.Context, id string) (*Message, error)

	// GetByWhatsAppID retrieves a message by its WhatsApp message ID within an instance.
	GetByWhatsAppID(ctx context.Context, instanceID, whatsappID string) (*Message, error)

	// ListByChat returns messages for a specific chat, newest first.
	ListByChat(ctx context.Context, instanceID, chatJID string, limit, offset int) ([]*Message, error)

	// UpdateStatus updates a message's delivery status.
	UpdateStatus(ctx context.Context, instanceID, whatsappID string, status MessageStatus) error

	// UpdateStatusBulk updates multiple messages by WhatsApp ID.
	UpdateStatusBulk(ctx context.Context, instanceID string, whatsappIDs []string, status MessageStatus) error

	// ListScheduledReady returns scheduled messages whose scheduled_at has passed.
	ListScheduledReady(ctx context.Context, limit int) ([]*Message, error)

	// UpdateAfterSend updates a scheduled message after engine send completes.
	UpdateAfterSend(ctx context.Context, id, waMessageID string, status MessageStatus, sentAt *time.Time, errMsg string) error

	// ListScheduledByInstance returns pending scheduled messages for an instance.
	ListScheduledByInstance(ctx context.Context, instanceID string, limit, offset int) ([]*Message, error)

	// Search returns messages matching the query string across content/text, within optional date range.
	Search(ctx context.Context, instanceID, query string, from, to *time.Time, limit, offset int) ([]*Message, error)
}
