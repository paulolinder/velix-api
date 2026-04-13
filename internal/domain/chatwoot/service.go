package chatwoot

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/rs/zerolog"

	"velix/internal/domain/instance"
	"velix/internal/engine"
	"velix/internal/logger"
)

// HistoryBufferMsg mirrors repo.HistoryBufferMsg for the domain layer.
type HistoryBufferMsg struct {
	MessageID string
	ChatJID   string
	SenderJID string
	FromMe    bool
	Text      string
	MsgType   string
	PushName  string
	Timestamp time.Time
}

// MappingRepo persists WhatsApp ↔ Chatwoot ID mappings.
type MappingRepo interface {
	GetContactID(ctx context.Context, instanceID, jid string) (int64, error)
	SaveContactID(ctx context.Context, instanceID, jid string, contactID int64) error
	GetConversationID(ctx context.Context, instanceID, chatJID string) (int64, error)
	SaveConversationID(ctx context.Context, instanceID, chatJID string, convID int64) error
	GetChatJIDByConversationID(ctx context.Context, instanceID string, convID int64) (string, error)
	SaveHistoryMessages(ctx context.Context, instanceID string, msgs []HistoryBufferMsg) error
	GetHistoryMessages(ctx context.Context, instanceID string) ([]HistoryBufferMsg, error)
	DeleteHistoryMessages(ctx context.Context, instanceID string) error
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
	http     *http.Client
	log      zerolog.Logger
	eventCh  chan engine.Event // buffered channel for async event processing
}

// NewService creates the Chatwoot service and subscribes it to engine events.
// Events are processed asynchronously via a buffered channel to avoid blocking
// the engine's event dispatch loop when Chatwoot API is slow.
func NewService(eng engine.Engine, instSvc InstanceSettingsReader, _ any, mappings MappingRepo) *Service {
	s := &Service{
		eng:      eng,
		instSvc:  instSvc,
		mappings: mappings,
		http:     &http.Client{Timeout: 30 * time.Second},
		log:      logger.New("chatwoot-service"),
		eventCh:  make(chan engine.Event, 500),
	}
	eng.Subscribe(s.enqueueEvent)
	// Start worker that processes events from the channel.
	go s.eventWorker()
	return s
}

// enqueueEvent puts events into the buffered channel without blocking the engine.
func (s *Service) enqueueEvent(evt engine.Event) {
	select {
	case s.eventCh <- evt:
	default:
		s.log.Warn().Str("event", string(evt.Type)).Msg("Chatwoot event channel full — dropping event")
	}
}

// eventWorker processes Chatwoot events sequentially from the channel.
func (s *Service) eventWorker() {
	for evt := range s.eventCh {
		s.handleEngineEvent(evt)
	}
}

// SyncResult holds the result of a history sync operation.
type SyncResult struct {
	ContactsCreated      int `json:"contacts_created"`
	ConversationsCreated int `json:"conversations_created"`
	MessagesSynced       int `json:"messages_synced"`
	Skipped              int `json:"skipped"`
	Errors               int `json:"errors"`
}

// SyncHistory imports known WhatsApp contacts into Chatwoot as contacts+conversations.
// It runs synchronously and rate-limits Chatwoot API calls to avoid overload.
func (s *Service) SyncHistory(ctx context.Context, instanceID string) (*SyncResult, error) {
	cfg, err := s.instSvc.GetChatwootSettings(ctx, instanceID)
	if err != nil {
		return nil, fmt.Errorf("get chatwoot settings: %w", err)
	}
	if !cfg.ChatwootEnabled || cfg.ChatwootURL == "" || cfg.ChatwootToken == "" || cfg.ChatwootInboxID == 0 {
		return nil, fmt.Errorf("chatwoot is not fully configured on this instance")
	}

	contacts, err := s.eng.GetContacts(ctx, instanceID)
	if err != nil {
		return nil, fmt.Errorf("get contacts: %w", err)
	}

	client := NewClient(cfg.ChatwootURL, cfg.ChatwootToken, cfg.ChatwootAccountID)
	result := &SyncResult{}

	// Rate limit: ~3 contacts/sec (each contact = up to 2 API calls).
	ticker := time.NewTicker(350 * time.Millisecond)
	defer ticker.Stop()

	for jid, info := range contacts {
		select {
		case <-ctx.Done():
			return result, ctx.Err()
		case <-ticker.C:
		}

		phone := jidToPhone(jid)
		name := info.PushName
		if name == "" {
			name = info.BusinessName
		}
		if name == "" {
			name = phone
		}

		// Check if we already have a mapping (skip = already synced).
		if existingID, _ := s.mappings.GetContactID(ctx, instanceID, jid); existingID != 0 {
			result.Skipped++
			continue
		}

		contactID, err := client.FindOrCreateContact(ctx, name, "+"+phone, cfg.ChatwootInboxID)
		if err != nil {
			s.log.Warn().Err(err).Str("jid", jid).Msg("Chatwoot sync: failed to create contact")
			result.Errors++
			continue
		}
		_ = s.mappings.SaveContactID(ctx, instanceID, jid, contactID)
		result.ContactsCreated++

		// Wait before conversation creation.
		select {
		case <-ctx.Done():
			return result, ctx.Err()
		case <-ticker.C:
		}

		_, err = client.FindOrCreateConversation(ctx, contactID, cfg.ChatwootInboxID, cfg.ChatwootConvPending)
		if err != nil {
			s.log.Warn().Err(err).Str("jid", jid).Msg("Chatwoot sync: failed to create conversation")
			result.Errors++
			continue
		}
		_ = s.mappings.SaveConversationID(ctx, instanceID, jid, contactID)
		result.ConversationsCreated++
	}

	// Phase 2: replay buffered history messages into Chatwoot conversations.
	histMsgs, err := s.mappings.GetHistoryMessages(ctx, instanceID)
	if err != nil {
		s.log.Warn().Err(err).Str("instance", instanceID).Msg("Chatwoot sync: failed to load history buffer")
	} else if len(histMsgs) > 0 {
		s.replayHistoryMessages(ctx, client, instanceID, cfg, histMsgs, result, ticker)
	}

	s.log.Info().
		Str("instance", instanceID).
		Int("contacts", result.ContactsCreated).
		Int("conversations", result.ConversationsCreated).
		Int("messages", result.MessagesSynced).
		Int("skipped", result.Skipped).
		Int("errors", result.Errors).
		Msg("Chatwoot history sync completed")

	return result, nil
}

// replayHistoryMessages posts buffered history messages into Chatwoot conversations.
func (s *Service) replayHistoryMessages(ctx context.Context, client *Client, instanceID string, cfg *instance.Settings, msgs []HistoryBufferMsg, result *SyncResult, ticker *time.Ticker) {
	for _, m := range msgs {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}

		phone := jidToPhone(m.ChatJID)
		name := m.PushName
		if name == "" {
			name = phone
		}

		contactID, err := s.findOrCreateContact(ctx, client, instanceID, m.ChatJID, name, phone, cfg.ChatwootInboxID)
		if err != nil {
			result.Errors++
			continue
		}

		convID, err := s.findOrCreateConversation(ctx, client, instanceID, m.ChatJID, contactID, cfg.ChatwootInboxID, cfg.ChatwootConvPending)
		if err != nil {
			result.Errors++
			continue
		}

		// Build message content with sender prefix for context.
		content := m.Text
		if m.FromMe {
			content = "*Eu:* " + content
		} else if m.PushName != "" {
			content = "*" + m.PushName + ":* " + content
		}

		if err := client.PostIncomingMessage(ctx, convID, content); err != nil {
			s.log.Warn().Err(err).Str("msg_id", m.MessageID).Msg("Chatwoot sync: failed to post message")
			result.Errors++
			continue
		}
		result.MessagesSynced++
	}

	// Clean up buffer after successful replay.
	_ = s.mappings.DeleteHistoryMessages(ctx, instanceID)
}

// --- Engine event handler ----------------------------------------------------

func (s *Service) handleEngineEvent(evt engine.Event) {
	ctx := context.Background()

	switch evt.Type {
	case engine.EventMessageReceived:
		payload, ok := evt.Payload.(*engine.MessagePayload)
		if !ok || payload.FromMe || payload.IsGroup {
			return
		}
		if err := s.syncInbound(ctx, evt.InstanceID, payload); err != nil {
			s.log.Warn().Err(err).
				Str("instance", evt.InstanceID).
				Str("from", payload.From).
				Msg("Chatwoot: failed to sync inbound message")
		}

	case engine.EventHistorySync:
		payload, ok := evt.Payload.(*engine.HistorySyncPayload)
		if !ok || len(payload.Messages) == 0 {
			return
		}
		s.bufferHistorySync(ctx, evt.InstanceID, payload)
	}
}

// bufferHistorySync saves history sync messages into the database buffer for later replay.
func (s *Service) bufferHistorySync(ctx context.Context, instanceID string, p *engine.HistorySyncPayload) {
	msgs := make([]HistoryBufferMsg, len(p.Messages))
	for i, m := range p.Messages {
		msgs[i] = HistoryBufferMsg{
			MessageID: m.MessageID,
			ChatJID:   m.ChatJID,
			SenderJID: m.SenderJID,
			FromMe:    m.FromMe,
			Text:      m.Text,
			MsgType:   m.Type,
			PushName:  m.PushName,
			Timestamp: m.Timestamp,
		}
	}
	if err := s.mappings.SaveHistoryMessages(ctx, instanceID, msgs); err != nil {
		s.log.Warn().Err(err).Str("instance", instanceID).Int("count", len(msgs)).
			Msg("Chatwoot: failed to buffer history sync messages")
		return
	}
	s.log.Info().Str("instance", instanceID).Int("count", len(msgs)).
		Msg("Chatwoot: history sync messages buffered")
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

	phone := jidToPhone(p.From)
	name := p.PushName
	if name == "" {
		name = phone
	}

	contactID, err := s.findOrCreateContact(ctx, client, instanceID, p.From, name, phone, cfg.ChatwootInboxID)
	if err != nil {
		return fmt.Errorf("find/create contact: %w", err)
	}

	convID, err := s.findOrCreateConversation(ctx, client, instanceID, p.Chat, contactID, cfg.ChatwootInboxID, cfg.ChatwootConvPending)
	if err != nil {
		return fmt.Errorf("find/create conversation: %w", err)
	}

	if cfg.ChatwootReopenConv {
		_ = client.ReopenConversation(ctx, convID)
	}

	// Media message — upload the file as an attachment if available.
	if p.Media != nil && p.Media.LocalPath != "" {
		return s.forwardMediaToConversation(ctx, client, convID, p)
	}

	// Text or fallback content.
	content := buildMessageContent(p)
	if content == "" {
		return nil
	}
	return client.PostIncomingMessage(ctx, convID, content)
}

// forwardMediaToConversation reads the media file and posts it to Chatwoot.
func (s *Service) forwardMediaToConversation(ctx context.Context, client *Client, convID int64, p *engine.MessagePayload) error {
	if p.Media.DirectURL == "" && p.Media.LocalPath == "" {
		return client.PostIncomingMessage(ctx, convID, buildMessageContent(p))
	}

	var fileData []byte

	// Prefer reading from the local filesystem — the engine already saved the file there.
	// DirectURL is a relative path (/v1/media/UUID) and cannot be used for HTTP requests.
	if p.Media.LocalPath != "" {
		fileData, _ = os.ReadFile(p.Media.LocalPath)
	}

	// Fallback: try an absolute HTTP URL if LocalPath is absent or unreadable.
	if len(fileData) == 0 && p.Media.DirectURL != "" {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.Media.DirectURL, nil)
		if err == nil {
			resp, err := s.http.Do(req)
			if err == nil && resp.StatusCode == http.StatusOK {
				defer resp.Body.Close()
				fileData, _ = io.ReadAll(resp.Body)
			}
		}
	}

	if len(fileData) == 0 {
		return client.PostIncomingMessage(ctx, convID, buildMessageContent(p))
	}

	return client.PostIncomingMedia(ctx, convID, p.Media.Caption, p.Media.FileName, p.Media.MimeType, fileData)
}

func (s *Service) findOrCreateContact(ctx context.Context, client *Client, instanceID, jid, name, phone string, inboxID int64) (int64, error) {
	if id, _ := s.mappings.GetContactID(ctx, instanceID, jid); id != 0 {
		return id, nil
	}
	id, err := client.FindOrCreateContact(ctx, name, "+"+phone, inboxID)
	if err != nil {
		return 0, err
	}
	_ = s.mappings.SaveContactID(ctx, instanceID, jid, id)
	return id, nil
}

func (s *Service) findOrCreateConversation(ctx context.Context, client *Client, instanceID, chatJID string, contactID, inboxID int64, pending bool) (int64, error) {
	if id, _ := s.mappings.GetConversationID(ctx, instanceID, chatJID); id != 0 {
		return id, nil
	}
	id, err := client.FindOrCreateConversation(ctx, contactID, inboxID, pending)
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
	Private     bool   `json:"private"`      // true for internal agent notes — must not be forwarded
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
	if !isOutgoingMessage(p.MessageType) {
		return nil
	}
	// Skip internal agent notes — they must not be forwarded to WhatsApp.
	if p.Private {
		return nil
	}
	// Ignore bot messages to prevent loops.
	if p.Sender.Type == "agent_bot" {
		return nil
	}

	inst, err := s.instSvc.GetByChatwootInboxID(ctx, p.Conversation.InboxID)
	if err != nil {
		return fmt.Errorf("no instance for inbox %d: %w", p.Conversation.InboxID, err)
	}

	chatJID, err := s.mappings.GetChatJIDByConversationID(ctx, inst.ID, p.Conversation.ID)
	if err != nil || chatJID == "" {
		return fmt.Errorf("no WhatsApp chat for conversation %d", p.Conversation.ID)
	}

	// Send text content if present.
	content := strings.TrimSpace(p.Content)
	if content != "" {
		if inst.Settings.ChatwootSignMsgs && p.Sender.Name != "" {
			content = fmt.Sprintf("*%s:* %s", p.Sender.Name, content)
		}
		if _, err := s.eng.SendText(ctx, inst.ID, chatJID, content); err != nil {
			return err
		}
	}

	// Send attachments (images, documents, etc.) sent by the agent.
	for _, att := range p.Attachments {
		if att.DataURL == "" {
			continue
		}
		if err := s.forwardAttachmentToWA(ctx, inst.ID, chatJID, att.DataURL, att.FileType); err != nil {
			s.log.Warn().Err(err).Str("url", att.DataURL).Msg("Chatwoot: failed to forward attachment to WhatsApp")
		}
	}

	return nil
}

// forwardAttachmentToWA downloads a Chatwoot attachment and sends it via WhatsApp.
func (s *Service) forwardAttachmentToWA(ctx context.Context, instanceID, to, dataURL, fileType string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, dataURL, nil)
	if err != nil {
		return err
	}
	resp, err := s.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	mimeType := resp.Header.Get("Content-Type")
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}

	mediaType := fileTypeToMediaType(fileType, mimeType)
	_, err = s.eng.SendMedia(ctx, instanceID, to, engine.MediaPayload{
		Type:     mediaType,
		Data:     data,
		MimeType: strings.Split(mimeType, ";")[0],
	})
	return err
}

// --- Helpers -----------------------------------------------------------------

func jidToPhone(jid string) string {
	if idx := strings.Index(jid, "@"); idx != -1 {
		return jid[:idx]
	}
	return jid
}

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

func isOutgoingMessage(raw any) bool {
	switch v := raw.(type) {
	case string:
		return v == "outgoing"
	case float64:
		return v == 1
	}
	return false
}

// fileTypeToMediaType maps Chatwoot file_type to engine.MediaType.
func fileTypeToMediaType(fileType, mimeType string) engine.MediaType {
	switch strings.ToLower(fileType) {
	case "image":
		return engine.MediaTypeImage
	case "video":
		return engine.MediaTypeVideo
	case "audio":
		return engine.MediaTypeAudio
	case "sticker":
		return engine.MediaTypeSticker
	default:
		// Derive from MIME type when fileType is "file" or unknown.
		mt := strings.Split(mimeType, "/")[0]
		switch mt {
		case "image":
			return engine.MediaTypeImage
		case "video":
			return engine.MediaTypeVideo
		case "audio":
			return engine.MediaTypeAudio
		}
		return engine.MediaTypeDocument
	}
}
