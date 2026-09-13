package waengine

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	waProto "go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	waevents "go.mau.fi/whatsmeow/types/events"

	"velix/internal/engine"
)

// handleWAEvent receives every raw WhatsMeow event, translates it to our engine.Event
// type, updates managed instance status, and dispatches to all subscribers.
func (e *Engine) handleWAEvent(instanceID string, mi *managedInstance, evt any) {
	now := time.Now()

	switch v := evt.(type) {

	case *waevents.Connected:
		mi.setStatus(engine.StatusConnected)
		// Apply always_online presence after connection.
		if s := mi.getSettings(); s.AlwaysOnline {
			_ = mi.getClient().SendPresence(e.ctx, types.PresenceAvailable)
		}
		e.dispatch(engine.Event{
			Type:       engine.EventInstanceConnected,
			InstanceID: instanceID,
			Timestamp:  now,
		})

	case *waevents.Disconnected:
		cur := mi.getStatus()
		if cur == engine.StatusLoggedOut {
			// Never overwrite LoggedOut — reconnect is not expected after a forced logout.
			return
		}
		// Guard against stale out-of-order delivery: if the client already reconnected
		// (auto-reconnect goroutine raced ahead of this event), skip the status downgrade.
		if cur == engine.StatusConnected && mi.getClient().IsConnected() {
			e.log.Debug().Str("instance", instanceID).Msg("Stale Disconnected event dropped — client already reconnected")
			return
		}
		mi.setStatus(engine.StatusDisconnected)
		e.dispatch(engine.Event{
			Type:       engine.EventInstanceDisconnected,
			InstanceID: instanceID,
			Timestamp:  now,
		})

	case *waevents.LoggedOut:
		mi.setStatus(engine.StatusLoggedOut)
		e.dispatch(engine.Event{
			Type:       engine.EventInstanceLoggedOut,
			InstanceID: instanceID,
			Timestamp:  now,
		})
		// Reinitialize the client in a goroutine: whatsmeow deleted the device
		// from SQLite when this event fired, leaving mi.client pointing at a
		// deleted device. Without reinit, the next GetQRChannel call would fail
		// with "invalid use of deleted device". The goroutine avoids deadlocking
		// on whatsmeow's own event loop.
		go func() {
			if err := e.reinitClient(instanceID, mi); err != nil {
				e.log.Error().Err(err).Str("instance", instanceID).Msg("Failed to reinit client after logout")
			}
		}()

	case *waevents.PairSuccess:
		mi.setStatus(engine.StatusConnected)
		e.dispatch(engine.Event{
			Type:       engine.EventInstancePaired,
			InstanceID: instanceID,
			Payload: &engine.PairPayload{
				JID:          v.ID.ToNonAD().String(),
				Platform:     v.Platform,
				BusinessName: v.BusinessName,
			},
			Timestamp: now,
		})

	case *waevents.TemporaryBan:
		mi.setStatus(engine.StatusBanned)
		// Auto-disconnect to prevent further damage.
		if mi.getClient().IsConnected() {
			mi.getClient().Disconnect()
		}
		e.log.Warn().
			Str("instance", instanceID).
			Stringer("reason", v).
			Dur("expire", v.Expire).
			Msg("Instance received temporary ban — auto-disconnected")
		e.dispatch(engine.Event{
			Type:       engine.EventInstanceBanned,
			InstanceID: instanceID,
			Timestamp:  now,
		})

	case *waevents.CallOffer:
		// Auto-reject incoming calls if configured.
		if s := mi.getSettings(); s.RejectCall {
			_ = mi.getClient().RejectCall(e.ctx, v.From, v.CallID)
		}

	case *waevents.Message:
		settings := mi.getSettings()

		// Skip group messages if configured (own messages in groups are still dispatched).
		if v.Info.IsGroup && !v.Info.IsFromMe && settings.IgnoreGroups {
			return
		}

		// Auto-read incoming messages if configured (never mark own messages as read).
		if !v.Info.IsFromMe && settings.ReadMessages {
			_ = mi.getClient().MarkRead(
				e.ctx,
				[]types.MessageID{v.Info.ID},
				v.Info.Timestamp,
				v.Info.Chat,
				v.Info.Sender,
			)
		}

		payload := mapMessage(v, e.ctx, mi.getClient().Store)

		// Determine message source:
		//   contact — received from a client
		//   api     — sent by this system via the API (we registered the ID in markAPISent)
		//   manual  — sent by an operator from the phone or WhatsApp Web
		if !v.Info.IsFromMe {
			payload.Source = engine.MessageSourceContact
			// Check if this chat is under a human-pause window.
			if mi.isChatPaused(payload.Chat) {
				payload.Paused = true
			}
		} else if e.isAPISent(string(v.Info.ID)) {
			payload.Source = engine.MessageSourceAPI
			// API reply — activate human pause for this chat.
			mi.pauseChat(payload.Chat)
		} else {
			payload.Source = engine.MessageSourceManual
			// Manual reply — activate human pause for this chat.
			mi.pauseChat(payload.Chat)
		}

		// Media messages need to be downloaded from WhatsApp CDN before dispatching.
		// Do it in a goroutine to avoid blocking whatsmeow's event loop.
		if payload.Media != nil {
			go e.downloadMediaAndDispatch(instanceID, mi, v.Message, payload, now)
			return
		}

		e.dispatch(engine.Event{
			Type:       engine.EventMessageReceived,
			InstanceID: instanceID,
			Payload:    payload,
			Timestamp:  now,
		})

	case *waevents.HistorySync:
		go e.handleHistorySync(instanceID, mi, v)

	case *waevents.Receipt:
		payload := mapReceipt(v, e.ctx, mi.getClient().Store)
		evtType := engine.EventReceiptDelivered
		if v.Type == types.ReceiptTypeRead || v.Type == types.ReceiptTypeReadSelf {
			evtType = engine.EventReceiptRead
		}
		e.dispatch(engine.Event{
			Type:       evtType,
			InstanceID: instanceID,
			Payload:    payload,
			Timestamp:  now,
		})
	}
}

// ---------------------------------------------------------------------------
// Mappers
// ---------------------------------------------------------------------------

// resolveJID returns the @s.whatsapp.net JID when available, falling back to the
// raw JID. WhatsApp may address messages with a privacy-preserving @lid JID;
// SenderAlt (or RecipientAlt) carries the corresponding phone-number JID.
func resolveJID(primary, alt types.JID) string {
	if alt.Server == types.DefaultUserServer && !alt.IsEmpty() {
		return alt.ToNonAD().String()
	}
	return primary.ToNonAD().String()
}

// resolveToPN tries hard to return a @s.whatsapp.net JID string.
// It first checks the alt JID; if that's empty or not a phone-number JID, it
// falls back to the whatsmeow LID→PN store. Only returns a @lid as a last resort.
func resolveToPN(ctx context.Context, primary, alt types.JID, store lidResolver) string {
	// 1. Alt field has what we need.
	if alt.Server == types.DefaultUserServer && !alt.IsEmpty() {
		return alt.ToNonAD().String()
	}
	// 2. Primary is already a phone number — nothing to resolve.
	if primary.Server == types.DefaultUserServer {
		return primary.ToNonAD().String()
	}
	// 3. Primary is a @lid — ask the store for the PN mapping.
	if store != nil && primary.Server == types.HiddenUserServer {
		if pn, err := store.GetAltJID(ctx, primary); err == nil && !pn.IsEmpty() {
			return pn.ToNonAD().String()
		}
	}
	// 4. Last resort.
	return primary.ToNonAD().String()
}

// lidResolver is the subset of *store.Device we need for LID→PN lookups.
type lidResolver interface {
	GetAltJID(ctx context.Context, jid types.JID) (types.JID, error)
}

func mapMessage(v *waevents.Message, ctx context.Context, store lidResolver) *engine.MessagePayload {
	// Resolve Chat JID: in a DM, Chat == the other person.
	//   Incoming (client→us): Chat == Sender, so SenderAlt has the @s.whatsapp.net
	//   Outgoing echo (us→client): Chat == Recipient, so RecipientAlt has it
	//   Group: Chat is the group JID — no alt resolution needed.
	var chat string
	if v.Info.IsGroup {
		chat = v.Info.Chat.ToNonAD().String()
	} else if v.Info.IsFromMe {
		chat = resolveToPN(ctx, v.Info.Chat, v.Info.RecipientAlt, store)
	} else {
		chat = resolveToPN(ctx, v.Info.Chat, v.Info.SenderAlt, store)
	}

	p := &engine.MessagePayload{
		ID:        string(v.Info.ID),
		From:      resolveToPN(ctx, v.Info.Sender, v.Info.SenderAlt, store),
		Chat:      chat,
		FromMe:    v.Info.IsFromMe,
		IsGroup:   v.Info.IsGroup,
		Timestamp: v.Info.Timestamp,
		PushName:  v.Info.PushName,
	}

	msg := v.Message
	if msg == nil {
		p.Type = "unknown"
		return p
	}

	switch {
	case msg.GetConversation() != "":
		p.Type = "text"
		p.Text = msg.GetConversation()

	case msg.GetExtendedTextMessage() != nil:
		em := msg.GetExtendedTextMessage()
		p.Type = "text"
		p.Text = em.GetText()
		applyContextInfo(em.GetContextInfo(), p)

	case msg.GetImageMessage() != nil:
		im := msg.GetImageMessage()
		p.Type = "image"
		p.Text = im.GetCaption()
		p.Media = &engine.MediaInfo{
			MimeType: im.GetMimetype(),
			Caption:  im.GetCaption(),
			Size:     im.GetFileLength(),
		}
		applyContextInfo(im.GetContextInfo(), p)

	case msg.GetVideoMessage() != nil:
		vm := msg.GetVideoMessage()
		p.Type = "video"
		p.Text = vm.GetCaption()
		p.Media = &engine.MediaInfo{
			MimeType:        vm.GetMimetype(),
			Caption:         vm.GetCaption(),
			Size:            vm.GetFileLength(),
			DurationSeconds: vm.GetSeconds(),
		}
		applyContextInfo(vm.GetContextInfo(), p)

	case msg.GetAudioMessage() != nil:
		am := msg.GetAudioMessage()
		p.Type = "audio"
		p.Media = &engine.MediaInfo{
			MimeType:        am.GetMimetype(),
			Size:            am.GetFileLength(),
			DurationSeconds: am.GetSeconds(),
			IsVoiceNote:     am.GetPTT(),
		}
		applyContextInfo(am.GetContextInfo(), p)

	case msg.GetDocumentMessage() != nil:
		dm := msg.GetDocumentMessage()
		p.Type = "document"
		p.Media = &engine.MediaInfo{
			MimeType: dm.GetMimetype(),
			FileName: dm.GetFileName(),
			Caption:  dm.GetCaption(),
			Size:     dm.GetFileLength(),
		}
		applyContextInfo(dm.GetContextInfo(), p)

	case msg.GetStickerMessage() != nil:
		p.Type = "sticker"
		applyContextInfo(msg.GetStickerMessage().GetContextInfo(), p)

	case msg.GetLocationMessage() != nil:
		lm := msg.GetLocationMessage()
		p.Type = "location"
		p.Location = &engine.LocationInfo{
			Latitude:  lm.GetDegreesLatitude(),
			Longitude: lm.GetDegreesLongitude(),
			Name:      lm.GetName(),
			Address:   lm.GetAddress(),
			IsLive:    lm.GetIsLive(),
		}
		applyContextInfo(lm.GetContextInfo(), p)

	case msg.GetContactMessage() != nil:
		cm := msg.GetContactMessage()
		p.Type = "contact"
		p.VCard = &engine.VCardInfo{
			DisplayName: cm.GetDisplayName(),
			VCard:       cm.GetVcard(),
		}
		applyContextInfo(cm.GetContextInfo(), p)

	case msg.GetReactionMessage() != nil:
		rm := msg.GetReactionMessage()
		p.Type = "reaction"
		p.Text = rm.GetText()
		if key := rm.GetKey(); key != nil {
			p.ReactionTarget = key.GetID()
		}

	default:
		p.Type = "unknown"
	}

	return p
}

// applyContextInfo extracts quoted-message data, mentions, and forwarding flag
// from a ContextInfo protobuf and populates the corresponding payload fields.
func applyContextInfo(ctx *waProto.ContextInfo, p *engine.MessagePayload) {
	if ctx == nil {
		return
	}
	// Quoted message —————————————————————————————————————
	if id := ctx.GetStanzaID(); id != "" {
		p.QuotedID = id
		p.QuotedFrom = ctx.GetParticipant()
		if qm := ctx.GetQuotedMessage(); qm != nil {
			switch {
			case qm.GetConversation() != "":
				p.QuotedText = qm.GetConversation()
			case qm.GetExtendedTextMessage() != nil:
				p.QuotedText = qm.GetExtendedTextMessage().GetText()
			case qm.GetImageMessage() != nil:
				p.QuotedText = qm.GetImageMessage().GetCaption()
			case qm.GetVideoMessage() != nil:
				p.QuotedText = qm.GetVideoMessage().GetCaption()
			case qm.GetDocumentMessage() != nil:
				p.QuotedText = qm.GetDocumentMessage().GetFileName()
			case qm.GetAudioMessage() != nil:
				p.QuotedText = "[audio]"
			case qm.GetStickerMessage() != nil:
				p.QuotedText = "[sticker]"
			case qm.GetLocationMessage() != nil:
				p.QuotedText = "[location]"
			case qm.GetContactMessage() != nil:
				p.QuotedText = qm.GetContactMessage().GetDisplayName()
			}
		}
	}
	// Mentions ————————————————————————————————————————————
	if jids := ctx.GetMentionedJID(); len(jids) > 0 {
		p.Mentions = jids
	}
	// Forwarded ———————————————————————————————————————————
	if ctx.GetIsForwarded() {
		p.IsForwarded = true
	}
}

// downloadMediaAndDispatch downloads media from WhatsApp CDN, saves it to local
// storage, then dispatches the message event with the local path and URL filled in.
// Runs in its own goroutine — does NOT block whatsmeow's event loop.
func (e *Engine) downloadMediaAndDispatch(instanceID string, mi *managedInstance, msg *waProto.Message, payload *engine.MessagePayload, ts time.Time) {
	if e.cfg.MediaStorePath != "" {
		ctx, cancel := context.WithTimeout(e.ctx, 60*time.Second)
		defer cancel()

		data, err := mi.getClient().DownloadAny(ctx, msg)
		if err != nil {
			e.log.Warn().Err(err).Str("instance", instanceID).
				Str("msg_id", payload.ID).Msg("Failed to download media from WhatsApp CDN")
		} else if len(data) > 0 {
			ext := extFromMIME(payload.Media.MimeType)
			mediaID := uuid.NewString()
			destPath := filepath.Join(e.cfg.MediaStorePath, mediaID+ext)
			if werr := os.WriteFile(destPath, data, 0o644); werr != nil {
				e.log.Warn().Err(werr).Str("instance", instanceID).Msg("Failed to save media to disk")
			} else {
				payload.Media.LocalPath = destPath
				payload.Media.DirectURL = "/v1/media/" + mediaID
			}
			// Include base64 in webhook payload when the setting is enabled.
			if mi.getSettings().WebhookBase64 {
				payload.Media.Base64 = base64.StdEncoding.EncodeToString(data)
			}
		}
	}

	e.dispatch(engine.Event{
		Type:       engine.EventMessageReceived,
		InstanceID: instanceID,
		Payload:    payload,
		Timestamp:  ts,
	})
}

// extFromMIME returns a file extension for the given MIME type.
func extFromMIME(mimeType string) string {
	m := strings.ToLower(mimeType)
	switch {
	case strings.Contains(m, "jpeg") || strings.Contains(m, "jpg"):
		return ".jpg"
	case strings.Contains(m, "png"):
		return ".png"
	case strings.Contains(m, "gif"):
		return ".gif"
	case strings.Contains(m, "webp"):
		return ".webp"
	case strings.Contains(m, "mp4"):
		return ".mp4"
	case strings.Contains(m, "mpeg"):
		return ".mp3"
	case strings.Contains(m, "ogg"):
		return ".ogg"
	case strings.Contains(m, "opus"):
		return ".ogg"
	case strings.Contains(m, "pdf"):
		return ".pdf"
	case strings.Contains(m, "aac"):
		return ".aac"
	default:
		return ".bin"
	}
}

// handleHistorySync extracts messages from a WhatsApp history sync blob and
// dispatches them as an EventHistorySync event. Only text messages from
// individual chats (not groups) are included — media would require downloading
// from WhatsApp CDN which is unreliable for old messages.
func (e *Engine) handleHistorySync(instanceID string, mi *managedInstance, v *waevents.HistorySync) {
	defer func() {
		if r := recover(); r != nil {
			e.log.Error().Str("instance", instanceID).Interface("panic", r).Msg("Panic in handleHistorySync — recovered")
		}
	}()

	data := v.Data
	if data == nil {
		return
	}

	var msgs []engine.HistorySyncMessage

	store := mi.getClient().Store

	for _, conv := range data.GetConversations() {
		chatJID := conv.GetID()
		if chatJID == "" {
			continue
		}
		// Skip groups and broadcasts — only sync individual chats.
		if strings.Contains(chatJID, "@g.us") || strings.Contains(chatJID, "@broadcast") {
			continue
		}
		// Resolve LID JIDs to phone-number JIDs so contacts get proper phone numbers.
		chatJID = resolveHistoryJID(e.ctx, chatJID, store)

		for _, hsm := range conv.GetMessages() {
			webMsg := hsm.GetMessage()
			if webMsg == nil {
				continue
			}
			key := webMsg.GetKey()
			if key == nil {
				continue
			}

			ts := time.Unix(int64(webMsg.GetMessageTimestamp()), 0)
			msg := webMsg.GetMessage()
			if msg == nil {
				continue
			}

			// Extract text content.
			text := msg.GetConversation()
			msgType := "text"
			if text == "" && msg.GetExtendedTextMessage() != nil {
				text = msg.GetExtendedTextMessage().GetText()
			}
			if text == "" {
				// Tag media types but don't include content (can't download old media reliably).
				switch {
				case msg.GetImageMessage() != nil:
					msgType = "image"
					text = msg.GetImageMessage().GetCaption()
					if text == "" {
						text = "[imagem]"
					}
				case msg.GetVideoMessage() != nil:
					msgType = "video"
					text = "[vídeo]"
				case msg.GetAudioMessage() != nil:
					msgType = "audio"
					text = "[áudio]"
				case msg.GetDocumentMessage() != nil:
					msgType = "document"
					text = "[documento]"
				case msg.GetStickerMessage() != nil:
					msgType = "sticker"
					text = "[sticker]"
				case msg.GetLocationMessage() != nil:
					msgType = "location"
					lm := msg.GetLocationMessage()
					text = fmt.Sprintf("[localização: %.4f, %.4f]", lm.GetDegreesLatitude(), lm.GetDegreesLongitude())
				case msg.GetContactMessage() != nil:
					msgType = "contact"
					text = "[contato: " + msg.GetContactMessage().GetDisplayName() + "]"
				default:
					continue // Skip unsupported types.
				}
			}

			senderJID := key.GetRemoteJID()
			if key.GetFromMe() {
				senderJID = "me"
			} else {
				senderJID = resolveHistoryJID(e.ctx, senderJID, store)
			}

			msgs = append(msgs, engine.HistorySyncMessage{
				ChatJID:   chatJID,
				SenderJID: senderJID,
				FromMe:    key.GetFromMe(),
				MessageID: key.GetID(),
				Text:      text,
				Timestamp: ts,
				PushName:  webMsg.GetPushName(),
				Type:      msgType,
			})
		}
	}

	if len(msgs) == 0 {
		return
	}

	// Sort by timestamp ascending so Chatwoot gets messages in order.
	sort.Slice(msgs, func(i, j int) bool {
		return msgs[i].Timestamp.Before(msgs[j].Timestamp)
	})

	e.log.Info().
		Str("instance", instanceID).
		Int("messages", len(msgs)).
		Msg("History sync received")

	e.dispatch(engine.Event{
		Type:       engine.EventHistorySync,
		InstanceID: instanceID,
		Payload:    &engine.HistorySyncPayload{Messages: msgs},
		Timestamp:  time.Now(),
	})
}

// resolveHistoryJID converts a raw JID string from history sync into a
// phone-number JID. WhatsApp may use LID (Linked Identity) JIDs in history
// data — these are opaque numeric IDs that are meaningless to the user.
// When the local store has a LID→PN mapping, we replace it with the real number.
func resolveHistoryJID(ctx context.Context, raw string, store lidResolver) string {
	parsed, err := types.ParseJID(raw)
	if err != nil {
		return raw
	}
	if parsed.Server == types.HiddenUserServer && store != nil {
		if pn, err := store.GetAltJID(ctx, parsed); err == nil && !pn.IsEmpty() {
			return pn.ToNonAD().String()
		}
	}
	return parsed.ToNonAD().String()
}

func mapReceipt(v *waevents.Receipt, ctx context.Context, store lidResolver) *engine.ReceiptPayload {
	ids := make([]string, len(v.MessageIDs))
	for i, id := range v.MessageIDs {
		ids[i] = string(id)
	}

	// Same Chat alt-resolution logic as mapMessage:
	// receipts from us use RecipientAlt; receipts from others use SenderAlt.
	var chat string
	if v.MessageSource.IsGroup {
		chat = v.MessageSource.Chat.ToNonAD().String()
	} else if v.MessageSource.IsFromMe {
		chat = resolveToPN(ctx, v.MessageSource.Chat, v.MessageSource.RecipientAlt, store)
	} else {
		chat = resolveToPN(ctx, v.MessageSource.Chat, v.MessageSource.SenderAlt, store)
	}

	return &engine.ReceiptPayload{
		MessageIDs: ids,
		Chat:       chat,
		Sender:     resolveToPN(ctx, v.MessageSource.Sender, v.MessageSource.SenderAlt, store),
		Type:       string(v.Type),
		Timestamp:  v.Timestamp,
	}
}
