package repo

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"velix/internal/domain/chatwoot"
)

// ChatwootRepo persists WhatsApp ↔ Chatwoot ID mappings.
type ChatwootRepo struct {
	db *pgxpool.Pool
}

func NewChatwootRepo(db *pgxpool.Pool) *ChatwootRepo {
	return &ChatwootRepo{db: db}
}

// GetContactID returns the cached Chatwoot contact ID for a WhatsApp JID.
// Returns 0, nil if not found.
func (r *ChatwootRepo) GetContactID(ctx context.Context, instanceID, jid string) (int64, error) {
	const q = `SELECT contact_id FROM chatwoot_contacts WHERE instance_id=$1 AND jid=$2`
	var id int64
	err := r.db.QueryRow(ctx, q, instanceID, jid).Scan(&id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, nil
		}
		return 0, err
	}
	return id, nil
}

// SaveContactID caches a Chatwoot contact ID for a WhatsApp JID.
func (r *ChatwootRepo) SaveContactID(ctx context.Context, instanceID, jid string, contactID int64) error {
	const q = `
		INSERT INTO chatwoot_contacts (instance_id, jid, contact_id)
		VALUES ($1, $2, $3)
		ON CONFLICT (instance_id, jid) DO UPDATE SET contact_id = EXCLUDED.contact_id`
	_, err := r.db.Exec(ctx, q, instanceID, jid, contactID)
	return err
}

// GetConversationID returns the cached Chatwoot conversation ID for a WhatsApp chat JID.
// Returns 0, nil if not found.
func (r *ChatwootRepo) GetConversationID(ctx context.Context, instanceID, chatJID string) (int64, error) {
	const q = `SELECT conversation_id FROM chatwoot_conversations WHERE instance_id=$1 AND chat_jid=$2`
	var id int64
	err := r.db.QueryRow(ctx, q, instanceID, chatJID).Scan(&id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, nil
		}
		return 0, err
	}
	return id, nil
}

// SaveConversationID caches a Chatwoot conversation ID for a WhatsApp chat JID.
func (r *ChatwootRepo) SaveConversationID(ctx context.Context, instanceID, chatJID string, convID int64) error {
	const q = `
		INSERT INTO chatwoot_conversations (instance_id, chat_jid, conversation_id, updated_at)
		VALUES ($1, $2, $3, NOW())
		ON CONFLICT (instance_id, chat_jid) DO UPDATE
		  SET conversation_id = EXCLUDED.conversation_id, updated_at = NOW()`
	_, err := r.db.Exec(ctx, q, instanceID, chatJID, convID)
	return err
}

// --- History buffer ----------------------------------------------------------

// SaveHistoryMessages bulk-inserts history sync messages into the buffer.
// Duplicates (same instance_id + message_id) are silently ignored.
func (r *ChatwootRepo) SaveHistoryMessages(ctx context.Context, instanceID string, msgs []chatwoot.HistoryBufferMsg) error {
	if len(msgs) == 0 {
		return nil
	}
	const q = `
		INSERT INTO chatwoot_history_buffer
			(instance_id, message_id, chat_jid, sender_jid, from_me, text, msg_type, push_name, timestamp)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT (instance_id, message_id) DO NOTHING`

	batch := &pgx.Batch{}
	for _, m := range msgs {
		batch.Queue(q, instanceID, m.MessageID, m.ChatJID, m.SenderJID, m.FromMe, m.Text, m.MsgType, m.PushName, m.Timestamp)
	}
	br := r.db.SendBatch(ctx, batch)
	defer br.Close()
	for range msgs {
		if _, err := br.Exec(); err != nil {
			return err
		}
	}
	return nil
}

// GetHistoryMessages returns buffered messages for an instance, ordered by timestamp.
func (r *ChatwootRepo) GetHistoryMessages(ctx context.Context, instanceID string) ([]chatwoot.HistoryBufferMsg, error) {
	const q = `SELECT message_id, chat_jid, sender_jid, from_me, text, msg_type, push_name, timestamp
	           FROM chatwoot_history_buffer
	           WHERE instance_id=$1
	           ORDER BY timestamp ASC`

	rows, err := r.db.Query(ctx, q, instanceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []chatwoot.HistoryBufferMsg
	for rows.Next() {
		var m chatwoot.HistoryBufferMsg
		if err := rows.Scan(&m.MessageID, &m.ChatJID, &m.SenderJID, &m.FromMe, &m.Text, &m.MsgType, &m.PushName, &m.Timestamp); err != nil {
			return nil, err
		}
		result = append(result, m)
	}
	return result, rows.Err()
}

// DeleteHistoryMessages removes all buffered messages for an instance after successful sync.
func (r *ChatwootRepo) DeleteHistoryMessages(ctx context.Context, instanceID string) error {
	_, err := r.db.Exec(ctx, `DELETE FROM chatwoot_history_buffer WHERE instance_id=$1`, instanceID)
	return err
}

// GetChatJIDByConversationID performs a reverse lookup: Chatwoot conversation ID → WhatsApp chat JID.
// Returns "", nil if not found.
func (r *ChatwootRepo) GetChatJIDByConversationID(ctx context.Context, instanceID string, convID int64) (string, error) {
	const q = `SELECT chat_jid FROM chatwoot_conversations WHERE instance_id=$1 AND conversation_id=$2`
	var jid string
	err := r.db.QueryRow(ctx, q, instanceID, convID).Scan(&jid)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", nil
		}
		return "", err
	}
	return jid, nil
}
