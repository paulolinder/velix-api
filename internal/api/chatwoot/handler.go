// Package chatwoot provides the HTTP endpoint that receives Chatwoot webhook events.
package chatwoot

import (
	"context"
	"encoding/json"
	"net/http"

	"velix/internal/domain/chatwoot"
)

// webhookService is the subset of chatwoot.Service used by the handler.
type webhookService interface {
	HandleWebhook(ctx context.Context, p *chatwoot.ChatwootWebhookPayload) error
}

// Handler handles POST /v1/chatwoot/webhook.
type Handler struct {
	svc webhookService
}

// NewHandler creates a new Chatwoot webhook handler.
func NewHandler(svc webhookService) *Handler {
	return &Handler{svc: svc}
}

// Webhook receives events from Chatwoot and forwards agent messages to WhatsApp.
// This endpoint is intentionally unauthenticated — Chatwoot sends plain HTTP POST.
func (h *Handler) Webhook(w http.ResponseWriter, r *http.Request) {
	var payload chatwoot.ChatwootWebhookPayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	// Process asynchronously so Chatwoot doesn't wait for WA delivery.
	go func() {
		_ = h.svc.HandleWebhook(context.Background(), &payload)
	}()

	w.WriteHeader(http.StatusOK)
}
