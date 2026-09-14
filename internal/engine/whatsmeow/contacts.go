package waengine

import (
	"context"
	"fmt"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/types"
	waevents "go.mau.fi/whatsmeow/types/events"

	"velix/internal/engine"
)

// GetContacts returns all locally cached contacts from the whatsmeow SQLite store.
// Only individual contacts are returned (groups and broadcasts are filtered out).
func (e *Engine) GetContacts(ctx context.Context, instanceID string) (map[string]engine.ContactInfo, error) {
	mi, err := e.getInstance(instanceID)
	if err != nil {
		return nil, err
	}

	raw, err := mi.getClient().Store.Contacts.GetAllContacts(ctx)
	if err != nil {
		return nil, fmt.Errorf("GetAllContacts: %w", err)
	}

	result := make(map[string]engine.ContactInfo, len(raw))
	for jid, info := range raw {
		// Skip groups, broadcasts, status, and newsletter JIDs.
		if jid.Server != types.DefaultUserServer {
			continue
		}
		result[jid.ToNonAD().String()] = engine.ContactInfo{
			JID:          jid.ToNonAD().String(),
			PushName:     info.PushName,
			BusinessName: info.BusinessName,
		}
	}
	return result, nil
}

// IsOnWhatsApp checks which phone numbers have WhatsApp accounts.
func (e *Engine) IsOnWhatsApp(ctx context.Context, instanceID string, phones []string) ([]engine.ContactCheck, error) {
	mi, err := e.getInstance(instanceID)
	if err != nil {
		return nil, err
	}

	resp, err := mi.getClient().IsOnWhatsApp(ctx, phones)
	if err != nil {
		return nil, fmt.Errorf("IsOnWhatsApp: %w", err)
	}

	results := make([]engine.ContactCheck, len(resp))
	for i, r := range resp {
		results[i] = engine.ContactCheck{
			Phone:  r.Query,
			JID:    r.JID.ToNonAD().String(),
			Exists: r.IsIn,
		}
	}
	return results, nil
}

// GetContactInfo returns metadata for a single WhatsApp contact.
// It combines the local device store (push name, business name) with a live
// profile-picture lookup.
func (e *Engine) GetContactInfo(ctx context.Context, instanceID, jid string) (engine.ContactInfo, error) {
	mi, err := e.getInstance(instanceID)
	if err != nil {
		return engine.ContactInfo{}, err
	}

	parsedJID, err := parseJID(jid)
	if err != nil {
		return engine.ContactInfo{}, fmt.Errorf("invalid JID %q: %w", jid, err)
	}

	// Pull cached contact data from the local store (push name / business name).
	contact, _ := mi.getClient().Store.Contacts.GetContact(ctx, parsedJID)

	// Fetch live UserInfo (status) — best-effort with short timeout so the
	// WhatsApp IQ round-trip cannot block the HTTP handler indefinitely.
	var status string
	infoCtx, infoCancel := context.WithTimeout(ctx, 5*time.Second)
	defer infoCancel()
	if infoMap, err := mi.getClient().GetUserInfo(infoCtx, []types.JID{parsedJID}); err == nil {
		if info, ok := infoMap[parsedJID]; ok {
			status = info.Status
		}
	}

	// Attempt profile picture thumbnail URL — best-effort, 5 s deadline.
	// Preview=true requests a thumbnail rather than the full-size URL, which
	// is faster and sufficient for display purposes. Callers wanting the
	// full URL can call the dedicated GetProfilePicture endpoint instead.
	picURL := ""
	picCtx, picCancel := context.WithTimeout(ctx, 5*time.Second)
	defer picCancel()
	if pic, err := mi.getClient().GetProfilePictureInfo(picCtx, parsedJID, &whatsmeow.GetProfilePictureParams{Preview: true}); err == nil && pic != nil {
		picURL = pic.URL
	}

	return engine.ContactInfo{
		JID:          parsedJID.ToNonAD().String(),
		PushName:     contact.PushName,
		BusinessName: contact.BusinessName,
		About:        status,
		PictureURL:   picURL,
	}, nil
}

func (e *Engine) GetBlocklist(ctx context.Context, instanceID string) ([]string, error) {
	mi, err := e.getInstance(instanceID)
	if err != nil {
		return nil, err
	}
	bl, err := mi.getClient().GetBlocklist(ctx)
	if err != nil {
		return nil, fmt.Errorf("get blocklist: %w", err)
	}
	jids := make([]string, len(bl.JIDs))
	for i, j := range bl.JIDs {
		jids[i] = j.ToNonAD().String()
	}
	return jids, nil
}

func (e *Engine) BlockContact(ctx context.Context, instanceID, jid string) error {
	mi, err := e.getInstance(instanceID)
	if err != nil {
		return err
	}
	parsed, err := parseJID(jid)
	if err != nil {
		return fmt.Errorf("invalid JID %q: %w", jid, err)
	}
	_, err = mi.getClient().UpdateBlocklist(ctx, parsed, waevents.BlocklistChangeActionBlock)
	return err
}

func (e *Engine) UnblockContact(ctx context.Context, instanceID, jid string) error {
	mi, err := e.getInstance(instanceID)
	if err != nil {
		return err
	}
	parsed, err := parseJID(jid)
	if err != nil {
		return fmt.Errorf("invalid JID %q: %w", jid, err)
	}
	_, err = mi.getClient().UpdateBlocklist(ctx, parsed, waevents.BlocklistChangeActionUnblock)
	return err
}
