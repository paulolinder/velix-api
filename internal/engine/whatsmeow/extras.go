package waengine

import (
	"context"
	"fmt"
	"strings"

	"go.mau.fi/whatsmeow"
	waProto "go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"google.golang.org/protobuf/proto"

	"velix/internal/engine"
)

// ---------------------------------------------------------------------------
// SendStatus
// ---------------------------------------------------------------------------

// SendStatus posts a WhatsApp Status (Story) update.
// Text statuses appear with a coloured background; image/video statuses are
// uploaded to WhatsApp CDN and then broadcast to the status@broadcast JID.
func (e *Engine) SendStatus(ctx context.Context, instanceID string, p engine.StatusPayload) (engine.SentMessage, error) {
	mi, err := e.getInstance(instanceID)
	if err != nil {
		return engine.SentMessage{}, err
	}
	if mi.getStatus() == engine.StatusBanned {
		return engine.SentMessage{}, fmt.Errorf("instance is banned — cannot send status")
	}

	if err := waitForRateLimit(ctx, mi); err != nil {
		return engine.SentMessage{}, fmt.Errorf("rate limit: %w", err)
	}

	var msg *waProto.Message

	switch p.Type {
	case engine.StatusTypeText:
		bgColor := p.BackgroundColor
		if bgColor == 0 {
			bgColor = 0xFF000000 // default: opaque black
		}
		fontType := waProto.ExtendedTextMessage_FontType(p.FontType)
		msg = &waProto.Message{
			ExtendedTextMessage: &waProto.ExtendedTextMessage{
				Text:           proto.String(p.Caption),
				BackgroundArgb: proto.Uint32(bgColor),
				TextArgb:       proto.Uint32(0xFFFFFFFF), // white text
				Font:           &fontType,
			},
		}

	case engine.StatusTypeImage:
		uploaded, err := mi.client.Upload(ctx, p.Data, whatsmeow.MediaImage)
		if err != nil {
			return engine.SentMessage{}, fmt.Errorf("upload image: %w", err)
		}
		size := uint64(len(p.Data))
		msg = &waProto.Message{
			ImageMessage: &waProto.ImageMessage{
				Caption:       proto.String(p.Caption),
				Mimetype:      proto.String(p.MimeType),
				URL:           proto.String(uploaded.URL),
				DirectPath:    proto.String(uploaded.DirectPath),
				MediaKey:      uploaded.MediaKey,
				FileEncSHA256: uploaded.FileEncSHA256,
				FileSHA256:    uploaded.FileSHA256,
				FileLength:    proto.Uint64(size),
			},
		}

	case engine.StatusTypeVideo:
		uploaded, err := mi.client.Upload(ctx, p.Data, whatsmeow.MediaVideo)
		if err != nil {
			return engine.SentMessage{}, fmt.Errorf("upload video: %w", err)
		}
		size := uint64(len(p.Data))
		msg = &waProto.Message{
			VideoMessage: &waProto.VideoMessage{
				Caption:       proto.String(p.Caption),
				Mimetype:      proto.String(p.MimeType),
				URL:           proto.String(uploaded.URL),
				DirectPath:    proto.String(uploaded.DirectPath),
				MediaKey:      uploaded.MediaKey,
				FileEncSHA256: uploaded.FileEncSHA256,
				FileSHA256:    uploaded.FileSHA256,
				FileLength:    proto.Uint64(size),
			},
		}

	default:
		return engine.SentMessage{}, fmt.Errorf("unsupported status type %q: must be text, image, or video", p.Type)
	}

	resp, err := mi.client.SendMessage(ctx, types.StatusBroadcastJID, msg)
	if err != nil {
		return engine.SentMessage{}, fmt.Errorf("send status: %w", err)
	}
	e.markAPISent(resp.ID)
	return engine.SentMessage{ID: resp.ID, Timestamp: resp.Timestamp}, nil
}

// ---------------------------------------------------------------------------
// SendLocation
// ---------------------------------------------------------------------------

func (e *Engine) SendLocation(ctx context.Context, instanceID, to string, lat, lng float64, name, address string) (engine.SentMessage, error) {
	mi, err := e.getInstance(instanceID)
	if err != nil {
		return engine.SentMessage{}, err
	}
	if s := mi.getStatus(); s == engine.StatusBanned {
		return engine.SentMessage{}, fmt.Errorf("instance is banned — cannot send messages")
	}

	jid, err := parseJID(to)
	if err != nil {
		return engine.SentMessage{}, fmt.Errorf("invalid JID %q: %w", to, err)
	}

	if err := waitForRateLimit(ctx, mi); err != nil {
		return engine.SentMessage{}, fmt.Errorf("rate limit: %w", err)
	}

	msg := &waProto.Message{
		LocationMessage: &waProto.LocationMessage{
			DegreesLatitude:  proto.Float64(lat),
			DegreesLongitude: proto.Float64(lng),
			Name:             proto.String(name),
			Address:          proto.String(address),
		},
	}

	resp, err := mi.client.SendMessage(ctx, jid, msg)
	if err != nil {
		return engine.SentMessage{}, fmt.Errorf("send location: %w", err)
	}
	e.markAPISent(resp.ID)
	return engine.SentMessage{ID: resp.ID, Timestamp: resp.Timestamp}, nil
}

// ---------------------------------------------------------------------------
// SendPoll
// ---------------------------------------------------------------------------

func (e *Engine) SendPoll(ctx context.Context, instanceID, to, question string, options []string, multiSelect bool) (engine.SentMessage, error) {
	mi, err := e.getInstance(instanceID)
	if err != nil {
		return engine.SentMessage{}, err
	}
	if s := mi.getStatus(); s == engine.StatusBanned {
		return engine.SentMessage{}, fmt.Errorf("instance is banned — cannot send messages")
	}

	jid, err := parseJID(to)
	if err != nil {
		return engine.SentMessage{}, fmt.Errorf("invalid JID %q: %w", to, err)
	}

	if err := waitForRateLimit(ctx, mi); err != nil {
		return engine.SentMessage{}, fmt.Errorf("rate limit: %w", err)
	}

	maxAnswers := 1
	if multiSelect {
		maxAnswers = len(options)
	}

	msg := mi.client.BuildPollCreation(question, options, maxAnswers)

	resp, err := mi.client.SendMessage(ctx, jid, msg)
	if err != nil {
		return engine.SentMessage{}, fmt.Errorf("send poll: %w", err)
	}
	e.markAPISent(resp.ID)
	return engine.SentMessage{ID: resp.ID, Timestamp: resp.Timestamp}, nil
}

// ---------------------------------------------------------------------------
// SendContact
// ---------------------------------------------------------------------------

// buildVCard constructs a minimal vCard 3.0 string from a ContactCard.
func buildVCard(c engine.ContactCard) string {
	var sb strings.Builder
	sb.WriteString("BEGIN:VCARD\r\n")
	sb.WriteString("VERSION:3.0\r\n")
	sb.WriteString("FN:" + c.DisplayName + "\r\n")
	sb.WriteString("TEL;type=CELL;type=VOICE;waid=" + c.Phone + ":+" + c.Phone + "\r\n")
	if c.Organization != "" {
		sb.WriteString("ORG:" + c.Organization + "\r\n")
	}
	if c.Email != "" {
		sb.WriteString("EMAIL:" + c.Email + "\r\n")
	}
	sb.WriteString("END:VCARD\r\n")
	return sb.String()
}

func (e *Engine) SendContact(ctx context.Context, instanceID, to string, contacts []engine.ContactCard) (engine.SentMessage, error) {
	mi, err := e.getInstance(instanceID)
	if err != nil {
		return engine.SentMessage{}, err
	}
	if s := mi.getStatus(); s == engine.StatusBanned {
		return engine.SentMessage{}, fmt.Errorf("instance is banned — cannot send messages")
	}
	if len(contacts) == 0 {
		return engine.SentMessage{}, fmt.Errorf("contacts list is empty")
	}

	jid, err := parseJID(to)
	if err != nil {
		return engine.SentMessage{}, fmt.Errorf("invalid JID %q: %w", to, err)
	}

	if err := waitForRateLimit(ctx, mi); err != nil {
		return engine.SentMessage{}, fmt.Errorf("rate limit: %w", err)
	}

	var msg *waProto.Message

	if len(contacts) == 1 {
		vcard := buildVCard(contacts[0])
		msg = &waProto.Message{
			ContactMessage: &waProto.ContactMessage{
				DisplayName: proto.String(contacts[0].DisplayName),
				Vcard:       proto.String(vcard),
			},
		}
	} else {
		contactMsgs := make([]*waProto.ContactMessage, len(contacts))
		for i, c := range contacts {
			vcard := buildVCard(c)
			contactMsgs[i] = &waProto.ContactMessage{
				DisplayName: proto.String(c.DisplayName),
				Vcard:       proto.String(vcard),
			}
		}
		msg = &waProto.Message{
			ContactsArrayMessage: &waProto.ContactsArrayMessage{
				Contacts: contactMsgs,
			},
		}
	}

	resp, err := mi.client.SendMessage(ctx, jid, msg)
	if err != nil {
		return engine.SentMessage{}, fmt.Errorf("send contact: %w", err)
	}
	e.markAPISent(resp.ID)
	return engine.SentMessage{ID: resp.ID, Timestamp: resp.Timestamp}, nil
}

// ---------------------------------------------------------------------------
// GetProfilePicture
// ---------------------------------------------------------------------------

func (e *Engine) GetProfilePicture(ctx context.Context, instanceID, jid string) (string, error) {
	mi, err := e.getInstance(instanceID)
	if err != nil {
		return "", err
	}

	parsedJID, err := parseJID(jid)
	if err != nil {
		return "", fmt.Errorf("invalid JID %q: %w", jid, err)
	}

	info, err := mi.client.GetProfilePictureInfo(ctx, parsedJID, &whatsmeow.GetProfilePictureParams{Preview: false})
	if err != nil {
		return "", nil
	}
	if info == nil {
		return "", nil
	}
	return info.URL, nil
}

// ---------------------------------------------------------------------------
// SetPresence
// ---------------------------------------------------------------------------

func (e *Engine) SetPresence(ctx context.Context, instanceID, to, presenceType string) error {
	mi, err := e.getInstance(instanceID)
	if err != nil {
		return err
	}

	switch presenceType {
	case "available":
		return mi.client.SendPresence(ctx, types.PresenceAvailable)
	case "unavailable":
		return mi.client.SendPresence(ctx, types.PresenceUnavailable)
	case "typing", "recording", "paused":
		chatJID, err := parseJID(to)
		if err != nil {
			return fmt.Errorf("invalid JID %q: %w", to, err)
		}

		var chatPresence types.ChatPresence
		var media types.ChatPresenceMedia

		switch presenceType {
		case "typing":
			chatPresence = types.ChatPresenceComposing
			media = types.ChatPresenceMediaText
		case "recording":
			chatPresence = types.ChatPresenceComposing
			media = types.ChatPresenceMediaAudio
		case "paused":
			chatPresence = types.ChatPresencePaused
			media = types.ChatPresenceMediaText
		}

		return mi.client.SendChatPresence(ctx, chatJID, chatPresence, media)
	default:
		return fmt.Errorf("unknown presence type %q: must be one of typing, recording, paused, available, unavailable", presenceType)
	}
}

// ---------------------------------------------------------------------------
// UpdateProfile
// ---------------------------------------------------------------------------

func (e *Engine) UpdateProfile(ctx context.Context, instanceID, name string, photoData []byte) error {
	_, err := e.getInstance(instanceID)
	if err != nil {
		return err
	}

	// whatsmeow does not expose a public API to update the own display name
	// or profile picture for a regular (non-newsletter) account.
	// Log and return nil so callers are not broken; implement when whatsmeow
	// adds the necessary RPCs.
	e.log.Warn().
		Str("instance", instanceID).
		Str("name", name).
		Bool("has_photo", photoData != nil).
		Msg("profile update not supported by whatsmeow")
	return nil
}
