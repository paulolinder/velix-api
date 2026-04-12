package chatwoot

import (
	"context"
	"fmt"
	"strings"

	"github.com/rs/zerolog"

	"velix/internal/domain/instance"
	"velix/internal/engine"
	"velix/internal/logger"
)

// MappingRepo persists WhatsApp ↔ Chatwoot ID mappings.
type MappingRepo interface {
	GetContactID(ctx context.Context, instanceID, jid string) (int64, error)
	SaveContactID(ctx context.Context, instanceID, jid string, contactID int64) error
	GetConversationID(ctx context.Context, instanceID, chatJID string) (int64, error)
	SaveConversationID(ctx context.Context, instanceID, chatJID string, convID int64) error
	GetChatJIDByConversationID(ctx context.Context, instanceID string, convID int64) (string, error)
}

// InstanceSettingsReader provides per-instance Chatwoot configuration.
type InstanceSettingsReader interface {
	GetChatwootSettings(ctx context.Context, instanceID string) (*instance.Settings, error)
	GetByChatwootInboxID(ctx context.Context, inboxID int64) (*instance.Instance, error)
}

// Service bridges WhatsApp events and Chatwoot conversations.
type Service struct {
	eng      engine.Engine
	instSvc  InstanceSettingsReader
	mappings MappingRepo
	log      zerolog.Logger
}

// NewService creates the Chatwoot service and subscribes it to engine events.
func NewService(eng engine.Engine, instSvc InstanceSettingsReader, _ any, mappings MappingRepo) *Service {
	s := &Service{
		eng:      eng,
		instSvc:  instSvc,
		mappings: mappings,
		log:      logger.New("chatwoot-service"),
	}
	eng.Subscribe(s.handleEngineEvent)
	return s
}

// --- Engine event handler ----------------------------------------------------

func (s *Service) handleEngineEvent(evt engine.Event) {
	if evt.Type != engine.EventMessageReceived {
		return
	}
	payload, ok := evt.Payload.(*engine.MessagePayload)
	if !ok {
		return
	}
	// Only forward messages from the contact (not our own API/manual sends).
	if payload.FromMe {
		return
	}
	// Skip group messages — Chatwoot doesn't model group chats natively.
	if payload.IsGroup {
		return
	}

	ctx := context.Background()
	if err := s.syncInbound(ctx, evt.InstanceID, payload); err != nil {
		s.log.Warn().Err(err).
			Str("instance", evt.InstanceID).
			Str("from", payload.From).
			Msg("Chatwoot: failed to sync inbound message")
	}
}

func (s *Service) syncInbound(ctx context.Context, instanceID string, p *engine.MessagePayload) error {
	cfg, err := s.instSvc.GetChatwootSettings(ctx, instanceID)
	if err != nil {
		return err
	}
	if !cfg.ChatwootEnabled || cfg.ChatwootURL == "" || cfg.ChatwootToken == "" {
		return nil
	}

	client := NewClient(cfg.ChatwootURL, cfg.ChatwootToken, cfg.ChatwootAccountID)

	// Normalize sender JID → phone number for Chatwoot.
	phone := jidToPhone(p.From)
	name := p.PushName
	if name == "" {
		name = phone
	}

	// Find or create the Chatwoot contact.
	contactID, err := s.findOrCreateContact(ctx, client, instanceID, p.From, name, phone)
	if err != nil {
		return fmt.Errorf("find/create contact: %w", err)
	}

	// Find or create the Chatwoot conversation for this chat.
	convID, err := s.findOrCreateConversation(ctx, client, instanceID, p.Chat, contactID, cfg.ChatwootInboxID)
	if err != nil {
		return fmt.Errorf("find/create conversation: %w", err)
	}

	// Optionally reopen resolved conversations.
	if cfg.ChatwootReopenConv {
		_ = client.ReopenConversation(ctx, convID)
	}

	// Build message content.
	content := buildMessageContent(p)
	if content == "" {
		return nil // nothing to forward (e.g. sticker with no text)
	}

	return client.PostIncomingMessage(ctx, convID, content)
}

func (s *Service) findOrCreateContact(ctx context.Context, client *Client, instanceID, jid, name, phone string) (int64, error) {
	// Check local cache first.
	if id, _ := s.mappings.GetContactID(ctx, instanceID, jid); id != 0 {
		return id, nil
	}
	id, err := client.FindOrCreateContact(ctx, name, "+"+phone)
	if err != nil {
		return 0, err
	}
	_ = s.mappings.SaveContactID(ctx, instanceID, jid, id)
	return id, nil
}

func (s *Service) findOrCreateConversation(ctx context.Context, client *Client, instanceID, chatJID string, contactID, inboxID int64) (int64, error) {
	// Check local cache first.
	if id, _ := s.mappings.GetConversationID(ctx, instanceID, chatJID); id != 0 {
		return id, nil
	}
	id, err := client.FindOrCreateConversation(ctx, contactID, inboxID)
	if err != nil {
		return 0, err
	}
	_ = s.mappings.SaveConversationID(ctx, instanceID, chatJID, id)
	return id, nil
}

// --- Outbound: Chatwoot → WhatsApp ------------------------------------------

// ChatwootWebhookPayload is the body Chatwoot POSTs to our webhook endpoint.
type ChatwootWebhookPayload struct {
	Event       string `json:"event"`
	MessageType any    `json:"message_type"` // string or int depending on CW version
	Content     string `json:"content"`
	Conversation struct {
		ID      int64 `json:"id"`
		InboxID int64 `json:"inbox_id"`
	} `json:"conversation"`
	Sender struct {
		Type string `json:"type"` // "user" | "agent_bot" | "contact"
		Name string `json:"name"`
	} `json:"sender"`
	Attachments []struct {
		FileType string `json:"file_type"`
		DataURL  string `json:"data_url"`
	} `json:"attachments"`
}

// HandleWebhook processes an inbound Chatwoot webhook and forwards agent messages to WhatsApp.
func (s *Service) HandleWebhook(ctx context.Context, p *ChatwootWebhookPayload) error {
	if p.Event != "message_created" {
		return nil
	}
	// Only forward outgoing messages (agent → contact).
	if !isOutgoingMessage(p.MessageType) {
		return nil
	}
	// Ignore bot messages to prevent loops if an AI agent is configured.
	if p.Sender.Type == "agent_bot" {
		return nil
	}

	// Find the instance by Chatwoot inbox ID.
	inst, err := s.instSvc.GetByChatwootInboxID(ctx, p.Conversation.InboxID)
	if err != nil {
		return fmt.Errorf("no instance for inbox %d: %w", p.Conversation.InboxID, err)
	}

	// Reverse-lookup the WhatsApp chat JID from our mappings.
	chatJID, err := s.mappings.GetChatJIDByConversationID(ctx, inst.ID, p.Conversation.ID)
	if err != nil || chatJID == "" {
		return fmt.Errorf("no WhatsApp chat for conversation %d", p.Conversation.ID)
	}

	// Build message text.
	content := strings.TrimSpace(p.Content)
	if content == "" {
		return nil
	}
	if inst.Settings.ChatwootSignMsgs && p.Sender.Name != "" {
		content = fmt.Sprintf("*%s:* %s", p.Sender.Name, content)
	}

	_, err = s.eng.SendText(ctx, inst.ID, chatJID, content)
	return err
}

// --- Helpers -----------------------------------------------------------------

// jidToPhone strips the WhatsApp suffix and returns the raw phone number.
// "5511999990001@s.whatsapp.net" → "5511999990001"
func jidToPhone(jid string) string {
	if idx := strings.Index(jid, "@"); idx != -1 {
		return jid[:idx]
	}
	return jid
}

// buildMessageContent returns a text representation of the WhatsApp message.
func buildMessageContent(p *engine.MessagePayload) string {
	switch {
	case p.Text != "":
		return p.Text
	case p.Media != nil && p.Media.Caption != "":
		return fmt.Sprintf("[%s] %s", strings.ToUpper(p.Type), p.Media.Caption)
	case p.Media != nil:
		return fmt.Sprintf("[%s]", strings.ToUpper(p.Type))
	case p.Location != nil:
		if p.Location.Name != "" {
			return fmt.Sprintf("[Localização: %s — %.6f, %.6f]", p.Location.Name, p.Location.Latitude, p.Location.Longitude)
		}
		return fmt.Sprintf("[Localização: %.6f, %.6f]", p.Location.Latitude, p.Location.Longitude)
	case p.VCard != nil:
		return fmt.Sprintf("[Contato: %s]", p.VCard.DisplayName)
	default:
		return ""
	}
}

// isOutgoingMessage returns true when the Chatwoot message_type indicates an agent outgoing message.
// Chatwoot uses either the string "outgoing" or the integer 1.
func isOutgoingMessage(raw any) bool {
	switch v := raw.(type) {
	case string:
		return v == "outgoing"
	case float64:
		return v == 1
	}
	return false
}
