// Package webhook delivers engine events to per-instance webhook URLs configured in settings.
package webhook

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/rs/zerolog"

	"velix/internal/engine"
	"velix/internal/logger"
)

// InstanceSettingsReader provides per-instance webhook settings.
type InstanceSettingsReader interface {
	GetInstanceWebhookSettings(ctx context.Context, instanceID string) (url string, events []string, err error)
}

// Service subscribes to engine events and delivers them to each instance's configured webhook URL.
type Service struct {
	eng        engine.Engine
	instReader InstanceSettingsReader
	httpClient *http.Client
	log        zerolog.Logger
}

// NewService creates a new webhook service and subscribes to engine events.
func NewService(eng engine.Engine, instReader InstanceSettingsReader) *Service {
	s := &Service{
		eng:        eng,
		instReader: instReader,
		httpClient: &http.Client{Timeout: 10 * time.Second},
		log:        logger.New("webhook-service"),
	}
	eng.Subscribe(s.handleEngineEvent)
	return s
}

// handleEngineEvent converts an engine event to a JSON payload and delivers it to the
// instance's configured webhook URL (Settings → webhook_url / webhook_events).
func (s *Service) handleEngineEvent(evt engine.Event) {
	payload := map[string]any{
		"event":       string(evt.Type),
		"instance_id": evt.InstanceID,
		"timestamp":   evt.Timestamp,
		"data":        evt.Payload,
	}
	s.deliver(context.Background(), evt.InstanceID, string(evt.Type), payload)
}

// deliver sends the payload to the instance's webhook URL.
// Delivery is best-effort — errors are logged as warnings, no retry.
func (s *Service) deliver(ctx context.Context, instanceID, eventType string, payload map[string]any) {
	if s.instReader == nil {
		return
	}
	url, events, err := s.instReader.GetInstanceWebhookSettings(ctx, instanceID)
	if err != nil || url == "" {
		return
	}
	// If the instance has an event whitelist, check membership.
	if len(events) > 0 {
		allowed := false
		for _, e := range events {
			if e == eventType {
				allowed = true
				break
			}
		}
		if !allowed {
			return
		}
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		s.log.Warn().Err(err).Str("instance", instanceID).Msg("Invalid webhook URL")
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Event-Type", eventType)
	req.Header.Set("X-Instance-ID", instanceID)

	resp, err := s.httpClient.Do(req)
	if err != nil {
		s.log.Warn().Err(err).Str("instance", instanceID).Str("url", url).Msg("Webhook delivery failed")
		return
	}
	resp.Body.Close()
}
