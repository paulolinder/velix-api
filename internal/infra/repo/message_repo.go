package repo

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"velix/internal/domain/message"
)

// MessageRepo is the PostgreSQL implementation of message.Repository.
type MessageRepo struct {
	db *pgxpool.Pool
}

// NewMessageRepo creates a new PostgreSQL-backed message repository.
func NewMessageRepo(db *pgxpool.Pool) *MessageRepo {
	return &MessageRepo{db: db}
}

// Create inserts a new message and returns the persisted record.
func (r *MessageRepo) Create(ctx context.Context, msg *message.Message) (*message.Message, error) {
	const q = `
		INSERT INTO messages (
			instance_id, whatsapp_message_id, direction, status,
			from_jid, to_jid, chat_jid, is_group, type, content,
			scheduled_at, sent_at, delivered_at, read_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)
		RETURNING id, created_at`

	row := r.db.QueryRow(ctx, q,
		msg.InstanceID,
		nullString(msg.WhatsAppMessageID),
		msg.Direction,
		msg.Status,
		msg.FromJID,
		msg.ToJID,
		msg.ChatJID,
		msg.IsGroup,
		msg.Type,
		msg.Content,
		msg.ScheduledAt,
		msg.SentAt,
		msg.DeliveredAt,
		msg.ReadAt,
	)

	if err := row.Scan(&msg.ID, &msg.CreatedAt); err != nil {
		return nil, fmt.Errorf("create message: %w", err)
	}
	return msg, nil
}

// GetByID retrieves a message by its internal UUID.
func (r *MessageRepo) GetByID(ctx context.Context, id string) (*message.Message, error) {
	const q = `
		SELECT id, instance_id, COALESCE(whatsapp_message_id,''), direction, status,
		       from_jid, to_jid, chat_jid, is_group, type, content,
		       COALESCE(error_message,''), scheduled_at, sent_at, delivered_at, read_at, created_at
		FROM messages WHERE id = $1`

	msg, err := scanMessage(r.db.QueryRow(ctx, q, id))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("message not found")
		}
		return nil, err
	}
	return msg, nil
}

// GetByWhatsAppID retrieves a message by WA message ID within an instance.
func (r *MessageRepo) GetByWhatsAppID(ctx context.Context, instanceID, whatsappID string) (*message.Message, error) {
	const q = `
		SELECT id, instance_id, COALESCE(whatsapp_message_id,''), direction, status,
		       from_jid, to_jid, chat_jid, is_group, type, content,
		       COALESCE(error_message,''), scheduled_at, sent_at, delivered_at, read_at, created_at
		FROM messages WHERE instance_id = $1 AND whatsapp_message_id = $2`

	msg, err := scanMessage(r.db.QueryRow(ctx, q, instanceID, whatsappID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("message not found")
		}
		return nil, err
	}
	return msg, nil
}

// ListByChat returns messages for a specific chat, newest first.
func (r *MessageRepo) ListByChat(ctx context.Context, instanceID, chatJID string, limit, offset int) ([]*message.Message, error) {
	const q = `
		SELECT id, instance_id, COALESCE(whatsapp_message_id,''), direction, status,
		       from_jid, to_jid, chat_jid, is_group, type, content,
		       COALESCE(error_message,''), scheduled_at, sent_at, delivered_at, read_at, created_at
		FROM messages
		WHERE instance_id = $1 AND chat_jid = $2
		ORDER BY created_at DESC
		LIMIT $3 OFFSET $4`

	rows, err := r.db.Query(ctx, q, instanceID, chatJID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("list messages: %w", err)
	}
	defer rows.Close()

	var results []*message.Message
	for rows.Next() {
		msg, err := scanMessage(rows)
		if err != nil {
			return nil, err
		}
		results = append(results, msg)
	}
	return results, rows.Err()
}

// UpdateStatus updates a single message's delivery status by WA message ID.
func (r *MessageRepo) UpdateStatus(ctx context.Context, instanceID, whatsappID string, status message.MessageStatus) error {
	const q = `UPDATE messages SET status=$1 WHERE instance_id=$2 AND whatsapp_message_id=$3`
	_, err := r.db.Exec(ctx, q, status, instanceID, whatsappID)
	if err != nil {
		return fmt.Errorf("update message status: %w", err)
	}
	return nil
}

// UpdateStatusBulk updates multiple messages by WA message ID.
func (r *MessageRepo) UpdateStatusBulk(ctx context.Context, instanceID string, whatsappIDs []string, status message.MessageStatus) error {
	const q = `UPDATE messages SET status=$1 WHERE instance_id=$2 AND whatsapp_message_id = ANY($3)`
	_, err := r.db.Exec(ctx, q, status, instanceID, whatsappIDs)
	if err != nil {
		return fmt.Errorf("bulk update message status: %w", err)
	}
	return nil
}

// ListScheduledReady returns scheduled messages whose time has come.
func (r *MessageRepo) ListScheduledReady(ctx context.Context, limit int) ([]*message.Message, error) {
	const q = `
		SELECT id, instance_id, COALESCE(whatsapp_message_id,''), direction, status,
		       from_jid, to_jid, chat_jid, is_group, type, content,
		       COALESCE(error_message,''), scheduled_at, sent_at, delivered_at, read_at, created_at
		FROM messages
		WHERE status = 'scheduled' AND scheduled_at IS NOT NULL AND scheduled_at <= NOW()
		ORDER BY scheduled_at ASC
		LIMIT $1`

	rows, err := r.db.Query(ctx, q, limit)
	if err != nil {
		return nil, fmt.Errorf("list scheduled ready: %w", err)
	}
	defer rows.Close()

	var results []*message.Message
	for rows.Next() {
		msg, err := scanMessage(rows)
		if err != nil {
			return nil, err
		}
		results = append(results, msg)
	}
	return results, rows.Err()
}

// UpdateAfterSend updates a scheduled message after the engine processes it.
func (r *MessageRepo) UpdateAfterSend(ctx context.Context, id, waMessageID string, status message.MessageStatus, sentAt *time.Time, errMsg string) error {
	const q = `
		UPDATE messages
		SET whatsapp_message_id = COALESCE(NULLIF($1,''), whatsapp_message_id),
		    status = $2, sent_at = $3, error_message = $4
		WHERE id = $5`
	_, err := r.db.Exec(ctx, q, waMessageID, status, sentAt, errMsg, id)
	if err != nil {
		return fmt.Errorf("update after send: %w", err)
	}
	return nil
}

// ListScheduledByInstance returns pending scheduled messages for an instance.
func (r *MessageRepo) ListScheduledByInstance(ctx context.Context, instanceID string, limit, offset int) ([]*message.Message, error) {
	const q = `
		SELECT id, instance_id, COALESCE(whatsapp_message_id,''), direction, status,
		       from_jid, to_jid, chat_jid, is_group, type, content,
		       COALESCE(error_message,''), scheduled_at, sent_at, delivered_at, read_at, created_at
		FROM messages
		WHERE instance_id = $1 AND status IN ('scheduled','cancelled')
		ORDER BY scheduled_at ASC
		LIMIT $2 OFFSET $3`

	rows, err := r.db.Query(ctx, q, instanceID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("list scheduled: %w", err)
	}
	defer rows.Close()

	var results []*message.Message
	for rows.Next() {
		msg, err := scanMessage(rows)
		if err != nil {
			return nil, err
		}
		results = append(results, msg)
	}
	return results, rows.Err()
}

// Search returns messages matching a text query within an instance, with optional date range.
func (r *MessageRepo) Search(ctx context.Context, instanceID, query string, from, to *time.Time, limit, offset int) ([]*message.Message, error) {
	const q = `
		SELECT id, instance_id, COALESCE(whatsapp_message_id,''), direction, status,
		       from_jid, to_jid, chat_jid, is_group, type, content,
		       COALESCE(error_message,''), scheduled_at, sent_at, delivered_at, read_at, created_at
		FROM messages
		WHERE instance_id = $1
		  AND content::text ILIKE $2
		  AND ($3::timestamptz IS NULL OR created_at >= $3)
		  AND ($4::timestamptz IS NULL OR created_at <= $4)
		ORDER BY created_at DESC
		LIMIT $5 OFFSET $6`

	like := "%" + query + "%"
	rows, err := r.db.Query(ctx, q, instanceID, like, from, to, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("search messages: %w", err)
	}
	defer rows.Close()

	var results []*message.Message
	for rows.Next() {
		msg, err := scanMessage(rows)
		if err != nil {
			return nil, err
		}
		results = append(results, msg)
	}
	return results, rows.Err()
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func scanMessage(row rowScanner) (*message.Message, error) {
	msg := &message.Message{}
	err := row.Scan(
		&msg.ID,
		&msg.InstanceID,
		&msg.WhatsAppMessageID,
		&msg.Direction,
		&msg.Status,
		&msg.FromJID,
		&msg.ToJID,
		&msg.ChatJID,
		&msg.IsGroup,
		&msg.Type,
		&msg.Content,
		&msg.ErrorMessage,
		&msg.ScheduledAt,
		&msg.SentAt,
		&msg.DeliveredAt,
		&msg.ReadAt,
		&msg.CreatedAt,
	)
	if err != nil {
		return nil, err
	}
	return msg, nil
}
