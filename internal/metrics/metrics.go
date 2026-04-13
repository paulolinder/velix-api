// Package metrics provides lightweight Prometheus-compatible metrics for Velix API.
// It emits text in the Prometheus exposition format without external dependencies.
package metrics

import (
	"fmt"
	"net/http"
	"runtime"
	"sync/atomic"
	"time"
)

// M is the global metrics registry. Increment counters anywhere in the codebase
// by calling metrics.M.MessagesSent.Add(1), etc.
var M = &Registry{started: time.Now()}

// Registry holds all application-level counters and gauges.
type Registry struct {
	// Counters (monotonically increasing)
	MessagesSent      atomic.Int64 // outbound messages dispatched to WhatsApp
	MessagesScheduled atomic.Int64 // messages saved as scheduled (not yet sent)
	MessagesInbound   atomic.Int64 // inbound messages received from WhatsApp
	WebhookDeliveries atomic.Int64 // webhook POST attempts that returned 2xx
	WebhookFailures   atomic.Int64 // webhook POST attempts that failed
	HTTPRequests      atomic.Int64 // total HTTP requests handled

	// Gauges (can go up and down)
	InstancesConnected atomic.Int64 // WhatsApp instances currently connected
	WSClients          atomic.Int64 // active WebSocket connections

	started time.Time
}

// Started returns the time the process started (used to compute uptime).
func (r *Registry) Started() time.Time { return r.started }

// Handler returns an http.HandlerFunc that writes Prometheus text exposition.
func (r *Registry) Handler() http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		uptime := time.Since(r.started).Seconds()

		lines := []string{
			"# HELP velix_uptime_seconds Seconds since the process started.",
			"# TYPE velix_uptime_seconds gauge",
			fmt.Sprintf("velix_uptime_seconds %.2f", uptime),

			"# HELP velix_messages_sent_total Outbound messages sent to WhatsApp.",
			"# TYPE velix_messages_sent_total counter",
			fmt.Sprintf("velix_messages_sent_total %d", r.MessagesSent.Load()),

			"# HELP velix_messages_scheduled_total Messages saved as scheduled.",
			"# TYPE velix_messages_scheduled_total counter",
			fmt.Sprintf("velix_messages_scheduled_total %d", r.MessagesScheduled.Load()),

			"# HELP velix_messages_inbound_total Inbound messages received from WhatsApp.",
			"# TYPE velix_messages_inbound_total counter",
			fmt.Sprintf("velix_messages_inbound_total %d", r.MessagesInbound.Load()),

			"# HELP velix_webhook_deliveries_total Successful webhook deliveries (2xx).",
			"# TYPE velix_webhook_deliveries_total counter",
			fmt.Sprintf("velix_webhook_deliveries_total %d", r.WebhookDeliveries.Load()),

			"# HELP velix_webhook_failures_total Failed webhook delivery attempts.",
			"# TYPE velix_webhook_failures_total counter",
			fmt.Sprintf("velix_webhook_failures_total %d", r.WebhookFailures.Load()),

			"# HELP velix_http_requests_total Total HTTP requests processed.",
			"# TYPE velix_http_requests_total counter",
			fmt.Sprintf("velix_http_requests_total %d", r.HTTPRequests.Load()),

			"# HELP velix_instances_connected Current number of connected WhatsApp instances.",
			"# TYPE velix_instances_connected gauge",
			fmt.Sprintf("velix_instances_connected %d", r.InstancesConnected.Load()),

			"# HELP velix_websocket_clients Current number of active WebSocket connections.",
			"# TYPE velix_websocket_clients gauge",
			fmt.Sprintf("velix_websocket_clients %d", r.WSClients.Load()),

			"# HELP velix_goroutines Current number of goroutines.",
			"# TYPE velix_goroutines gauge",
			fmt.Sprintf("velix_goroutines %d", runtime.NumGoroutine()),
		}

		for _, line := range lines {
			fmt.Fprintln(w, line)
		}
	}
}
