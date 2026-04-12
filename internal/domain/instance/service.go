package instance

import (
	"context"
	"fmt"

	"github.com/rs/zerolog"

	"velix/internal/engine"
	"velix/internal/logger"
)

// Service orchestrates instance lifecycle: persistence + engine management.
type Service struct {
	repo   Repository
	engine engine.Engine
	log    zerolog.Logger
}

// NewService creates a new instance service.
func NewService(repo Repository, eng engine.Engine) *Service {
	s := &Service{
		repo:   repo,
		engine: eng,
		log:    logger.New("instance-service"),
	}
	// Bridge engine events → DB updates.
	eng.Subscribe(s.handleEngineEvent)
	return s
}

// Create persists a new instance and registers it in the engine.
func (s *Service) Create(ctx context.Context, workspaceID, name, proxyURL string) (*Instance, error) {
	inst := &Instance{
		WorkspaceID: workspaceID,
		Name:        name,
		ProxyURL:    proxyURL,
		Status:      engine.StatusDisconnected,
	}

	created, err := s.repo.Create(ctx, inst)
	if err != nil {
		return nil, fmt.Errorf("persist instance: %w", err)
	}

	if err := s.engine.CreateInstance(ctx, created.ID, engine.InstanceOptions{
		ProxyURL: proxyURL,
	}); err != nil {
		// Roll back DB row — best effort; log if it also fails.
		if delErr := s.repo.Delete(ctx, created.ID); delErr != nil {
			s.log.Error().Err(delErr).Str("id", created.ID).Msg("Failed to delete instance after engine error")
		}
		return nil, fmt.Errorf("register in engine: %w", err)
	}

	s.log.Info().Str("id", created.ID).Str("workspace", workspaceID).Msg("Instance created")
	return created, nil
}

// Get returns a single instance by ID, verifying workspace ownership.
// The returned status reflects the real-time engine state, not the stale DB value.
func (s *Service) Get(ctx context.Context, workspaceID, instanceID string) (*Instance, error) {
	inst, err := s.repo.GetByID(ctx, instanceID)
	if err != nil {
		return nil, err
	}
	if inst.WorkspaceID != workspaceID {
		return nil, ErrNotFound
	}
	// Override DB status with live engine status so callers always see reality.
	if liveStatus, err := s.engine.GetStatus(inst.ID); err == nil {
		inst.Status = liveStatus
	}
	return inst, nil
}

// List returns instances for a workspace with pagination.
// Each instance's status is enriched with the real-time engine state.
func (s *Service) List(ctx context.Context, workspaceID string, limit, offset int) ([]*Instance, error) {
	instances, err := s.repo.ListByWorkspace(ctx, workspaceID, limit, offset)
	if err != nil {
		return nil, err
	}
	for _, inst := range instances {
		if liveStatus, err := s.engine.GetStatus(inst.ID); err == nil {
			inst.Status = liveStatus
		}
	}
	return instances, nil
}

// Delete disconnects, removes from engine, and deletes from DB.
func (s *Service) Delete(ctx context.Context, workspaceID, instanceID string) error {
	inst, err := s.Get(ctx, workspaceID, instanceID)
	if err != nil {
		return err
	}

	if err := s.engine.DeleteInstance(ctx, inst.ID); err != nil {
		return fmt.Errorf("remove from engine: %w", err)
	}

	return s.repo.Delete(ctx, instanceID)
}

// Connect starts the WhatsApp connection for an instance.
func (s *Service) Connect(ctx context.Context, workspaceID, instanceID string) error {
	inst, err := s.Get(ctx, workspaceID, instanceID)
	if err != nil {
		return err
	}
	return s.engine.Connect(ctx, inst.ID)
}

// Disconnect cleanly drops the WhatsApp WebSocket connection.
// Always updates the DB status to avoid stale "connected" rows when the client
// was already disconnected and no WA event fires.
func (s *Service) Disconnect(ctx context.Context, workspaceID, instanceID string) error {
	inst, err := s.Get(ctx, workspaceID, instanceID)
	if err != nil {
		return err
	}
	if err := s.engine.Disconnect(ctx, inst.ID); err != nil {
		return err
	}
	_ = s.repo.UpdateStatus(ctx, inst.ID, engine.StatusDisconnected)
	return nil
}

// Logout logs out from WhatsApp (clears session) and disconnects.
func (s *Service) Logout(ctx context.Context, workspaceID, instanceID string) error {
	inst, err := s.Get(ctx, workspaceID, instanceID)
	if err != nil {
		return err
	}
	return s.engine.Logout(ctx, inst.ID)
}

// GetStatus returns the live status from the engine.
func (s *Service) GetStatus(workspaceID, instanceID string) (Status, error) {
	// Lightweight — no DB call, engine holds the canonical live status.
	status, err := s.engine.GetStatus(instanceID)
	if err != nil {
		return "", ErrNotFound
	}
	_ = workspaceID // ownership already checked by caller via Get if needed
	return status, nil
}

// StartQRFlow opens a QR channel and connects; returns the event channel for SSE streaming.
func (s *Service) StartQRFlow(ctx context.Context, workspaceID, instanceID string) (<-chan engine.QREvent, error) {
	inst, err := s.Get(ctx, workspaceID, instanceID)
	if err != nil {
		return nil, err
	}

	qrChan, err := s.engine.GetQRChannel(ctx, inst.ID)
	if err != nil {
		return nil, fmt.Errorf("get QR channel: %w", err)
	}

	if err := s.engine.Connect(ctx, inst.ID); err != nil {
		return nil, fmt.Errorf("connect for QR: %w", err)
	}

	return qrChan, nil
}

// RequestPairCode requests a pairing code instead of QR; instance must be connecting.
func (s *Service) RequestPairCode(ctx context.Context, workspaceID, instanceID, phone string) (string, error) {
	inst, err := s.Get(ctx, workspaceID, instanceID)
	if err != nil {
		return "", err
	}
	return s.engine.RequestPairCode(ctx, inst.ID, phone)
}

// RestoreFromDB re-registers all persisted instances into the engine at startup.
// Instances that were connected are reconnected; others are left disconnected.
func (s *Service) RestoreFromDB(ctx context.Context) error {
	instances, err := s.repo.ListAll(ctx)
	if err != nil {
		return fmt.Errorf("list all instances: %w", err)
	}

	for _, inst := range instances {
		if err := s.engine.CreateInstance(ctx, inst.ID, engine.InstanceOptions{
			ProxyURL: inst.ProxyURL,
		}); err != nil {
			s.log.Warn().Err(err).Str("id", inst.ID).Msg("Failed to restore instance in engine")
			continue
		}

		// Push saved settings to engine.
		_ = s.engine.ApplySettings(ctx, inst.ID, engine.InstanceSettings{
			RejectCall:         inst.Settings.RejectCall,
			ReadMessages:       inst.Settings.ReadMessages,
			AlwaysOnline:       inst.Settings.AlwaysOnline,
			IgnoreGroups:       inst.Settings.IgnoreGroups,
			SyncFullHistory:    inst.Settings.SyncFullHistory,
			HumanPauseDuration: inst.Settings.HumanPauseDuration,
		})

		// Auto-reconnect if it was connected when the server last shut down.
		if inst.Status == engine.StatusConnected {
			if err := s.engine.Connect(ctx, inst.ID); err != nil {
				s.log.Warn().Err(err).Str("id", inst.ID).Msg("Failed to auto-reconnect instance — resetting status to disconnected")
				// Reset stale DB status so the UI doesn't show "connected" for an unreachable instance.
				_ = s.repo.UpdateStatus(ctx, inst.ID, engine.StatusDisconnected)
			} else {
				s.log.Info().Str("id", inst.ID).Msg("Instance auto-reconnected")
			}
		}
	}

	s.log.Info().Int("count", len(instances)).Msg("Instances restored from database")
	return nil
}

// ---------------------------------------------------------------------------
// Engine event handler
// ---------------------------------------------------------------------------

func (s *Service) handleEngineEvent(evt engine.Event) {
	ctx := context.Background()

	switch evt.Type {
	case engine.EventInstanceConnected:
		if err := s.repo.UpdateStatus(ctx, evt.InstanceID, engine.StatusConnected); err != nil {
			s.log.Error().Err(err).Str("id", evt.InstanceID).Msg("Failed to update status to connected")
		}

	case engine.EventInstanceDisconnected:
		if err := s.repo.UpdateStatus(ctx, evt.InstanceID, engine.StatusDisconnected); err != nil {
			s.log.Error().Err(err).Str("id", evt.InstanceID).Msg("Failed to update status to disconnected")
		}

	case engine.EventInstanceLoggedOut:
		if err := s.repo.UpdateStatus(ctx, evt.InstanceID, engine.StatusLoggedOut); err != nil {
			s.log.Error().Err(err).Str("id", evt.InstanceID).Msg("Failed to update status to logged_out")
		}

	case engine.EventInstanceBanned:
		if err := s.repo.UpdateStatus(ctx, evt.InstanceID, engine.StatusBanned); err != nil {
			s.log.Error().Err(err).Str("id", evt.InstanceID).Msg("Failed to update status to banned")
		}

	case engine.EventInstancePaired:
		p, ok := evt.Payload.(*engine.PairPayload)
		if !ok {
			return
		}
		if err := s.repo.UpdateAfterPair(ctx, evt.InstanceID, p.JID, p.Platform, p.BusinessName); err != nil {
			s.log.Error().Err(err).Str("id", evt.InstanceID).Msg("Failed to update instance after pair")
		}
	}
}

// GetSettings returns the settings for an instance, verifying workspace ownership.
func (s *Service) GetSettings(ctx context.Context, workspaceID, instanceID string) (*Settings, error) {
	inst, err := s.Get(ctx, workspaceID, instanceID)
	if err != nil {
		return nil, err
	}
	return &inst.Settings, nil
}

// UpdateSettings persists new settings and pushes behavioral changes to the engine.
func (s *Service) UpdateSettings(ctx context.Context, workspaceID, instanceID string, settings *Settings) (*Settings, error) {
	if _, err := s.Get(ctx, workspaceID, instanceID); err != nil {
		return nil, err
	}

	if err := s.repo.UpdateSettings(ctx, instanceID, settings); err != nil {
		return nil, fmt.Errorf("persist settings: %w", err)
	}

	// Push behavioral settings to engine (presence, auto-read, etc.).
	_ = s.engine.ApplySettings(ctx, instanceID, engine.InstanceSettings{
		RejectCall:         settings.RejectCall,
		ReadMessages:       settings.ReadMessages,
		AlwaysOnline:       settings.AlwaysOnline,
		IgnoreGroups:       settings.IgnoreGroups,
		SyncFullHistory:    settings.SyncFullHistory,
		HumanPauseDuration: settings.HumanPauseDuration,
	})

	s.log.Info().Str("id", instanceID).Msg("Instance settings updated")
	return settings, nil
}

// GetInstanceWebhookSettings returns the per-instance webhook URL and event filter.
// Used by webhook.Service to deliver events to instance-specific URLs.
func (s *Service) GetInstanceWebhookSettings(ctx context.Context, instanceID string) (string, []string, error) {
	inst, err := s.repo.GetByID(ctx, instanceID)
	if err != nil {
		return "", nil, err
	}
	return inst.Settings.WebhookURL, inst.Settings.WebhookEvents, nil
}

// InstanceBelongsToWorkspace checks ownership without returning the full entity.
// Used by middleware to guard cross-tenant access.
func (s *Service) InstanceBelongsToWorkspace(ctx context.Context, workspaceID, instanceID string) bool {
	inst, err := s.repo.GetByID(ctx, instanceID)
	if err != nil {
		return false
	}
	return inst.WorkspaceID == workspaceID
}

// SetPresence sends a typing/presence indicator for this instance.
func (s *Service) SetPresence(ctx context.Context, workspaceID, instanceID, to, presenceType string) error {
	if _, err := s.Get(ctx, workspaceID, instanceID); err != nil {
		return err
	}
	return s.engine.SetPresence(ctx, instanceID, to, presenceType)
}

// UpdateProfile updates the instance's display name and/or profile photo.
func (s *Service) UpdateProfile(ctx context.Context, workspaceID, instanceID, name string, photoData []byte) error {
	if _, err := s.Get(ctx, workspaceID, instanceID); err != nil {
		return err
	}
	return s.engine.UpdateProfile(ctx, instanceID, name, photoData)
}

// GetProfilePicture returns the profile picture URL for any JID (contact or group).
func (s *Service) GetProfilePicture(ctx context.Context, workspaceID, instanceID, jid string) (string, error) {
	if _, err := s.Get(ctx, workspaceID, instanceID); err != nil {
		return "", err
	}
	return s.engine.GetProfilePicture(ctx, instanceID, jid)
}

// ErrNotFound is returned when an instance does not exist or does not belong to the workspace.
var ErrNotFound = fmt.Errorf("instance not found")
