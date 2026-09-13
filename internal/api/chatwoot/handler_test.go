package chatwoot

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"velix/internal/domain/chatwoot"
)

// stubWebhookService satisfies webhookService without doing anything real.
type stubWebhookService struct {
	called bool
}

func (s *stubWebhookService) HandleWebhook(_ context.Context, _ *chatwoot.ChatwootWebhookPayload) error {
	s.called = true
	return nil
}

var validPayload = []byte(`{"event":"message_created","message_type":"outgoing"}`)

func TestWebhook_NoSecret_Returns503(t *testing.T) {
	h := NewHandler(&stubWebhookService{})
	req := httptest.NewRequest(http.MethodPost, "/v1/chatwoot/webhook", bytes.NewReader(validPayload))
	w := httptest.NewRecorder()
	h.Webhook(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("got %d, want 503", w.Code)
	}
}

func TestWebhook_WrongSecret_Returns401(t *testing.T) {
	h := NewHandler(&stubWebhookService{}, "correct-secret")
	req := httptest.NewRequest(http.MethodPost, "/v1/chatwoot/webhook?secret=wrong-secret", bytes.NewReader(validPayload))
	w := httptest.NewRecorder()
	h.Webhook(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("got %d, want 401", w.Code)
	}
}

func TestWebhook_CorrectSecret_QueryParam_Returns200(t *testing.T) {
	svc := &stubWebhookService{}
	h := NewHandler(svc, "my-secret")
	req := httptest.NewRequest(http.MethodPost, "/v1/chatwoot/webhook?secret=my-secret", bytes.NewReader(validPayload))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.Webhook(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("got %d, want 200", w.Code)
	}
}

func TestWebhook_CorrectSecret_AuthHeader_Returns200(t *testing.T) {
	svc := &stubWebhookService{}
	h := NewHandler(svc, "my-secret")
	req := httptest.NewRequest(http.MethodPost, "/v1/chatwoot/webhook", bytes.NewReader(validPayload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer my-secret")
	w := httptest.NewRecorder()
	h.Webhook(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("got %d, want 200", w.Code)
	}
}

func TestWebhook_BadJSON_Returns400(t *testing.T) {
	h := NewHandler(&stubWebhookService{}, "s")
	req := httptest.NewRequest(http.MethodPost, "/v1/chatwoot/webhook?secret=s", bytes.NewReader([]byte("not json")))
	w := httptest.NewRecorder()
	h.Webhook(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("got %d, want 400", w.Code)
	}
}
