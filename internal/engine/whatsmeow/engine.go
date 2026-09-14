// Package waengine implements the engine.Engine interface using the WhatsMeow library.
//
// This is the ONLY package allowed to import go.mau.fi/whatsmeow directly.
// All other application code must use engine.Engine interface.
package waengine

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	_ "github.com/glebarez/go-sqlite" // registers pure-Go SQLite as "sqlite" driver

	"velix/internal/config"
	"velix/internal/engine"
	"velix/internal/logger"

	"github.com/rs/zerolog"
)

// Engine implements engine.Engine using WhatsMeow as the WhatsApp protocol backend.
type Engine struct {
	cfg config.EngineConfig
	log zerolog.Logger
	ctx context.Context

	mu      sync.RWMutex
	clients map[string]*managedInstance

	subMu     sync.RWMutex
	handlers  map[uint32]engine.EventHandler
	nextSubID atomic.Uint32

	// apiSentIDs tracks message IDs sent via the API so that their WhatsApp
	// echo can be tagged Source="api" instead of Source="manual".
	// Value is the time.Time at which the entry was created; a single
	// background goroutine (started in Start) evicts entries older than 2
	// minutes so we never spawn one goroutine per message.
	apiSentIDs sync.Map // key: messageID (string) → time.Time
}

// markAPISent registers a message ID as API-originated.
func (e *Engine) markAPISent(id string) {
	e.apiSentIDs.Store(id, time.Now())
}

// isAPISent returns true and removes the entry if the ID was API-sent.
func (e *Engine) isAPISent(id string) bool {
	_, ok := e.apiSentIDs.LoadAndDelete(id)
	return ok
}

// startAPISentCleaner runs a single goroutine that evicts stale apiSentIDs
// entries every minute. This replaces the previous pattern of spawning one
// goroutine per markAPISent call, which could accumulate thousands of timers
// on busy multi-instance deployments.
func (e *Engine) startAPISentCleaner(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				cutoff := time.Now().Add(-2 * time.Minute)
				e.apiSentIDs.Range(func(k, v any) bool {
					if t, ok := v.(time.Time); ok && t.Before(cutoff) {
						e.apiSentIDs.Delete(k)
					}
					return true
				})
			}
		}
	}()
}

// New creates a new WhatsMeow engine. Call Start(ctx) before any other method.
func New(cfg config.EngineConfig) *Engine {
	return &Engine{
		cfg:      cfg,
		log:      logger.New("wa-engine"),
		clients:  make(map[string]*managedInstance),
		handlers: make(map[uint32]engine.EventHandler),
	}
}

// Start implements engine.Engine. Validates store path and logs startup.
func (e *Engine) Start(ctx context.Context) error {
	e.ctx = ctx
	e.startAPISentCleaner(ctx)
	e.log.Info().
		Str("store_path", e.cfg.StorePath).
		Int("max_instances", e.cfg.MaxInstances).
		Msg("WhatsApp engine started")
	return nil
}

// Stop implements engine.Engine. Disconnects all active instances gracefully.
func (e *Engine) Stop(_ context.Context) error {
	e.log.Info().Msg("WhatsApp engine stopping")

	e.mu.Lock()
	defer e.mu.Unlock()

	for id, mi := range e.clients {
		if mi.getClient().IsConnected() {
			mi.getClient().Disconnect()
			e.log.Debug().Str("instance", id).Msg("Instance disconnected")
		}
	}
	return nil
}

// Subscribe registers an event handler for all engine events and returns its ID.
func (e *Engine) Subscribe(h engine.EventHandler) uint32 {
	id := e.nextSubID.Add(1)
	e.subMu.Lock()
	e.handlers[id] = h
	e.subMu.Unlock()
	return id
}

// Unsubscribe removes a previously registered handler.
func (e *Engine) Unsubscribe(id uint32) {
	e.subMu.Lock()
	delete(e.handlers, id)
	e.subMu.Unlock()
}

// dispatch fans out an event to all registered subscribers.
// Each handler runs in its own goroutine so the whatsmeow event loop is never
// blocked by slow subscribers (DB writes, HTTP webhook delivery, WS fanout, etc.).
// A defer/recover in each goroutine ensures a panicking subscriber cannot take
// the entire process down.
func (e *Engine) dispatch(evt engine.Event) {
	e.subMu.RLock()
	list := make([]engine.EventHandler, 0, len(e.handlers))
	for _, h := range e.handlers {
		list = append(list, h)
	}
	e.subMu.RUnlock()

	for _, h := range list {
		go func(fn engine.EventHandler) {
			defer func() {
				if r := recover(); r != nil {
					e.log.Error().
						Interface("panic", r).
						Str("event", string(evt.Type)).
						Msg("panic in event subscriber — recovered")
				}
			}()
			fn(evt)
		}(h)
	}
}

// getInstance safely retrieves a managed instance or returns an error.
func (e *Engine) getInstance(id string) (*managedInstance, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	mi, ok := e.clients[id]
	if !ok {
		return nil, fmt.Errorf("instance %q not found in engine", id)
	}
	return mi, nil
}

// Compile-time check that Engine satisfies the interface.
var _ engine.Engine = (*Engine)(nil)
