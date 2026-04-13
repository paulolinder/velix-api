package engine

import "time"

// InstanceStatus represents the current state of a WhatsApp instance.
type InstanceStatus string

const (
	StatusDisconnected InstanceStatus = "disconnected"
	StatusConnecting   InstanceStatus = "connecting"
	StatusQRPending    InstanceStatus = "qr_pending"
	StatusConnected    InstanceStatus = "connected"
	StatusLoggedOut    InstanceStatus = "logged_out"
	StatusBanned       InstanceStatus = "banned"
)

// InstanceOptions holds optional settings when creating an instance.
type InstanceOptions struct {
	ProxyURL string
}

// InstanceSettings holds per-instance behavioral settings that the engine acts on.
type InstanceSettings struct {
	RejectCall         bool
	ReadMessages       bool
	AlwaysOnline       bool
	IgnoreGroups       bool
	SyncFullHistory    bool
	HumanPauseDuration int // seconds to pause after a FromMe message; 0 = disabled
}

// QREvent is emitted on the QR code channel.
// Code is empty and Error is non-nil when the QR expires or fails.
type QREvent struct {
	Code    string
	Timeout time.Duration
	Error   error
}

// SentMessage is returned after a successful send.
type SentMessage struct {
	ID        string    `json:"id"`
	Timestamp time.Time `json:"timestamp"`
}

// SendOptions are optional parameters for send methods.
type SendOptions struct {
	QuotedMessageID string
	MentionedJIDs   []string
}

// MediaType enumerates supported media categories.
type MediaType string

const (
	MediaTypeImage    MediaType = "image"
	MediaTypeVideo    MediaType = "video"
	MediaTypeAudio    MediaType = "audio"
	MediaTypeDocument MediaType = "document"
	MediaTypeSticker  MediaType = "sticker"
)

// MediaPayload carries a media file for sending.
type MediaPayload struct {
	Type     MediaType
	Data     []byte
	FileName string
	MimeType string
	Caption  string
}

// ContactCard holds a vCard for sending as a contact message.
type ContactCard struct {
	DisplayName string
	Phone       string
	// Optional vCard fields
	Organization string
	Email        string
}

// ContactCheck is the result of an IsOnWhatsApp query.
type ContactCheck struct {
	Phone  string
	JID    string
	Exists bool
}

// ContactInfo holds basic info about a WhatsApp contact.
type ContactInfo struct {
	JID          string
	PushName     string
	BusinessName string
	About        string
	PictureURL   string
}

// GroupParticipant holds a participant inside a GroupInfo.
type GroupParticipant struct {
	JID   string
	Admin bool
}

// GroupInfo holds metadata about a WhatsApp group.
type GroupInfo struct {
	JID          string
	Name         string
	Topic        string
	Participants []GroupParticipant
	IsAnnounce   bool
	IsLocked     bool
	CreatedAt    time.Time
}

// CreateGroupRequest is the input for Engine.CreateGroup.
type CreateGroupRequest struct {
	Name         string
	Participants []string
}

// ---------------------------------------------------------------------------
// Events
// ---------------------------------------------------------------------------

// EventType identifies the kind of event emitted by the engine.
type EventType string

const (
	EventMessageReceived     EventType = "messages.received"
	EventMessageSent         EventType = "messages.sent"
	EventReceiptDelivered    EventType = "receipts.delivered"
	EventReceiptRead         EventType = "receipts.read"
	EventInstanceConnected   EventType = "instance.connected"
	EventInstanceDisconnected EventType = "instance.disconnected"
	EventInstanceQRUpdated   EventType = "instance.qr_updated"
	EventInstanceLoggedOut   EventType = "instance.logged_out"
	EventInstanceBanned      EventType = "instance.banned"
	EventInstancePaired      EventType = "instance.paired"
	EventHistorySync         EventType = "history.sync"
)

// Event is the generic envelope for all engine events.
// Payload is one of the concrete *Payload types below.
type Event struct {
	Type       EventType
	InstanceID string
	Payload    any
	Timestamp  time.Time
}

// EventHandler is the callback signature for engine subscribers.
type EventHandler func(event Event)

// ---------------------------------------------------------------------------
// Event payload types
// ---------------------------------------------------------------------------

// MessageSource identifies who originated the message.
type MessageSource string

const (
	// MessageSourceContact — received from a contact (client).
	MessageSourceContact MessageSource = "contact"
	// MessageSourceAPI — sent programmatically via our WhatsApp API.
	MessageSourceAPI MessageSource = "api"
	// MessageSourceManual — sent by an operator directly from the phone or WhatsApp Web.
	MessageSourceManual MessageSource = "manual"
)

// MessagePayload is the payload for EventMessageReceived.
type MessagePayload struct {
	ID     string `json:"id"`
	From   string `json:"from"`
	Chat   string `json:"chat"`
	FromMe bool   `json:"from_me"`
	// Source distinguishes the message origin:
	//   "contact" — incoming from a client
	//   "api"     — sent by this system via the API
	//   "manual"  — sent by an operator from phone/WhatsApp Web
	Source  MessageSource `json:"source"`
	IsGroup bool          `json:"is_group"`
	Type    string        `json:"type"` // text | image | video | audio | document | sticker | location | contact | reaction
	Text    string        `json:"text,omitempty"`
	// Quoted message context (populated when the sender replied to a message).
	QuotedID   string `json:"quoted_id,omitempty"`
	QuotedText string `json:"quoted_text,omitempty"`
	QuotedFrom string `json:"quoted_from,omitempty"`
	// Mentions contains the JIDs @-mentioned in a text or caption.
	Mentions []string `json:"mentions,omitempty"`
	// IsForwarded is true when WhatsApp flagged the message as forwarded.
	IsForwarded bool `json:"is_forwarded,omitempty"`
	// ReactionTarget is the message ID that received a reaction (Type == "reaction").
	ReactionTarget string `json:"reaction_target,omitempty"`
	// Paused is true when this chat is under a human-pause window
	// (an operator or API replied recently and automated replies should be suppressed).
	Paused    bool          `json:"paused,omitempty"`
	Timestamp time.Time     `json:"timestamp"`
	PushName  string        `json:"push_name,omitempty"`
	Media     *MediaInfo    `json:"media,omitempty"`
	Location  *LocationInfo `json:"location,omitempty"`
	VCard     *VCardInfo    `json:"vcard,omitempty"`
}

// MediaInfo describes media attached to an inbound message.
type MediaInfo struct {
	MimeType        string `json:"mime_type"`
	FileName        string `json:"file_name,omitempty"`
	Caption         string `json:"caption,omitempty"`
	Size            uint64 `json:"size"`
	DurationSeconds uint32 `json:"duration_seconds,omitempty"` // audio and video
	IsVoiceNote     bool   `json:"is_voice_note,omitempty"`    // true for PTT (push-to-talk) voice messages
	DirectURL       string `json:"direct_url,omitempty"`       // local URL to download via GET /v1/media/{id}
	LocalPath       string `json:"local_path,omitempty"`       // server-side file path after download
}

// LocationInfo holds coordinates and metadata for a location message.
type LocationInfo struct {
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
	Name      string  `json:"name,omitempty"`
	Address   string  `json:"address,omitempty"`
	IsLive    bool    `json:"is_live,omitempty"`
}

// VCardInfo holds a received contact card message.
type VCardInfo struct {
	DisplayName string `json:"display_name"`
	VCard       string `json:"vcard"`
}

// HistorySyncMessage represents a single message extracted from a WhatsApp history sync blob.
type HistorySyncMessage struct {
	ChatJID   string    `json:"chat_jid"`
	SenderJID string    `json:"sender_jid"`
	FromMe    bool      `json:"from_me"`
	MessageID string    `json:"message_id"`
	Text      string    `json:"text"`
	Timestamp time.Time `json:"timestamp"`
	PushName  string    `json:"push_name"`
	Type      string    `json:"type"` // text | image | video | audio | document | location | contact | etc.
}

// HistorySyncPayload is the payload for EventHistorySync.
type HistorySyncPayload struct {
	Messages []HistorySyncMessage `json:"messages"`
}

// ReceiptPayload is the payload for EventReceiptDelivered / EventReceiptRead.
type ReceiptPayload struct {
	MessageIDs []string  `json:"message_ids"`
	Chat       string    `json:"chat"`
	Sender     string    `json:"sender"`
	Type       string    `json:"type"` // delivered | read | played
	Timestamp  time.Time `json:"timestamp"`
}

// QRPayload is the payload for EventInstanceQRUpdated.
type QRPayload struct {
	Code    string        `json:"code"`
	Timeout time.Duration `json:"timeout"`
}

// PairPayload is the payload for EventInstancePaired (after successful QR/code pairing).
type PairPayload struct {
	JID          string `json:"jid"`           // WhatsApp JID, e.g. 5511999990001@s.whatsapp.net
	Platform     string `json:"platform,omitempty"`      // e.g. iPhone, Android
	BusinessName string `json:"business_name,omitempty"`
}
