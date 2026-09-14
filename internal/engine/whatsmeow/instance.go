package waengine

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"
	waLog "go.mau.fi/whatsmeow/util/log"
	"golang.org/x/time/rate"

	"velix/internal/engine"
)

// humanPauseEntry holds the expiration time for a paused chat.
type humanPauseEntry struct {
	ExpiresAt time.Time
}

// managedInstance wraps a whatsmeow.Client with our status tracking.
type managedInstance struct {
	id        string
	clientRef atomic.Pointer[whatsmeow.Client] // accessed via getClient() / Store()
	container *sqlstore.Container              // kept so reinitClient can create a fresh device

	mu       sync.RWMutex
	status   engine.InstanceStatus
	settings engine.InstanceSettings

	// msgLimiter enforces per-instance message rate: max 15 msgs/min, burst 5.
	// This prevents WhatsApp anti-spam bans from rapid-fire sending.
	msgLimiter *rate.Limiter

	// humanPauses tracks which chats are under a human-pause window.
	// key: chat JID string → expiration time.
	humanPauses sync.Map
}

// getClient returns the current whatsmeow client. Always use this instead of
// accessing clientRef directly so the pointer swap in reinitClient is race-safe.
func (mi *managedInstance) getClient() *whatsmeow.Client {
	return mi.clientRef.Load()
}

func (mi *managedInstance) setStatus(s engine.InstanceStatus) {
	mi.mu.Lock()
	mi.status = s
	mi.mu.Unlock()
}

func (mi *managedInstance) getStatus() engine.InstanceStatus {
	mi.mu.RLock()
	defer mi.mu.RUnlock()
	return mi.status
}

func (mi *managedInstance) getSettings() engine.InstanceSettings {
	mi.mu.RLock()
	defer mi.mu.RUnlock()
	return mi.settings
}

// pauseChat marks a chat as paused for the configured HumanPauseDuration.
// Does nothing if HumanPauseDuration is 0.
func (mi *managedInstance) pauseChat(chatJID string) {
	s := mi.getSettings()
	if s.HumanPauseDuration <= 0 {
		return
	}
	mi.humanPauses.Store(chatJID, humanPauseEntry{
		ExpiresAt: time.Now().Add(time.Duration(s.HumanPauseDuration) * time.Second),
	})
}

// isChatPaused returns true if the chat is currently under a human-pause window.
// Expired entries are cleaned up on access.
func (mi *managedInstance) isChatPaused(chatJID string) bool {
	val, ok := mi.humanPauses.Load(chatJID)
	if !ok {
		return false
	}
	entry := val.(humanPauseEntry)
	if time.Now().After(entry.ExpiresAt) {
		mi.humanPauses.Delete(chatJID)
		return false
	}
	return true
}

// ---------------------------------------------------------------------------
// Instance lifecycle
// ---------------------------------------------------------------------------

// CreateInstance implements engine.Engine.
func (e *Engine) CreateInstance(ctx context.Context, instanceID string, opts engine.InstanceOptions) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	// Enforce hard cap before creating new instance.
	if e.cfg.MaxInstances > 0 && len(e.clients) >= e.cfg.MaxInstances {
		return fmt.Errorf("max instances limit (%d) reached", e.cfg.MaxInstances)
	}

	if _, exists := e.clients[instanceID]; exists {
		return fmt.Errorf("instance %q already exists in engine", instanceID)
	}

	// Create per-instance SQLite store directory.
	storeDir := filepath.Join(e.cfg.StorePath, instanceID)
	if err := os.MkdirAll(storeDir, 0o755); err != nil {
		return fmt.Errorf("create store dir: %w", err)
	}

	dbPath := filepath.Join(storeDir, "wa.db")

	// Use Noop logger for WA internals to avoid log spam; swap to waLog.Zerolog for debugging.
	waLogger := waLog.Noop

	// WAL mode: allows reads to proceed concurrently with writes (critical during
	// history sync which bulk-inserts rows while API reads hit contacts/store).
	// busy_timeout=5000: retry for up to 5 s instead of immediately returning
	// SQLITE_BUSY when another writer holds the lock.
	db, err := sql.Open("sqlite", fmt.Sprintf(
		"file:%s?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)",
		dbPath,
	))
	if err != nil {
		return fmt.Errorf("open sqlite for %s: %w", instanceID, err)
	}
	// WAL allows one writer + multiple readers; single writer is enough here.
	db.SetMaxOpenConns(1)

	container := sqlstore.NewWithDB(db, "sqlite3", waLogger)
	if err := container.Upgrade(ctx); err != nil {
		_ = db.Close()
		return fmt.Errorf("upgrade WA store for %s: %w", instanceID, err)
	}

	// GetFirstDevice creates a new device if none exists.
	device, err := container.GetFirstDevice(ctx)
	if err != nil {
		return fmt.Errorf("get WA device for %s: %w", instanceID, err)
	}

	client := whatsmeow.NewClient(device, waLogger)
	client.EnableAutoReconnect = e.cfg.AutoReconnect

	mi := &managedInstance{
		id:         instanceID,
		container:  container,
		status:     engine.StatusDisconnected,
		msgLimiter: rate.NewLimiter(rate.Every(4*time.Second), 5), // ~15 msgs/min, burst 5
	}
	mi.clientRef.Store(client)

	// Bridge every WA event to our dispatcher.
	client.AddEventHandler(func(evt any) {
		e.handleWAEvent(instanceID, mi, evt)
	})

	e.clients[instanceID] = mi
	e.log.Info().Str("instance", instanceID).Msg("Instance registered in engine")
	return nil
}

// DeleteInstance implements engine.Engine.
func (e *Engine) DeleteInstance(_ context.Context, instanceID string) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	mi, ok := e.clients[instanceID]
	if !ok {
		return nil // idempotent
	}

	if mi.getClient().IsConnected() {
		mi.getClient().Disconnect()
	}

	delete(e.clients, instanceID)

	// Remove SQLite data directory.
	storeDir := filepath.Join(e.cfg.StorePath, instanceID)
	if err := os.RemoveAll(storeDir); err != nil {
		e.log.Warn().Err(err).Str("instance", instanceID).Msg("Failed to remove store directory")
	}

	e.log.Info().Str("instance", instanceID).Msg("Instance removed from engine")
	return nil
}

// ApplySettings implements engine.Engine.
func (e *Engine) ApplySettings(_ context.Context, instanceID string, s engine.InstanceSettings) error {
	mi, err := e.getInstance(instanceID)
	if err != nil {
		return err
	}
	mi.mu.Lock()
	mi.settings = s
	mi.mu.Unlock()

	// If connected, apply presence immediately.
	if mi.getClient().IsConnected() {
		if s.AlwaysOnline {
			_ = mi.getClient().SendPresence(e.ctx, types.PresenceAvailable)
		}
	}
	return nil
}

// GetStatus implements engine.Engine.
func (e *Engine) GetStatus(instanceID string) (engine.InstanceStatus, error) {
	mi, err := e.getInstance(instanceID)
	if err != nil {
		return "", err
	}
	return mi.getStatus(), nil
}

// ---------------------------------------------------------------------------
// Connection management
// ---------------------------------------------------------------------------

// Connect implements engine.Engine.
// Uses the engine's long-lived context (not the caller's request context) so
// that whatsmeow's internal auto-reconnect goroutine stays alive after the HTTP
// request that triggered the connection is closed (e.g. QR SSE stream ends).
func (e *Engine) Connect(ctx context.Context, instanceID string) error {
	mi, err := e.getInstance(instanceID)
	if err != nil {
		return err
	}

	if mi.getClient().IsConnected() {
		return nil // already connected, nothing to do
	}

	mi.setStatus(engine.StatusConnecting)

	if err := mi.getClient().ConnectContext(e.ctx); err != nil {
		mi.setStatus(engine.StatusDisconnected)
		return fmt.Errorf("connect %s: %w", instanceID, err)
	}

	return nil
}

// Disconnect implements engine.Engine.
func (e *Engine) Disconnect(_ context.Context, instanceID string) error {
	mi, err := e.getInstance(instanceID)
	if err != nil {
		return err
	}
	mi.getClient().Disconnect()
	mi.setStatus(engine.StatusDisconnected)
	return nil
}

// Logout implements engine.Engine.
func (e *Engine) Logout(ctx context.Context, instanceID string) error {
	mi, err := e.getInstance(instanceID)
	if err != nil {
		return err
	}
	if err := mi.getClient().Logout(ctx); err != nil {
		return fmt.Errorf("logout %s: %w", instanceID, err)
	}
	mi.setStatus(engine.StatusLoggedOut)
	return nil
}

// ---------------------------------------------------------------------------
// Authentication
// ---------------------------------------------------------------------------

// GetQRChannel implements engine.Engine.
// Must be called BEFORE Connect for a device that has no saved session.
func (e *Engine) GetQRChannel(ctx context.Context, instanceID string) (<-chan engine.QREvent, error) {
	mi, err := e.getInstance(instanceID)
	if err != nil {
		return nil, err
	}

	waChan, err := mi.getClient().GetQRChannel(ctx)
	if err != nil {
		return nil, fmt.Errorf("get QR channel: %w", err)
	}

	mi.setStatus(engine.StatusQRPending)

	out := make(chan engine.QREvent, 5)
	go func() {
		defer close(out)
		for item := range waChan {
			switch item.Event {
			case "code":
				select {
				case out <- engine.QREvent{Code: item.Code, Timeout: item.Timeout}:
				case <-ctx.Done():
					return
				}
			case "success":
				// Pairing succeeded — PairSuccess event will follow via WA event handler.
				return
			default:
				// timeout or err-* events
				qrErr := item.Error
				if qrErr == nil {
					qrErr = fmt.Errorf("QR event: %s", item.Event)
				}
				select {
				case out <- engine.QREvent{Error: qrErr}:
				case <-ctx.Done():
				}
				return
			}
		}
	}()

	return out, nil
}

// RequestPairCode implements engine.Engine.
// Requires the instance to be in the connecting state (Connect called but no session).
func (e *Engine) RequestPairCode(ctx context.Context, instanceID, phone string) (string, error) {
	mi, err := e.getInstance(instanceID)
	if err != nil {
		return "", err
	}

	// PairPhone timeout defaults to 160 seconds in WhatsMeow.
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	code, err := mi.getClient().PairPhone(ctx, phone, true, whatsmeow.PairClientChrome, "Chrome (Linux)")
	if err != nil {
		return "", fmt.Errorf("pair phone: %w", err)
	}
	return code, nil
}

// reinitClient replaces the whatsmeow client with a fresh one after a logout.
// When WhatsApp fires LoggedOut it deletes the device row from SQLite, so
// GetFirstDevice creates a new blank device — giving us a clean slate for the
// next QR / pairing-code flow without a server restart.
// Must be called in its own goroutine, not from within an event handler.
func (e *Engine) reinitClient(instanceID string, mi *managedInstance) error {
	device, err := mi.container.GetFirstDevice(e.ctx)
	if err != nil {
		return fmt.Errorf("reinit client for %s: %w", instanceID, err)
	}

	client := whatsmeow.NewClient(device, waLog.Noop)
	client.EnableAutoReconnect = e.cfg.AutoReconnect
	client.AddEventHandler(func(evt any) {
		e.handleWAEvent(instanceID, mi, evt)
	})

	mi.clientRef.Store(client)
	mi.setStatus(engine.StatusDisconnected)
	e.log.Info().Str("instance", instanceID).Msg("Client reinitialized after logout — ready for new QR scan")
	return nil
}
