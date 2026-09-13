package message

import (
	"context"
	"encoding/base64"
	"fmt"
	"time"

	"github.com/rs/zerolog"

	"velix/internal/engine"
	"velix/internal/logger"
	"velix/internal/metrics"
)

// Service orchestrates message sending, scheduling, and persistence.
type Service struct {
	repo   Repository
	engine engine.Engine
	log    zerolog.Logger
}

// NewService creates a new message service and subscribes to engine events.
func NewService(repo Repository, eng engine.Engine) *Service {
	s := &Service{
		repo:   repo,
		engine: eng,
		log:    logger.New("message-service"),
	}
	eng.Subscribe(s.handleEngineEvent)
	return s
}

// ---------------------------------------------------------------------------
// Send / Schedule
// ---------------------------------------------------------------------------

// SendText sends a plain text message immediately, or schedules it for later
// if sendAt is non-nil and in the future.
func (s *Service) SendText(ctx context.Context, instanceID, to, text string, sendAt *time.Time, opts ...engine.SendOptions) (*Message, error) {
	if sendAt != nil && sendAt.After(time.Now()) {
		return s.scheduleText(ctx, instanceID, to, text, sendAt, opts...)
	}
	return s.sendTextNow(ctx, instanceID, to, text, opts...)
}

// SendMedia sends a media message immediately, or schedules it for later.
// For scheduled media, payload bytes are base64-encoded in the content JSONB.
func (s *Service) SendMedia(ctx context.Context, instanceID, to string, sendAt *time.Time, payload engine.MediaPayload, opts ...engine.SendOptions) (*Message, error) {
	if sendAt != nil && sendAt.After(time.Now()) {
		return s.scheduleMedia(ctx, instanceID, to, sendAt, payload)
	}
	return s.sendMediaNow(ctx, instanceID, to, payload, opts...)
}

// SendReaction proxies to the engine (reactions are ephemeral, not persisted).
func (s *Service) SendReaction(ctx context.Context, instanceID, to, messageID, reaction string) error {
	return s.engine.SendReaction(ctx, instanceID, to, messageID, reaction)
}

// RevokeMessage deletes a sent message for everyone and updates its status.
func (s *Service) RevokeMessage(ctx context.Context, instanceID, to, messageID string) error {
	if err := s.engine.RevokeMessage(ctx, instanceID, to, messageID); err != nil {
		return err
	}
	if err := s.repo.UpdateStatus(ctx, instanceID, messageID, StatusRevoked); err != nil {
		s.log.Warn().Err(err).Str("wa_id", messageID).Msg("Failed to mark message as revoked in DB")
	}
	return nil
}

// MarkAsRead sends read receipts for one or more messages.
func (s *Service) MarkAsRead(ctx context.Context, instanceID, chat string, messageIDs []string) error {
	return s.engine.MarkAsRead(ctx, instanceID, chat, messageIDs)
}

// ---------------------------------------------------------------------------
// Batch send
// ---------------------------------------------------------------------------

// BatchItem holds one message in a batch request.
type BatchItem struct {
	To      string
	Text    string
	SendAt  *time.Time
	Options []engine.SendOptions
}

// BatchResult holds the outcome for one item in a batch.
type BatchResult struct {
	Index   int
	Message *Message
	Error   string
}

// BatchSendText sends multiple text messages, each independently scheduled or immediate.
func (s *Service) BatchSendText(ctx context.Context, instanceID string, items []BatchItem) []BatchResult {
	results := make([]BatchResult, len(items))
	for i, item := range items {
		msg, err := s.SendText(ctx, instanceID, item.To, item.Text, item.SendAt, item.Options...)
		if err != nil {
			results[i] = BatchResult{Index: i, Error: err.Error()}
		} else {
			results[i] = BatchResult{Index: i, Message: msg}
		}
	}
	return results
}

// ---------------------------------------------------------------------------
// Scheduling
// ---------------------------------------------------------------------------

// CancelScheduled marks a scheduled message as cancelled.
// Returns an error if the message is not found, not owned by instanceID, or not scheduled.
func (s *Service) CancelScheduled(ctx context.Context, instanceID, msgID string) error {
	msg, err := s.repo.GetByID(ctx, msgID)
	if err != nil {
		return err
	}
	if msg.InstanceID != instanceID {
		return fmt.Errorf("message not found")
	}
	if msg.Status != StatusScheduled {
		return fmt.Errorf("message is not scheduled (status: %s)", msg.Status)
	}
	return s.repo.UpdateAfterSend(ctx, msgID, "", StatusCancelled, nil, "")
}

// ListScheduled returns pending scheduled messages for an instance.
func (s *Service) ListScheduled(ctx context.Context, instanceID string, limit, offset int) ([]*Message, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	return s.repo.ListScheduledByInstance(ctx, instanceID, limit, offset)
}

// ---------------------------------------------------------------------------
// Query
// ---------------------------------------------------------------------------

// ListByChat returns recent messages for a given chat.
func (s *Service) ListByChat(ctx context.Context, instanceID, chatJID string, limit, offset int) ([]*Message, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	return s.repo.ListByChat(ctx, instanceID, chatJID, limit, offset)
}

// GetByID retrieves a single message by its internal UUID (must belong to instanceID).
func (s *Service) GetByID(ctx context.Context, instanceID, msgID string) (*Message, error) {
	msg, err := s.repo.GetByID(ctx, msgID)
	if err != nil {
		return nil, err
	}
	if msg.InstanceID != instanceID {
		return nil, fmt.Errorf("message not found")
	}
	return msg, nil
}

// SendLocation sends a GPS location pin message.
func (s *Service) SendLocation(ctx context.Context, instanceID, to string, lat, lng float64, name, address string) (*Message, error) {
	sent, err := s.engine.SendLocation(ctx, instanceID, to, lat, lng, name, address)
	if err != nil {
		return nil, err
	}
	msg, err := s.repo.Create(ctx, &Message{
		InstanceID:        instanceID,
		WhatsAppMessageID: sent.ID,
		Direction:         DirectionOutbound,
		Status:            StatusSent,
		Type:              "location",
		ToJID:             to,
		ChatJID:           to,
		Content:           map[string]any{"lat": lat, "lng": lng, "name": name, "address": address},
	})
	if err != nil {
		s.log.Warn().Err(err).Msg("Failed to persist location message")
		return &Message{WhatsAppMessageID: sent.ID, Status: StatusSent, Type: "location"}, nil
	}
	metrics.M.MessagesSent.Add(1)
	return msg, nil
}

// SendPoll sends an interactive poll message.
func (s *Service) SendPoll(ctx context.Context, instanceID, to, question string, options []string, multiSelect bool) (*Message, error) {
	sent, err := s.engine.SendPoll(ctx, instanceID, to, question, options, multiSelect)
	if err != nil {
		return nil, err
	}
	msg, err := s.repo.Create(ctx, &Message{
		InstanceID:        instanceID,
		WhatsAppMessageID: sent.ID,
		Direction:         DirectionOutbound,
		Status:            StatusSent,
		Type:              "poll",
		ToJID:             to,
		ChatJID:           to,
		Content:           map[string]any{"question": question, "options": options, "multi_select": multiSelect},
	})
	if err != nil {
		s.log.Warn().Err(err).Msg("Failed to persist poll message")
		return &Message{WhatsAppMessageID: sent.ID, Status: StatusSent, Type: "poll"}, nil
	}
	metrics.M.MessagesSent.Add(1)
	return msg, nil
}

// SendContact sends one or more contact vCards.
func (s *Service) SendContact(ctx context.Context, instanceID, to string, contacts []engine.ContactCard) (*Message, error) {
	sent, err := s.engine.SendContact(ctx, instanceID, to, contacts)
	if err != nil {
		return nil, err
	}
	cards := make([]map[string]any, len(contacts))
	for i, c := range contacts {
		cards[i] = map[string]any{"name": c.DisplayName, "phone": c.Phone}
	}
	msg, err := s.repo.Create(ctx, &Message{
		InstanceID:        instanceID,
		WhatsAppMessageID: sent.ID,
		Direction:         DirectionOutbound,
		Status:            StatusSent,
		Type:              "contact",
		ToJID:             to,
		ChatJID:           to,
		Content:           map[string]any{"contacts": cards},
	})
	if err != nil {
		s.log.Warn().Err(err).Msg("Failed to persist contact message")
		return &Message{WhatsAppMessageID: sent.ID, Status: StatusSent, Type: "contact"}, nil
	}
	metrics.M.MessagesSent.Add(1)
	return msg, nil
}

// SendStatusUpdate posts a WhatsApp Status (Story) update for an instance.
// Status updates are not persisted as chat messages.
func (s *Service) SendStatusUpdate(ctx context.Context, instanceID string, payload engine.StatusPayload) (engine.SentMessage, error) {
	return s.engine.SendStatus(ctx, instanceID, payload)
}

// Search finds messages matching a text query within an instance.
func (s *Service) Search(ctx context.Context, instanceID, query string, from, to *time.Time, limit, offset int) ([]*Message, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	return s.repo.Search(ctx, instanceID, query, from, to, limit, offset)
}

// ---------------------------------------------------------------------------
// Scheduler worker
// ---------------------------------------------------------------------------

// StartSchedulerWorker runs a background goroutine that polls for scheduled
// messages whose send time has arrived and dispatches them. It stops when ctx
// is cancelled.
func (s *Service) StartSchedulerWorker(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		s.log.Info().Msg("Scheduler worker started")
		for {
			select {
			case <-ctx.Done():
				s.log.Info().Msg("Scheduler worker stopped")
				return
			case <-ticker.C:
				s.processScheduled(ctx)
			}
		}
	}()
}

func (s *Service) processScheduled(ctx context.Context) {
	msgs, err := s.repo.ListScheduledReady(ctx, 50)
	if err != nil {
		s.log.Error().Err(err).Msg("scheduler: failed to list ready messages")
		return
	}
	for _, msg := range msgs {
		s.dispatchScheduled(ctx, msg)
	}
}

func (s *Service) dispatchScheduled(ctx context.Context, msg *Message) {
	var waID, errMsg string
	var sentAt *time.Time
	status := StatusFailed

	switch msg.Type {
	case "text":
		text, _ := msg.Content["text"].(string)
		var opts []engine.SendOptions
		if qid, _ := msg.Content["quoted_id"].(string); qid != "" {
			opts = append(opts, engine.SendOptions{QuotedMessageID: qid})
		}
		sent, err := s.engine.SendText(ctx, msg.InstanceID, msg.ToJID, text, opts...)
		if err != nil {
			errMsg = err.Error()
		} else {
			waID = sent.ID
			t := sent.Timestamp
			sentAt = &t
			status = StatusSent
		}

	case "image", "video", "audio", "document", "sticker":
		b64, _ := msg.Content["data_b64"].(string)
		data, err := base64.StdEncoding.DecodeString(b64)
		if err != nil {
			errMsg = fmt.Sprintf("failed to decode stored media: %v", err)
			break
		}
		payload := engine.MediaPayload{
			Type:     engine.MediaType(msg.Type),
			Data:     data,
			MimeType: strOrEmpty(msg.Content["mime_type"]),
			FileName: strOrEmpty(msg.Content["file_name"]),
			Caption:  strOrEmpty(msg.Content["caption"]),
		}
		sent, err := s.engine.SendMedia(ctx, msg.InstanceID, msg.ToJID, payload)
		if err != nil {
			errMsg = err.Error()
		} else {
			waID = sent.ID
			t := sent.Timestamp
			sentAt = &t
			status = StatusSent
		}

	default:
		errMsg = fmt.Sprintf("unsupported scheduled message type: %s", msg.Type)
	}

	if err := s.repo.UpdateAfterSend(ctx, msg.ID, waID, status, sentAt, errMsg); err != nil {
		s.log.Error().Err(err).Str("msg_id", msg.ID).Msg("scheduler: failed to update message after send")
	} else if status == StatusSent {
		metrics.M.MessagesSent.Add(1)
		s.log.Info().Str("msg_id", msg.ID).Str("wa_id", waID).Msg("scheduler: dispatched scheduled message")
	} else {
		s.log.Warn().Str("msg_id", msg.ID).Str("error", errMsg).Msg("scheduler: failed to send scheduled message")
	}
}

// ---------------------------------------------------------------------------
// Engine event handler — persists inbound messages and updates delivery status
// ---------------------------------------------------------------------------

func (s *Service) handleEngineEvent(evt engine.Event) {
	ctx := context.Background()

	switch evt.Type {
	case engine.EventInstanceConnected:
		metrics.M.InstancesConnected.Add(1)
	case engine.EventInstanceDisconnected, engine.EventInstanceBanned:
		if v := metrics.M.InstancesConnected.Load(); v > 0 {
			metrics.M.InstancesConnected.Add(-1)
		}

	case engine.EventMessageReceived:
		p, ok := evt.Payload.(*engine.MessagePayload)
		if !ok {
			return
		}
		s.persistInbound(ctx, evt.InstanceID, p)

	case engine.EventReceiptDelivered:
		p, ok := evt.Payload.(*engine.ReceiptPayload)
		if !ok {
			return
		}
		if err := s.repo.UpdateStatusBulk(ctx, evt.InstanceID, p.MessageIDs, StatusDelivered); err != nil {
			s.log.Error().Err(err).Msg("Failed to mark messages as delivered")
		}

	case engine.EventReceiptRead:
		p, ok := evt.Payload.(*engine.ReceiptPayload)
		if !ok {
			return
		}
		if err := s.repo.UpdateStatusBulk(ctx, evt.InstanceID, p.MessageIDs, StatusRead); err != nil {
			s.log.Error().Err(err).Msg("Failed to mark messages as read")
		}
	}
}

func (s *Service) persistInbound(ctx context.Context, instanceID string, p *engine.MessagePayload) {
	content := map[string]any{"text": p.Text}
	if p.Media != nil {
		content["mime_type"] = p.Media.MimeType
		content["file_name"] = p.Media.FileName
		content["caption"] = p.Media.Caption
		content["size"] = p.Media.Size
	}

	ts := p.Timestamp
	msg := &Message{
		InstanceID:        instanceID,
		WhatsAppMessageID: p.ID,
		Direction:         DirectionInbound,
		Status:            StatusDelivered,
		FromJID:           p.From,
		ChatJID:           p.Chat,
		IsGroup:           p.IsGroup,
		Type:              p.Type,
		Content:           content,
		SentAt:            &ts,
		DeliveredAt:       timePtr(time.Now()),
	}

	if _, err := s.repo.Create(ctx, msg); err != nil {
		s.log.Error().Err(err).Str("wa_id", p.ID).Msg("Failed to persist inbound message")
	} else {
		metrics.M.MessagesInbound.Add(1)
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func (s *Service) scheduleText(ctx context.Context, instanceID, to, text string, sendAt *time.Time, opts ...engine.SendOptions) (*Message, error) {
	msg := &Message{
		InstanceID:  instanceID,
		Direction:   DirectionOutbound,
		Status:      StatusScheduled,
		ToJID:       to,
		ChatJID:     to,
		Type:        "text",
		Content:     map[string]any{"text": text},
		ScheduledAt: sendAt,
	}
	if len(opts) > 0 && opts[0].QuotedMessageID != "" {
		msg.Content["quoted_id"] = opts[0].QuotedMessageID
	}
	saved, err := s.repo.Create(ctx, msg)
	if err == nil {
		metrics.M.MessagesScheduled.Add(1)
	}
	return saved, err
}

// maxScheduledMediaBytes is the max file size we'll store in JSONB for scheduled media.
// Larger files should use a pre-uploaded URL; this prevents accidental DB bloat.
const maxScheduledMediaBytes = 10 << 20 // 10 MiB

func (s *Service) scheduleMedia(ctx context.Context, instanceID, to string, sendAt *time.Time, payload engine.MediaPayload) (*Message, error) {
	if len(payload.Data) > maxScheduledMediaBytes {
		return nil, fmt.Errorf("media too large for scheduling (%d bytes > %d limit); upload the file first and send by URL", len(payload.Data), maxScheduledMediaBytes)
	}
	msg := &Message{
		InstanceID:  instanceID,
		Direction:   DirectionOutbound,
		Status:      StatusScheduled,
		ToJID:       to,
		ChatJID:     to,
		Type:        string(payload.Type),
		Content: map[string]any{
			"mime_type": payload.MimeType,
			"file_name": payload.FileName,
			"caption":   payload.Caption,
			"data_b64":  base64.StdEncoding.EncodeToString(payload.Data),
		},
		ScheduledAt: sendAt,
	}
	saved, err := s.repo.Create(ctx, msg)
	if err == nil {
		metrics.M.MessagesScheduled.Add(1)
	}
	return saved, err
}

func (s *Service) sendTextNow(ctx context.Context, instanceID, to, text string, opts ...engine.SendOptions) (*Message, error) {
	sent, err := s.engine.SendText(ctx, instanceID, to, text, opts...)
	if err != nil {
		return nil, fmt.Errorf("engine send text: %w", err)
	}
	now := sent.Timestamp
	msg := &Message{
		InstanceID:        instanceID,
		WhatsAppMessageID: sent.ID,
		Direction:         DirectionOutbound,
		Status:            StatusSent,
		ToJID:             to,
		ChatJID:           to,
		Type:              "text",
		Content:           map[string]any{"text": text},
		SentAt:            &now,
	}
	if len(opts) > 0 && opts[0].QuotedMessageID != "" {
		msg.Content["quoted_id"] = opts[0].QuotedMessageID
	}
	saved, err := s.repo.Create(ctx, msg)
	if err != nil {
		s.log.Error().Err(err).Str("wa_id", sent.ID).Msg("Failed to persist outbound text message")
		return msg, nil
	}
	metrics.M.MessagesSent.Add(1)
	return saved, nil
}

func (s *Service) sendMediaNow(ctx context.Context, instanceID, to string, payload engine.MediaPayload, opts ...engine.SendOptions) (*Message, error) {
	sent, err := s.engine.SendMedia(ctx, instanceID, to, payload, opts...)
	if err != nil {
		return nil, fmt.Errorf("engine send media: %w", err)
	}
	now := sent.Timestamp
	msg := &Message{
		InstanceID:        instanceID,
		WhatsAppMessageID: sent.ID,
		Direction:         DirectionOutbound,
		Status:            StatusSent,
		ToJID:             to,
		ChatJID:           to,
		Type:              string(payload.Type),
		Content: map[string]any{
			"mime_type": payload.MimeType,
			"file_name": payload.FileName,
			"caption":   payload.Caption,
			"size":      len(payload.Data),
		},
		SentAt: &now,
	}
	saved, err := s.repo.Create(ctx, msg)
	if err != nil {
		s.log.Error().Err(err).Str("wa_id", sent.ID).Msg("Failed to persist outbound media message")
		return msg, nil
	}
	metrics.M.MessagesSent.Add(1)
	return saved, nil
}

func timePtr(t time.Time) *time.Time { return &t }

func strOrEmpty(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}
