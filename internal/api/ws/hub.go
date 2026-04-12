// Package ws implements a WebSocket hub for real-time engine event streaming.
package ws

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/rs/zerolog"

	"velix/internal/engine"
	"velix/internal/logger"
	"velix/internal/metrics"
)

// client represents a single connected WebSocket consumer.
type client struct {
	workspaceID string
	send        chan []byte
}

// Hub maintains all active WebSocket connections and routes engine events to them.
type Hub struct {
	mu      sync.RWMutex
	clients map[*client]struct{}
	instSvc InstanceOwnerChecker
	log     zerolog.Logger
}

// InstanceOwnerChecker can verify that a given instance belongs to a workspace.
type InstanceOwnerChecker interface {
	InstanceBelongsToWorkspace(ctx context.Context, workspaceID, instanceID string) bool
}

// NewHub creates a Hub and wires it into the engine event stream.
func NewHub(eng engine.Engine, instSvc InstanceOwnerChecker) *Hub {
	h := &Hub{
		clients: make(map[*client]struct{}),
		instSvc: instSvc,
		log:     logger.New("ws-hub"),
	}
	eng.Subscribe(h.handleEngineEvent)
	return h
}

// register adds a new client to the hub.
func (h *Hub) register(c *client) {
	h.mu.Lock()
	h.clients[c] = struct{}{}
	h.mu.Unlock()
	metrics.M.WSClients.Add(1)
}

// unregister removes a client from the hub and closes its send channel.
func (h *Hub) unregister(c *client) {
	h.mu.Lock()
	if _, ok := h.clients[c]; ok {
		delete(h.clients, c)
		close(c.send)
		metrics.M.WSClients.Add(-1)
	}
	h.mu.Unlock()
}

// handleEngineEvent is the engine subscriber callback.
// It looks up which workspace owns the instance and fans out to matching clients.
func (h *Hub) handleEngineEvent(evt engine.Event) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	payload, err := json.Marshal(map[string]any{
		"event":       string(evt.Type),
		"instance_id": evt.InstanceID,
		"timestamp":   time.Now().UTC(),
		"data":        evt.Payload,
	})
	if err != nil {
		h.log.Error().Err(err).Msg("ws hub: failed to marshal event")
		return
	}

	h.mu.RLock()
	defer h.mu.RUnlock()

	for c := range h.clients {
		if !h.instSvc.InstanceBelongsToWorkspace(ctx, c.workspaceID, evt.InstanceID) {
			continue
		}
		select {
		case c.send <- payload:
		default:
			// Slow consumer — drop message rather than block.
			h.log.Warn().Str("workspace_id", c.workspaceID).Msg("ws hub: dropping event for slow consumer")
		}
	}
}
