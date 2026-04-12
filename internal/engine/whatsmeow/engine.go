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
	// Entries are removed when the echo arrives or after a 2-minute TTL.
	apiSentIDs sync.Map // key: messageID (string) → struct{}
}

// markAPISent registers a message ID as API-originated.
// The entry expires automatically after 2 minutes if the echo never arrives.
func (e *Engine) markAPISent(id string) {
	e.apiSentIDs.Store(id, struct{}{})
	go func() {
		t := time.NewTimer(2 * time.Minute)
		defer t.Stop()
		select {
		case <-t.C:
			e.apiSentIDs.Delete(id)
		case <-e.ctx.Done():
		}
	}()
}

// isAPISent returns true and removes the entry if the ID was API-sent.
func (e *Engine) isAPISent(id string) bool {
	_, ok := e.apiSentIDs.LoadAndDelete(id)
	return ok
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
		if mi.client.IsConnected() {
			mi.client.Disconnect()
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
func (e *Engine) dispatch(evt engine.Event) {
	e.subMu.RLock()
	list := make([]engine.EventHandler, 0, len(e.handlers))
	for _, h := range e.handlers {
		list = append(list, h)
	}
	e.subMu.RUnlock()

	for _, h := range list {
		h(evt)
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
