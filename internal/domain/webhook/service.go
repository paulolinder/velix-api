// Package webhook delivers engine events to per-instance webhook URLs configured in settings.
package webhook

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/rs/zerolog"

	"velix/internal/engine"
	"velix/internal/logger"
	"velix/internal/metrics"
	"velix/internal/urlutil"
)

// InstanceSettingsReader provides per-instance webhook settings.
type InstanceSettingsReader interface {
	GetInstanceWebhookSettings(ctx context.Context, instanceID string) (url, secret string, events []string, err error)
}

// deliveryJob holds a webhook delivery request.
type deliveryJob struct {
	instanceID string
	eventType  string
	payload    map[string]any
}

// Service subscribes to engine events and delivers them to each instance's configured webhook URL.
// Deliveries are processed by a fixed worker pool to avoid goroutine leak under load.
type Service struct {
	eng        engine.Engine
	instReader InstanceSettingsReader
	httpClient *http.Client
	log        zerolog.Logger
	jobs       chan deliveryJob
}

// NewService creates a new webhook service and subscribes to engine events.
func NewService(eng engine.Engine, instReader InstanceSettingsReader) *Service {
	s := &Service{
		eng:        eng,
		instReader: instReader,
		// Never follow redirects: a webhook endpoint that returns 3xx is
		// treated as delivered (status < 500). More importantly, following
		// redirects bypasses the SSRF blocklist in ValidateWebhookURL — an
		// attacker registers http://evil.com/ which 301-redirects to
		// http://169.254.169.254/ and the default client would follow it.
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		log:        logger.New("webhook-service"),
		jobs:       make(chan deliveryJob, 1000),
	}
	eng.Subscribe(s.handleEngineEvent)
	// Start 10 delivery workers.
	for i := 0; i < 10; i++ {
		go s.worker()
	}
	return s
}

func (s *Service) worker() {
	for job := range s.jobs {
		s.deliverSafe(job)
	}
}

func (s *Service) deliverSafe(job deliveryJob) {
	defer func() {
		if r := recover(); r != nil {
			s.log.Error().Interface("panic", r).Str("instance", job.instanceID).Msg("panic in webhook worker — recovered")
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	s.deliver(ctx, job.instanceID, job.eventType, job.payload)
}

// handleEngineEvent converts an engine event to a JSON payload and enqueues delivery.
// Never blocks — if the job channel is full, the event is dropped with a warning.
func (s *Service) handleEngineEvent(evt engine.Event) {
	payload := map[string]any{
		"event":       string(evt.Type),
		"instance_id": evt.InstanceID,
		"timestamp":   evt.Timestamp,
		"data":        evt.Payload,
	}
	select {
	case s.jobs <- deliveryJob{instanceID: evt.InstanceID, eventType: string(evt.Type), payload: payload}:
	default:
		s.log.Warn().Str("instance", evt.InstanceID).Str("event", string(evt.Type)).Msg("Webhook job queue full — event dropped")
	}
}

// deliver sends the payload to the instance's webhook URL with retry on failure.
func (s *Service) deliver(ctx context.Context, instanceID, eventType string, payload map[string]any) {
	if s.instReader == nil {
		return
	}
	url, secret, events, err := s.instReader.GetInstanceWebhookSettings(ctx, instanceID)
	if err != nil || url == "" {
		return
	}
	// Block delivery to private/internal addresses (SSRF prevention).
	if err := urlutil.ValidateWebhookURL(url); err != nil {
		s.log.Warn().Err(err).Str("instance", instanceID).Str("url", url).Msg("Webhook delivery blocked: URL points to a private address")
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
	// Sign the payload with HMAC-SHA256 when a secret is configured.
	// Receiver must verify: HMAC-SHA256(secret, body) == header value (after stripping "sha256=").
	if secret != "" {
		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write(body)
		sig := "sha256=" + hex.EncodeToString(mac.Sum(nil))
		req.Header.Set("X-Webhook-Signature", sig)
		req.Header.Set("X-Hub-Signature-256", sig) // GitHub-compatible alias
	}

	// Retry up to 3 times with exponential backoff.
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			backoff := time.Duration(attempt*attempt) * time.Second // 1s, 4s
			time.Sleep(backoff)
			// Rebuild request (body was already consumed).
			req, _ = http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-Event-Type", eventType)
			req.Header.Set("X-Instance-ID", instanceID)
			if secret != "" {
				mac := hmac.New(sha256.New, []byte(secret))
				mac.Write(body)
				sig := "sha256=" + hex.EncodeToString(mac.Sum(nil))
				req.Header.Set("X-Webhook-Signature", sig)
				req.Header.Set("X-Hub-Signature-256", sig)
			}
		}

		resp, err := s.httpClient.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		resp.Body.Close()

		if resp.StatusCode < 500 {
			// 2xx/3xx/4xx — delivered (4xx is the receiver's problem, don't retry).
			metrics.M.WebhookDeliveries.Add(1)
			return
		}
		lastErr = fmt.Errorf("status %d", resp.StatusCode)
	}

	metrics.M.WebhookFailures.Add(1)
	s.log.Warn().Err(lastErr).Str("instance", instanceID).Str("url", url).Int("attempts", 3).Msg("Webhook delivery failed after retries")
}
