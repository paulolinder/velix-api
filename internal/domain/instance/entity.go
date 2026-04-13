// Package instance contains the domain model and business logic for WhatsApp instances.
package instance

import (
	"time"

	"velix/internal/engine"
)

// Status mirrors engine.InstanceStatus so the domain layer has no engine import at the type level.
type Status = engine.InstanceStatus

// Instance represents a managed WhatsApp connection belonging to a workspace.
type Instance struct {
	ID              string
	WorkspaceID     string
	Name            string
	PhoneNumber     string
	Status          Status
	JID             string // WhatsApp JID once paired
	Platform        string
	BusinessName    string
	ProxyURL        string
	Settings        Settings
	LastConnectedAt *time.Time
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// Settings holds per-instance behavioral configuration.
type Settings struct {
	RejectCall      bool     `json:"reject_call"`
	ReadMessages    bool     `json:"read_messages"`
	AlwaysOnline    bool     `json:"always_online"`
	IgnoreGroups    bool     `json:"ignore_groups"`
	SyncFullHistory bool     `json:"sync_full_history"`
	WebhookURL      string   `json:"webhook_url,omitempty"`
	WebhookSecret   string   `json:"webhook_secret,omitempty"`  // HMAC-SHA256 signing secret
	WebhookEvents   []string `json:"webhook_events,omitempty"`
	// HumanPauseDuration is the number of seconds automated replies should be
	// paused after a manual (or API) response is sent to a contact.
	// 0 = disabled.  The webhook payload includes a "Paused" flag so external
	// automation (e.g. n8n) can respect the pause window.
	HumanPauseDuration int `json:"human_pause_duration"`

	// Chatwoot integration — routes inbound WhatsApp messages to a Chatwoot inbox
	// and forwards agent replies back to WhatsApp.
	ChatwootEnabled    bool   `json:"chatwoot_enabled"`
	ChatwootURL        string `json:"chatwoot_url,omitempty"`
	ChatwootToken      string `json:"chatwoot_token,omitempty"`
	ChatwootAccountID  int64  `json:"chatwoot_account_id,omitempty"`
	ChatwootInboxID    int64  `json:"chatwoot_inbox_id,omitempty"`
	ChatwootSignMsgs   bool   `json:"chatwoot_sign_msgs"`    // prefix outgoing msgs with agent name
	ChatwootReopenConv bool   `json:"chatwoot_reopen_conv"`  // reopen resolved conversations on new message
	ChatwootConvPending bool  `json:"chatwoot_conv_pending"` // start new conversations as pending
}
