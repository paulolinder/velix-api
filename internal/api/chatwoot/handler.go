// Package chatwoot provides the HTTP endpoint that receives Chatwoot webhook events.
package chatwoot

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	apipkg "velix/internal/api"
	"velix/internal/domain/chatwoot"
)

// webhookService is the subset of chatwoot.Service used by the handler.
type webhookService interface {
	HandleWebhook(ctx context.Context, p *chatwoot.ChatwootWebhookPayload) error
}

// syncService is the subset of chatwoot.Service used by the sync handler.
type syncService interface {
	SyncHistory(ctx context.Context, instanceID string) (*chatwoot.SyncResult, error)
}

// Handler handles POST /v1/chatwoot/webhook.
type Handler struct {
	svc    webhookService
	secret string // optional shared secret (CHATWOOT_WEBHOOK_SECRET)
}

// SyncHandler handles POST /v1/instances/{instanceID}/chatwoot/sync.
type SyncHandler struct {
	svc syncService
}

// NewSyncHandler creates a new Chatwoot sync handler.
func NewSyncHandler(svc syncService) *SyncHandler {
	return &SyncHandler{svc: svc}
}

// Sync triggers a Chatwoot history sync for the given instance.
func (h *SyncHandler) Sync(w http.ResponseWriter, r *http.Request) {
	instanceID := chi.URLParam(r, "instanceID")
	if instanceID == "" {
		apipkg.WriteError(w, r, apipkg.NewError(apipkg.ErrCodeValidation, "instance ID is required"))
		return
	}

	result, err := h.svc.SyncHistory(r.Context(), instanceID)
	if err != nil {
		apipkg.WriteError(w, r, apipkg.NewError(apipkg.ErrCodeInternal, err.Error()))
		return
	}

	apipkg.WriteJSON(w, r, http.StatusOK, result)
}

// NewHandler creates a new Chatwoot webhook handler.
// secret is optional — if non-empty, every incoming request must supply it
// either as ?secret=<value> in the URL or as "Authorization: Bearer <value>".
func NewHandler(svc webhookService, secret ...string) *Handler {
	h := &Handler{svc: svc}
	if len(secret) > 0 {
		h.secret = secret[0]
	}
	return h
}

// Webhook receives events from Chatwoot and forwards agent messages to WhatsApp.
// When CHATWOOT_WEBHOOK_SECRET is configured the endpoint validates the secret
// before processing, preventing webhook forgery attacks.
func (h *Handler) Webhook(w http.ResponseWriter, r *http.Request) {
	// Require shared secret — rejects all requests if CHATWOOT_WEBHOOK_SECRET is not set.
	if h.secret == "" {
		http.Error(w, `{"error":"webhook secret not configured"}`, http.StatusServiceUnavailable)
		return
	}
	// Supports two delivery patterns:
	//   1. Query parameter: POST /v1/chatwoot/webhook?secret=<value>
	//   2. Header: Authorization: Bearer <value>
	supplied := r.URL.Query().Get("secret")
	if supplied == "" {
		if auth := r.Header.Get("Authorization"); len(auth) > 7 {
			supplied = auth[7:] // strip "Bearer "
		}
	}
	// Use constant-time comparison to prevent timing attacks.
	if subtle.ConstantTimeCompare([]byte(supplied), []byte(h.secret)) != 1 {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return
	}

	var payload chatwoot.ChatwootWebhookPayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	// Process asynchronously so Chatwoot doesn't wait for WA delivery.
	// Timeout prevents goroutine leak if downstream is unresponsive.
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = h.svc.HandleWebhook(ctx, &payload)
	}()

	w.WriteHeader(http.StatusOK)
}
