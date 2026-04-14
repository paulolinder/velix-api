// Package instance contains HTTP handlers for the /v1/instances resource.
package instance

import (
	"time"

	"velix/internal/domain/instance"
)

// CreateRequest is the body for POST /v1/instances.
type CreateRequest struct {
	Name     string `json:"name"`
	ProxyURL string `json:"proxy_url,omitempty"`
}

// ConnectRequest is the body for POST /v1/instances/{id}/connect.
// It's empty for now but kept as a struct for future options (e.g. phone number hint).
type ConnectRequest struct{}

// PairCodeRequest is the body for POST /v1/instances/{id}/pair-code.
type PairCodeRequest struct {
	Phone string `json:"phone"` // E.164 without +, e.g. "5511999990001"
}

// Response is the JSON representation of an instance.
type Response struct {
	ID              string     `json:"id"`
	WorkspaceID     string     `json:"workspace_id"`
	Name            string     `json:"name"`
	PhoneNumber     string     `json:"phone_number,omitempty"`
	Status          string     `json:"status"`
	JID             string     `json:"jid,omitempty"`
	Platform        string     `json:"platform,omitempty"`
	BusinessName    string     `json:"business_name,omitempty"`
	LastConnectedAt *time.Time `json:"last_connected_at,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

// CreateResponse is returned when a new instance is created.
// It includes the raw API key which is shown once and never returned again.
type CreateResponse struct {
	Instance *Response `json:"instance"`
	APIKey   string    `json:"api_key"` // shown once — save it
}

// PairCodeResponse is returned by POST /v1/instances/{id}/pair-code.
type PairCodeResponse struct {
	Code string `json:"code"`
}

// StatusResponse is returned by GET /v1/instances/{id}/status.
type StatusResponse struct {
	InstanceID string     `json:"instance_id"`
	Status     string     `json:"status"`
	LastSeen   *time.Time `json:"last_seen,omitempty"`
	Phone      string     `json:"phone,omitempty"`
	Platform   string     `json:"platform,omitempty"`
}

// SettingsResponse is the JSON shape for instance settings.
type SettingsResponse struct {
	RejectCall         bool     `json:"reject_call"`
	ReadMessages       bool     `json:"read_messages"`
	AlwaysOnline       bool     `json:"always_online"`
	IgnoreGroups       bool     `json:"ignore_groups"`
	SyncFullHistory    bool     `json:"sync_full_history"`
	WebhookURL         string   `json:"webhook_url"`
	WebhookSecret      string   `json:"webhook_secret,omitempty"` // HMAC secret (shown when set, never auto-generated)
	WebhookEvents      []string `json:"webhook_events"`
	HumanPauseDuration int      `json:"human_pause_duration"`
	WebhookBase64      bool     `json:"webhook_base64"`
	// Chatwoot
	ChatwootEnabled     bool   `json:"chatwoot_enabled"`
	ChatwootURL         string `json:"chatwoot_url"`
	ChatwootToken       string `json:"chatwoot_token"`
	ChatwootAccountID   int64  `json:"chatwoot_account_id"`
	ChatwootInboxID     int64  `json:"chatwoot_inbox_id"`
	ChatwootSignMsgs    bool   `json:"chatwoot_sign_msgs"`
	ChatwootReopenConv  bool   `json:"chatwoot_reopen_conv"`
	ChatwootConvPending bool   `json:"chatwoot_conv_pending"`
}

// UpdateSettingsRequest is the body for PATCH .../settings.
// Pointer fields allow distinguishing "not sent" from "set to false/empty".
type UpdateSettingsRequest struct {
	RejectCall         *bool     `json:"reject_call,omitempty"`
	ReadMessages       *bool     `json:"read_messages,omitempty"`
	AlwaysOnline       *bool     `json:"always_online,omitempty"`
	IgnoreGroups       *bool     `json:"ignore_groups,omitempty"`
	SyncFullHistory    *bool     `json:"sync_full_history,omitempty"`
	WebhookURL         *string   `json:"webhook_url,omitempty"`
	WebhookSecret      *string   `json:"webhook_secret,omitempty"`
	WebhookEvents      *[]string `json:"webhook_events,omitempty"`
	HumanPauseDuration *int      `json:"human_pause_duration,omitempty"`
	WebhookBase64      *bool    `json:"webhook_base64,omitempty"`
	// Chatwoot
	ChatwootEnabled     *bool   `json:"chatwoot_enabled,omitempty"`
	ChatwootURL         *string `json:"chatwoot_url,omitempty"`
	ChatwootToken       *string `json:"chatwoot_token,omitempty"`
	ChatwootAccountID   *int64  `json:"chatwoot_account_id,omitempty"`
	ChatwootInboxID     *int64  `json:"chatwoot_inbox_id,omitempty"`
	ChatwootSignMsgs    *bool   `json:"chatwoot_sign_msgs,omitempty"`
	ChatwootReopenConv  *bool   `json:"chatwoot_reopen_conv,omitempty"`
	ChatwootConvPending *bool   `json:"chatwoot_conv_pending,omitempty"`
}

func settingsFromDomain(s *instance.Settings) *SettingsResponse {
	events := s.WebhookEvents
	if events == nil {
		events = []string{}
	}
	return &SettingsResponse{
		RejectCall:         s.RejectCall,
		ReadMessages:       s.ReadMessages,
		AlwaysOnline:       s.AlwaysOnline,
		IgnoreGroups:       s.IgnoreGroups,
		SyncFullHistory:    s.SyncFullHistory,
		WebhookURL:         s.WebhookURL,
		WebhookSecret:      s.WebhookSecret,
		WebhookEvents:      events,
		HumanPauseDuration: s.HumanPauseDuration,
		WebhookBase64:      s.WebhookBase64,
		ChatwootEnabled:    s.ChatwootEnabled,
		ChatwootURL:        s.ChatwootURL,
		ChatwootToken:      s.ChatwootToken,
		ChatwootAccountID:  s.ChatwootAccountID,
		ChatwootInboxID:    s.ChatwootInboxID,
		ChatwootSignMsgs:    s.ChatwootSignMsgs,
		ChatwootReopenConv:  s.ChatwootReopenConv,
		ChatwootConvPending: s.ChatwootConvPending,
	}
}

// fromDomain converts a domain instance to its HTTP response shape.
func fromDomain(inst *instance.Instance) *Response {
	return &Response{
		ID:              inst.ID,
		WorkspaceID:     inst.WorkspaceID,
		Name:            inst.Name,
		PhoneNumber:     inst.PhoneNumber,
		Status:          string(inst.Status),
		JID:             inst.JID,
		Platform:        inst.Platform,
		BusinessName:    inst.BusinessName,
		LastConnectedAt: inst.LastConnectedAt,
		CreatedAt:       inst.CreatedAt,
		UpdatedAt:       inst.UpdatedAt,
	}
}
