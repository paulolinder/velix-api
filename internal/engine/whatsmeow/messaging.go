package waengine

import (
	"context"
	"fmt"
	"math/rand"
	"strings"
	"time"

	"go.mau.fi/whatsmeow"
	waCommon "go.mau.fi/whatsmeow/proto/waCommon"
	waProto "go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"google.golang.org/protobuf/proto"

	"velix/internal/engine"
)

// waitForRateLimit blocks until the per-instance message rate limiter allows.
// Returns an error if the context is cancelled while waiting.
func waitForRateLimit(ctx context.Context, mi *managedInstance) error {
	return mi.msgLimiter.Wait(ctx)
}

// simulateTyping sends a "composing" chat state (typing indicator) to the
// recipient, waits a realistic random duration, then sends "paused".
// This mimics human behavior and helps avoid WhatsApp anti-spam detection.
func (e *Engine) simulateTyping(ctx context.Context, mi *managedInstance, jid types.JID) {
	// Send presence "available" so the recipient sees us online.
	_ = mi.client.SendPresence(ctx, types.PresenceAvailable)

	// Send "composing" indicator.
	_ = mi.client.SendChatPresence(ctx, jid, types.ChatPresenceComposing, types.ChatPresenceMediaText)

	// Random delay between 1–3 seconds to simulate typing.
	delay := time.Duration(1000+rand.Intn(2000)) * time.Millisecond
	select {
	case <-time.After(delay):
	case <-ctx.Done():
		return
	}

	// Clear composing state.
	_ = mi.client.SendChatPresence(ctx, jid, types.ChatPresencePaused, types.ChatPresenceMediaText)
}

// ---------------------------------------------------------------------------
// SendText
// ---------------------------------------------------------------------------

func (e *Engine) SendText(ctx context.Context, instanceID, to, text string, opts ...engine.SendOptions) (engine.SentMessage, error) {
	mi, err := e.getInstance(instanceID)
	if err != nil {
		return engine.SentMessage{}, err
	}
	if s := mi.getStatus(); s == engine.StatusBanned {
		return engine.SentMessage{}, fmt.Errorf("instance is banned — cannot send messages")
	}

	jid, err := parseJID(to)
	if err != nil {
		return engine.SentMessage{}, fmt.Errorf("invalid JID %q: %w", to, err)
	}

	if err := waitForRateLimit(ctx, mi); err != nil {
		return engine.SentMessage{}, fmt.Errorf("rate limit: %w", err)
	}

	opt := mergeOpts(opts)

	var msg *waProto.Message
	if opt.QuotedMessageID != "" {
		msg = &waProto.Message{
			ExtendedTextMessage: &waProto.ExtendedTextMessage{
				Text: proto.String(text),
				ContextInfo: &waProto.ContextInfo{
					StanzaID:      proto.String(opt.QuotedMessageID),
					Participant:   proto.String(to),
					QuotedMessage: &waProto.Message{Conversation: proto.String("")},
				},
			},
		}
	} else {
		msg = &waProto.Message{Conversation: proto.String(text)}
	}

	// Simulate typing to avoid anti-spam detection.
	e.simulateTyping(ctx, mi, jid)

	resp, err := mi.client.SendMessage(ctx, jid, msg)
	if err != nil {
		return engine.SentMessage{}, fmt.Errorf("send text: %w", err)
	}

	e.markAPISent(resp.ID)
	return engine.SentMessage{ID: resp.ID, Timestamp: resp.Timestamp}, nil
}

// ---------------------------------------------------------------------------
// SendMedia
// ---------------------------------------------------------------------------

func (e *Engine) SendMedia(ctx context.Context, instanceID, to string, payload engine.MediaPayload, opts ...engine.SendOptions) (engine.SentMessage, error) {
	mi, err := e.getInstance(instanceID)
	if err != nil {
		return engine.SentMessage{}, err
	}
	if s := mi.getStatus(); s == engine.StatusBanned {
		return engine.SentMessage{}, fmt.Errorf("instance is banned — cannot send messages")
	}

	jid, err := parseJID(to)
	if err != nil {
		return engine.SentMessage{}, fmt.Errorf("invalid JID %q: %w", to, err)
	}

	if err := waitForRateLimit(ctx, mi); err != nil {
		return engine.SentMessage{}, fmt.Errorf("rate limit: %w", err)
	}

	waType, err := toWAMediaType(payload.Type)
	if err != nil {
		return engine.SentMessage{}, err
	}

	uploaded, err := mi.client.Upload(ctx, payload.Data, waType)
	if err != nil {
		return engine.SentMessage{}, fmt.Errorf("upload %s: %w", payload.Type, err)
	}

	msg, err := buildMediaMessage(payload, uploaded)
	if err != nil {
		return engine.SentMessage{}, err
	}

	// Simulate typing to avoid anti-spam detection.
	e.simulateTyping(ctx, mi, jid)

	resp, err := mi.client.SendMessage(ctx, jid, msg)
	if err != nil {
		return engine.SentMessage{}, fmt.Errorf("send media: %w", err)
	}

	e.markAPISent(resp.ID)
	return engine.SentMessage{ID: resp.ID, Timestamp: resp.Timestamp}, nil
}

// ---------------------------------------------------------------------------
// SendReaction
// ---------------------------------------------------------------------------

func (e *Engine) SendReaction(ctx context.Context, instanceID, to, messageID, reaction string) error {
	mi, err := e.getInstance(instanceID)
	if err != nil {
		return err
	}

	jid, err := parseJID(to)
	if err != nil {
		return fmt.Errorf("invalid JID %q: %w", to, err)
	}

	msg := &waProto.Message{
		ReactionMessage: &waProto.ReactionMessage{
			Key: &waCommon.MessageKey{
				RemoteJID: proto.String(jid.String()),
				FromMe:    proto.Bool(false),
				ID:        proto.String(messageID),
			},
			Text:              proto.String(reaction),
			SenderTimestampMS: proto.Int64(time.Now().UnixMilli()),
		},
	}

	resp, err := mi.client.SendMessage(ctx, jid, msg)
	if err != nil {
		return err
	}
	e.markAPISent(resp.ID)
	return nil
}

// ---------------------------------------------------------------------------
// RevokeMessage
// ---------------------------------------------------------------------------

func (e *Engine) RevokeMessage(ctx context.Context, instanceID, to, messageID string) error {
	mi, err := e.getInstance(instanceID)
	if err != nil {
		return err
	}

	jid, err := parseJID(to)
	if err != nil {
		return fmt.Errorf("invalid JID %q: %w", to, err)
	}

	_, err = mi.client.SendMessage(ctx, jid,
		mi.client.BuildRevoke(jid, types.EmptyJID, types.MessageID(messageID)),
	)
	return err
}

// ---------------------------------------------------------------------------
// MarkAsRead
// ---------------------------------------------------------------------------

func (e *Engine) MarkAsRead(ctx context.Context, instanceID, chat string, messageIDs []string) error {
	mi, err := e.getInstance(instanceID)
	if err != nil {
		return err
	}

	chatJID, err := parseJID(chat)
	if err != nil {
		return fmt.Errorf("invalid chat JID %q: %w", chat, err)
	}

	ids := make([]types.MessageID, len(messageIDs))
	for i, id := range messageIDs {
		ids[i] = types.MessageID(id)
	}

	return mi.client.MarkRead(ctx, ids, time.Now(), chatJID, types.EmptyJID)
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func parseJID(s string) (types.JID, error) {
	if !strings.ContainsRune(s, '@') {
		s += "@s.whatsapp.net"
	}
	return types.ParseJID(s)
}

func mergeOpts(opts []engine.SendOptions) engine.SendOptions {
	if len(opts) > 0 {
		return opts[0]
	}
	return engine.SendOptions{}
}

func toWAMediaType(t engine.MediaType) (whatsmeow.MediaType, error) {
	switch t {
	case engine.MediaTypeImage:
		return whatsmeow.MediaImage, nil
	case engine.MediaTypeVideo:
		return whatsmeow.MediaVideo, nil
	case engine.MediaTypeAudio:
		return whatsmeow.MediaAudio, nil
	case engine.MediaTypeDocument:
		return whatsmeow.MediaDocument, nil
	case engine.MediaTypeSticker:
		return whatsmeow.MediaImage, nil // stickers use Image upload slot
	default:
		return "", fmt.Errorf("unsupported media type: %s", t)
	}
}

func buildMediaMessage(p engine.MediaPayload, u whatsmeow.UploadResponse) (*waProto.Message, error) {
	size := uint64(len(p.Data))

	switch p.Type {
	case engine.MediaTypeImage:
		return &waProto.Message{
			ImageMessage: &waProto.ImageMessage{
				Caption:       proto.String(p.Caption),
				Mimetype:      proto.String(p.MimeType),
				URL:           proto.String(u.URL),
				DirectPath:    proto.String(u.DirectPath),
				MediaKey:      u.MediaKey,
				FileEncSHA256: u.FileEncSHA256,
				FileSHA256:    u.FileSHA256,
				FileLength:    proto.Uint64(size),
			},
		}, nil

	case engine.MediaTypeVideo:
		return &waProto.Message{
			VideoMessage: &waProto.VideoMessage{
				Caption:       proto.String(p.Caption),
				Mimetype:      proto.String(p.MimeType),
				URL:           proto.String(u.URL),
				DirectPath:    proto.String(u.DirectPath),
				MediaKey:      u.MediaKey,
				FileEncSHA256: u.FileEncSHA256,
				FileSHA256:    u.FileSHA256,
				FileLength:    proto.Uint64(size),
			},
		}, nil

	case engine.MediaTypeAudio:
		return &waProto.Message{
			AudioMessage: &waProto.AudioMessage{
				Mimetype:      proto.String(p.MimeType),
				URL:           proto.String(u.URL),
				DirectPath:    proto.String(u.DirectPath),
				MediaKey:      u.MediaKey,
				FileEncSHA256: u.FileEncSHA256,
				FileSHA256:    u.FileSHA256,
				FileLength:    proto.Uint64(size),
				PTT:           proto.Bool(p.MimeType == "audio/ogg; codecs=opus"),
			},
		}, nil

	case engine.MediaTypeDocument:
		return &waProto.Message{
			DocumentMessage: &waProto.DocumentMessage{
				Title:         proto.String(p.FileName),
				FileName:      proto.String(p.FileName),
				Caption:       proto.String(p.Caption),
				Mimetype:      proto.String(p.MimeType),
				URL:           proto.String(u.URL),
				DirectPath:    proto.String(u.DirectPath),
				MediaKey:      u.MediaKey,
				FileEncSHA256: u.FileEncSHA256,
				FileSHA256:    u.FileSHA256,
				FileLength:    proto.Uint64(size),
			},
		}, nil

	case engine.MediaTypeSticker:
		return &waProto.Message{
			StickerMessage: &waProto.StickerMessage{
				Mimetype:      proto.String(p.MimeType),
				URL:           proto.String(u.URL),
				DirectPath:    proto.String(u.DirectPath),
				MediaKey:      u.MediaKey,
				FileEncSHA256: u.FileEncSHA256,
				FileSHA256:    u.FileSHA256,
				FileLength:    proto.Uint64(size),
				IsAnimated:    proto.Bool(p.MimeType == "image/webp"),
			},
		}, nil

	default:
		return nil, fmt.Errorf("unsupported media type: %s", p.Type)
	}
}
