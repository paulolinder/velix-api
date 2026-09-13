package waengine

import (
	"context"
	"fmt"
	"strings"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/types"

	"velix/internal/engine"
)

// CreateGroup creates a new WhatsApp group with the given name and initial participants.
func (e *Engine) CreateGroup(ctx context.Context, instanceID string, req engine.CreateGroupRequest) (engine.GroupInfo, error) {
	mi, err := e.getInstance(instanceID)
	if err != nil {
		return engine.GroupInfo{}, err
	}

	participants := make([]types.JID, 0, len(req.Participants))
	for _, p := range req.Participants {
		jid, err := parseJID(p)
		if err != nil {
			return engine.GroupInfo{}, fmt.Errorf("invalid participant JID %q: %w", p, err)
		}
		participants = append(participants, jid)
	}

	info, err := mi.client.CreateGroup(ctx, whatsmeow.ReqCreateGroup{
		Name:         req.Name,
		Participants: participants,
	})
	if err != nil {
		return engine.GroupInfo{}, fmt.Errorf("create group: %w", err)
	}

	return mapGroupInfo(info), nil
}

// GetGroupInfo returns metadata and participant list for a group.
func (e *Engine) GetGroupInfo(ctx context.Context, instanceID, groupJID string) (engine.GroupInfo, error) {
	mi, err := e.getInstance(instanceID)
	if err != nil {
		return engine.GroupInfo{}, err
	}

	jid, err := parseGroupJID(groupJID)
	if err != nil {
		return engine.GroupInfo{}, fmt.Errorf("invalid group JID %q: %w", groupJID, err)
	}

	info, err := mi.client.GetGroupInfo(ctx, jid)
	if err != nil {
		return engine.GroupInfo{}, fmt.Errorf("get group info: %w", err)
	}

	return mapGroupInfo(info), nil
}

// UpdateGroupParticipants adds, removes, promotes, or demotes participants.
// action must be one of: "add", "remove", "promote", "demote".
func (e *Engine) UpdateGroupParticipants(ctx context.Context, instanceID, groupJID string, participants []string, action string) error {
	mi, err := e.getInstance(instanceID)
	if err != nil {
		return err
	}

	jid, err := parseGroupJID(groupJID)
	if err != nil {
		return fmt.Errorf("invalid group JID %q: %w", groupJID, err)
	}

	pJIDs := make([]types.JID, 0, len(participants))
	for _, p := range participants {
		pjid, err := parseJID(p)
		if err != nil {
			return fmt.Errorf("invalid participant JID %q: %w", p, err)
		}
		pJIDs = append(pJIDs, pjid)
	}

	waAction, err := toParticipantChange(action)
	if err != nil {
		return err
	}

	_, err = mi.client.UpdateGroupParticipants(ctx, jid, pJIDs, waAction)
	return err
}

// LeaveGroup leaves a WhatsApp group.
func (e *Engine) LeaveGroup(ctx context.Context, instanceID, groupJID string) error {
	mi, err := e.getInstance(instanceID)
	if err != nil {
		return err
	}

	jid, err := parseGroupJID(groupJID)
	if err != nil {
		return fmt.Errorf("invalid group JID %q: %w", groupJID, err)
	}

	return mi.client.LeaveGroup(ctx, jid)
}

// GetJoinedGroups returns all groups the instance is a member of.
func (e *Engine) GetJoinedGroups(ctx context.Context, instanceID string) ([]engine.GroupInfo, error) {
	mi, err := e.getInstance(instanceID)
	if err != nil {
		return nil, err
	}

	groups, err := mi.client.GetJoinedGroups(ctx)
	if err != nil {
		return nil, fmt.Errorf("get joined groups: %w", err)
	}

	results := make([]engine.GroupInfo, len(groups))
	for i, g := range groups {
		results[i] = mapGroupInfo(g)
	}
	return results, nil
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// parseGroupJID parses a group JID (adds @g.us suffix if missing).
func parseGroupJID(s string) (types.JID, error) {
	if len(s) > 0 && s[len(s)-1] != ')' {
		// If no @, assume it's a numeric group ID
		for _, c := range s {
			if c == '@' {
				return types.ParseJID(s)
			}
		}
		return types.ParseJID(s + "@g.us")
	}
	return types.ParseJID(s)
}

func toParticipantChange(action string) (whatsmeow.ParticipantChange, error) {
	switch action {
	case "add":
		return whatsmeow.ParticipantChangeAdd, nil
	case "remove":
		return whatsmeow.ParticipantChangeRemove, nil
	case "promote":
		return whatsmeow.ParticipantChangePromote, nil
	case "demote":
		return whatsmeow.ParticipantChangeDemote, nil
	default:
		return "", fmt.Errorf("unknown participant action %q: must be add|remove|promote|demote", action)
	}
}

func (e *Engine) GetGroupInviteLink(ctx context.Context, instanceID, groupJID string, reset bool) (string, error) {
	mi, err := e.getInstance(instanceID)
	if err != nil {
		return "", err
	}
	jid, err := parseGroupJID(groupJID)
	if err != nil {
		return "", fmt.Errorf("invalid group JID %q: %w", groupJID, err)
	}
	link, err := mi.client.GetGroupInviteLink(ctx, jid, reset)
	if err != nil {
		return "", fmt.Errorf("get invite link: %w", err)
	}
	return link, nil
}

func (e *Engine) JoinGroupWithLink(ctx context.Context, instanceID, link string) (string, error) {
	mi, err := e.getInstance(instanceID)
	if err != nil {
		return "", err
	}
	// Accept full URL (https://chat.whatsapp.com/CODE) or bare code.
	code := link
	if idx := strings.LastIndex(link, "/"); idx >= 0 {
		code = link[idx+1:]
	}
	groupJID, err := mi.client.JoinGroupWithLink(ctx, code)
	if err != nil {
		return "", fmt.Errorf("join group: %w", err)
	}
	return groupJID.ToNonAD().String(), nil
}

func (e *Engine) SetGroupName(ctx context.Context, instanceID, groupJID, name string) error {
	mi, err := e.getInstance(instanceID)
	if err != nil {
		return err
	}
	jid, err := parseGroupJID(groupJID)
	if err != nil {
		return fmt.Errorf("invalid group JID %q: %w", groupJID, err)
	}
	return mi.client.SetGroupName(ctx, jid, name)
}

func (e *Engine) SetGroupDescription(ctx context.Context, instanceID, groupJID, description string) error {
	mi, err := e.getInstance(instanceID)
	if err != nil {
		return err
	}
	jid, err := parseGroupJID(groupJID)
	if err != nil {
		return fmt.Errorf("invalid group JID %q: %w", groupJID, err)
	}
	return mi.client.SetGroupDescription(ctx, jid, description)
}

func (e *Engine) SetGroupPhoto(ctx context.Context, instanceID, groupJID string, photo []byte) error {
	mi, err := e.getInstance(instanceID)
	if err != nil {
		return err
	}
	jid, err := parseGroupJID(groupJID)
	if err != nil {
		return fmt.Errorf("invalid group JID %q: %w", groupJID, err)
	}
	_, err = mi.client.SetGroupPhoto(ctx, jid, photo)
	return err
}

func mapGroupInfo(g *types.GroupInfo) engine.GroupInfo {
	participants := make([]engine.GroupParticipant, len(g.Participants))
	for i, p := range g.Participants {
		participants[i] = engine.GroupParticipant{
			JID:   p.JID.ToNonAD().String(),
			Admin: p.IsAdmin || p.IsSuperAdmin,
		}
	}
	return engine.GroupInfo{
		JID:          g.JID.ToNonAD().String(),
		Name:         g.GroupName.Name,
		Topic:        g.GroupTopic.Topic,
		Participants: participants,
		IsAnnounce:   g.GroupAnnounce.IsAnnounce,
		IsLocked:     g.GroupLocked.IsLocked,
		CreatedAt:    g.GroupCreated,
	}
}
