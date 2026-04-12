// Package engine defines the core abstraction for WhatsApp protocol operations.
//
// IMPORTANT: All application code must depend on this interface.
// Never import go.mau.fi/whatsmeow (or any WhatsApp library) outside of the
// internal/engine/whatsmeow package. This isolation ensures we can swap the
// underlying library or add features without breaking the rest of the codebase.
package engine

import "context"

// Engine is the single contract between the application and the WhatsApp protocol.
// The concrete implementation lives in internal/engine/whatsmeow/.
type Engine interface {

	// --- Lifecycle ---

	// Start initialises the engine and restores any previously connected instances.
	Start(ctx context.Context) error

	// Stop gracefully disconnects all instances and releases resources.
	Stop(ctx context.Context) error

	// --- Instance management ---

	// CreateInstance registers a new WhatsApp instance identified by instanceID.
	// It does NOT connect — call Connect afterwards.
	CreateInstance(ctx context.Context, instanceID string, opts InstanceOptions) error

	// DeleteInstance disconnects and permanently removes an instance.
	DeleteInstance(ctx context.Context, instanceID string) error

	// GetStatus returns the current connection status of an instance.
	GetStatus(instanceID string) (InstanceStatus, error)

	// --- Connection ---

	// Connect initiates the WebSocket connection for an instance.
	// If the instance has a saved session it reconnects silently;
	// otherwise it transitions to StatusQRPending.
	Connect(ctx context.Context, instanceID string) error

	// Disconnect closes the WebSocket without removing the session.
	Disconnect(ctx context.Context, instanceID string) error

	// Logout revokes the WhatsApp session and resets the instance to a
	// pristine state. The user will need to scan a new QR code to reconnect.
	Logout(ctx context.Context, instanceID string) error

	// --- Authentication ---

	// GetQRChannel returns a channel of QR events for an instance that is
	// not yet authenticated.  Must be called before Connect.
	// The channel is closed after a successful pair, timeout, or error.
	GetQRChannel(ctx context.Context, instanceID string) (<-chan QREvent, error)

	// RequestPairCode requests a numeric pair code for phone-number pairing.
	// The instance must already be in the connecting state (Connect called).
	RequestPairCode(ctx context.Context, instanceID, phone string) (string, error)

	// --- Messaging ---

	// SendText sends a plain-text message to a JID (user or group).
	SendText(ctx context.Context, instanceID, to, text string, opts ...SendOptions) (SentMessage, error)

	// SendMedia uploads and sends a media message.
	SendMedia(ctx context.Context, instanceID, to string, media MediaPayload, opts ...SendOptions) (SentMessage, error)

	// SendReaction adds an emoji reaction to an existing message.
	SendReaction(ctx context.Context, instanceID, to, messageID, emoji string) error

	// RevokeMessage deletes a previously sent message for everyone.
	RevokeMessage(ctx context.Context, instanceID, to, messageID string) error

	// MarkAsRead sends read receipts for the given message IDs in a chat.
	MarkAsRead(ctx context.Context, instanceID, chat string, messageIDs []string) error

	// SendLocation sends a location pin message.
	SendLocation(ctx context.Context, instanceID, to string, lat, lng float64, name, address string) (SentMessage, error)

	// SendPoll sends an interactive poll message.
	SendPoll(ctx context.Context, instanceID, to, question string, options []string, multiSelect bool) (SentMessage, error)

	// SendContact sends one or more contact vCards.
	SendContact(ctx context.Context, instanceID, to string, contacts []ContactCard) (SentMessage, error)

	// --- Contacts ---

	// IsOnWhatsApp checks whether the given phone numbers have WhatsApp accounts.
	IsOnWhatsApp(ctx context.Context, instanceID string, phones []string) ([]ContactCheck, error)

	// GetContactInfo returns profile information for a JID.
	GetContactInfo(ctx context.Context, instanceID, jid string) (ContactInfo, error)

	// GetProfilePicture returns the profile picture URL for a JID (contact or group).
	GetProfilePicture(ctx context.Context, instanceID, jid string) (string, error)

	// --- Groups ---

	// CreateGroup creates a new WhatsApp group.
	CreateGroup(ctx context.Context, instanceID string, req CreateGroupRequest) (GroupInfo, error)

	// GetGroupInfo returns metadata for a group JID.
	GetGroupInfo(ctx context.Context, instanceID, jid string) (GroupInfo, error)

	// UpdateGroupParticipants adds, removes, promotes or demotes participants.
	// action must be one of: "add" | "remove" | "promote" | "demote"
	UpdateGroupParticipants(ctx context.Context, instanceID, groupJID string, participants []string, action string) error

	// LeaveGroup leaves a group the instance is a member of.
	LeaveGroup(ctx context.Context, instanceID, groupJID string) error

	// GetJoinedGroups returns all groups the instance belongs to.
	GetJoinedGroups(ctx context.Context, instanceID string) ([]GroupInfo, error)

	// --- Settings ---

	// ApplySettings pushes runtime behavioral settings to an instance.
	// Safe to call on disconnected instances; settings are applied on next connect.
	ApplySettings(ctx context.Context, instanceID string, settings InstanceSettings) error

	// SetPresence sends a typing/recording/available/unavailable presence to a chat.
	// presenceType: "typing" | "recording" | "paused" | "available" | "unavailable"
	SetPresence(ctx context.Context, instanceID, to, presenceType string) error

	// UpdateProfile updates the instance's own display name and/or profile photo.
	UpdateProfile(ctx context.Context, instanceID, name string, photoData []byte) error

	// --- Events ---

	// Subscribe registers an event handler and returns its handler ID.
	// Handlers are called for events from ALL instances managed by this engine.
	// Filter by event.InstanceID inside the handler.
	Subscribe(handler EventHandler) uint32

	// Unsubscribe removes a previously registered handler.
	Unsubscribe(handlerID uint32)
}
