// Package message contains HTTP handlers for the /v1/instances/{id}/messages resource.
package message

import (
	"time"

	"velix/internal/domain/message"
)

// SendTextRequest is the body for POST /v1/instances/{id}/messages/text.
type SendTextRequest struct {
	To              string     `json:"to"`
	Text            string     `json:"text"`
	QuotedMessageID string     `json:"quoted_id,omitempty"`
	SendAt          *time.Time `json:"send_at,omitempty"` // RFC3339; omit for immediate send
}

// SendMediaRequest is the body for POST /v1/instances/{id}/messages/media.
type SendMediaRequest struct {
	To       string     `json:"to"`
	Type     string     `json:"type"`     // image | video | audio | document | sticker
	DataB64  string     `json:"data"`     // base64-encoded file bytes
	MimeType string     `json:"mime_type"`
	FileName string     `json:"file_name,omitempty"`
	Caption  string     `json:"caption,omitempty"`
	SendAt   *time.Time `json:"send_at,omitempty"`
}

// BatchTextItem is one entry in a batch send request.
type BatchTextItem struct {
	To     string     `json:"to"`
	Text   string     `json:"text"`
	SendAt *time.Time `json:"send_at,omitempty"`
}

// BatchTextRequest is the body for POST /v1/instances/{id}/messages/batch.
type BatchTextRequest struct {
	Messages []BatchTextItem `json:"messages"`
}

// BatchResultItem is the outcome for one item in a batch response.
type BatchResultItem struct {
	Index   int              `json:"index"`
	Message *MessageResponse `json:"message,omitempty"`
	Error   string           `json:"error,omitempty"`
}

// SendReactionRequest is the body for POST /v1/instances/{id}/messages/reaction.
type SendReactionRequest struct {
	To        string `json:"to"`
	MessageID string `json:"message_id"`
	Reaction  string `json:"reaction"` // emoji or "" to remove
}

// RevokeRequest is the body for DELETE /v1/instances/{id}/messages/{msgID}.
type RevokeRequest struct {
	To string `json:"to"` // chat JID where the message lives
}

// MarkReadRequest is the body for POST /v1/instances/{id}/messages/read.
type MarkReadRequest struct {
	Chat       string   `json:"chat"`
	MessageIDs []string `json:"message_ids"`
}

// SendLocationRequest is the body for POST /messages/location.
type SendLocationRequest struct {
	To      string  `json:"to"`
	Lat     float64 `json:"lat"`
	Lng     float64 `json:"lng"`
	Name    string  `json:"name,omitempty"`
	Address string  `json:"address,omitempty"`
}

// SendPollRequest is the body for POST /messages/poll.
type SendPollRequest struct {
	To          string   `json:"to"`
	Question    string   `json:"question"`
	Options     []string `json:"options"`
	MultiSelect bool     `json:"multi_select,omitempty"`
}

// ContactCardRequest is a single vCard entry in SendContactRequest.
type ContactCardRequest struct {
	Name  string `json:"name"`
	Phone string `json:"phone"`
}

// SendContactRequest is the body for POST /messages/contact.
type SendContactRequest struct {
	To       string               `json:"to"`
	Contacts []ContactCardRequest `json:"contacts"`
}

// SendStatusRequest is the body for POST /messages/status.
// Type must be "text", "image", or "video".
// For "text": Caption is the text, BackgroundColor is an optional hex color (e.g. "#FF0000").
// For "image"/"video": Data is base64-encoded file bytes, MimeType is required.
type SendStatusRequest struct {
	Type            string `json:"type"`                       // text | image | video
	Caption         string `json:"caption,omitempty"`          // text content or media caption
	BackgroundColor string `json:"background_color,omitempty"` // hex color for text status, e.g. "#000000"
	FontType        int32  `json:"font_type,omitempty"`        // 0=SansSerif 1=Serif 2=Norican 3=Bryndan 4=Bebas 5=Oswald
	DataB64         string `json:"data,omitempty"`             // base64-encoded image or video
	MimeType        string `json:"mime_type,omitempty"`        // required for image/video
}

// MessageResponse is the JSON shape of a message.
type MessageResponse struct {
	ID                string         `json:"id"`
	InstanceID        string         `json:"instance_id"`
	WhatsAppMessageID string         `json:"whatsapp_message_id,omitempty"`
	Direction         string         `json:"direction"`
	Status            string         `json:"status"`
	FromJID           string         `json:"from_jid,omitempty"`
	ToJID             string         `json:"to_jid,omitempty"`
	ChatJID           string         `json:"chat_jid"`
	IsGroup           bool           `json:"is_group"`
	Type              string         `json:"type"`
	Content           map[string]any `json:"content"`
	ScheduledAt       *time.Time     `json:"scheduled_at,omitempty"`
	SentAt            *time.Time     `json:"sent_at,omitempty"`
	DeliveredAt       *time.Time     `json:"delivered_at,omitempty"`
	ReadAt            *time.Time     `json:"read_at,omitempty"`
	CreatedAt         time.Time      `json:"created_at"`
}

func fromDomain(msg *message.Message) *MessageResponse {
	content := msg.Content
	// Strip stored base64 data from API responses (it's internal scheduler data)
	if _, ok := content["data_b64"]; ok {
		clean := make(map[string]any, len(content))
		for k, v := range content {
			if k != "data_b64" {
				clean[k] = v
			}
		}
		content = clean
	}
	return &MessageResponse{
		ID:                msg.ID,
		InstanceID:        msg.InstanceID,
		WhatsAppMessageID: msg.WhatsAppMessageID,
		Direction:         string(msg.Direction),
		Status:            string(msg.Status),
		FromJID:           msg.FromJID,
		ToJID:             msg.ToJID,
		ChatJID:           msg.ChatJID,
		IsGroup:           msg.IsGroup,
		Type:              msg.Type,
		Content:           content,
		ScheduledAt:       msg.ScheduledAt,
		SentAt:            msg.SentAt,
		DeliveredAt:       msg.DeliveredAt,
		ReadAt:            msg.ReadAt,
		CreatedAt:         msg.CreatedAt,
	}
}
