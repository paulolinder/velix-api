// Package message contains the domain model and business logic for WhatsApp messages.
package message

import "time"

// Direction indicates whether a message was sent or received.
type Direction string

const (
	DirectionInbound  Direction = "inbound"
	DirectionOutbound Direction = "outbound"
)

// MessageStatus tracks delivery lifecycle of a message.
type MessageStatus string

const (
	StatusPending   MessageStatus = "pending"
	StatusScheduled MessageStatus = "scheduled"
	StatusQueued    MessageStatus = "queued"
	StatusSent      MessageStatus = "sent"
	StatusDelivered MessageStatus = "delivered"
	StatusRead      MessageStatus = "read"
	StatusFailed    MessageStatus = "failed"
	StatusRevoked   MessageStatus = "revoked"
	StatusCancelled MessageStatus = "cancelled"
)

// Message represents a single WhatsApp message (inbound or outbound).
type Message struct {
	ID                string
	InstanceID        string
	WhatsAppMessageID string
	Direction         Direction
	Status            MessageStatus
	FromJID           string
	ToJID             string
	ChatJID           string
	IsGroup           bool
	Type              string // text | image | video | audio | document | sticker | reaction
	Content           map[string]any
	ErrorMessage      string
	ScheduledAt       *time.Time
	SentAt            *time.Time
	DeliveredAt       *time.Time
	ReadAt            *time.Time
	CreatedAt         time.Time
}
