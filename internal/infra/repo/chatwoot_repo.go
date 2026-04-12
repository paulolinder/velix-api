package repo

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
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
