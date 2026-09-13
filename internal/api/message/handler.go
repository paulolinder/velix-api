package message

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	apipkg "velix/internal/api"
	"velix/internal/domain/auth"
	"velix/internal/domain/message"
	"velix/internal/engine"
	"velix/internal/server/middleware"
)

// Handler holds the message service for all HTTP handlers.
type Handler struct {
	svc *message.Service
}

// NewHandler creates a new message HTTP handler.
func NewHandler(svc *message.Service) *Handler {
	return &Handler{svc: svc}
}

// Routes mounts all message sub-routes under /v1/instances/{instanceID}/messages.
func Routes(svc *message.Service) http.Handler {
	h := NewHandler(svc)
	r := chi.NewRouter()

	send := middleware.RequirePermission(auth.PermMessagesSend)
	view := middleware.RequirePermission(auth.PermMessagesView)
	schedule := middleware.RequirePermission(auth.PermMessagesSchedule)

	r.With(send).Post("/text", h.SendText)
	r.With(send).Post("/media", h.SendMedia)
	r.With(send).Post("/reaction", h.SendReaction)
	r.With(send).Post("/read", h.MarkAsRead)
	r.With(send).Post("/batch", h.BatchSend)
	r.With(send).Post("/location", h.SendLocation)
	r.With(send).Post("/poll", h.SendPoll)
	r.With(send).Post("/contact", h.SendContact)
	r.With(send).Post("/status", h.SendStatus)
	r.With(send).Post("/edit", h.EditMessage)
	r.With(send).Post("/disappearing", h.SetDisappearingTimer)

	r.With(view).Get("/", h.ListByChat)
	r.With(schedule).Get("/scheduled", h.ListScheduled)
	r.With(view).Get("/search", h.SearchMessages)

	r.With(send).Delete("/{msgID}", h.RevokeMessage)
	r.With(schedule).Delete("/{msgID}/schedule", h.CancelScheduled)

	return r
}

// ---------------------------------------------------------------------------
// Send handlers
// ---------------------------------------------------------------------------

// SendText handles POST /v1/instances/{instanceID}/messages/text.
func (h *Handler) SendText(w http.ResponseWriter, r *http.Request) {
	instanceID := apipkg.Param(r, "instanceID")

	var req SendTextRequest
	if !apipkg.DecodeJSON(w, r, &req) {
		return
	}
	if !apipkg.RequireFields(w, r, map[string]string{"to": req.To, "text": req.Text}) {
		return
	}

	var opts []engine.SendOptions
	if req.QuotedMessageID != "" {
		opts = append(opts, engine.SendOptions{QuotedMessageID: req.QuotedMessageID})
	}

	msg, err := h.svc.SendText(r.Context(), instanceID, req.To, req.Text, req.SendAt, opts...)
	if err != nil {
		apipkg.LogAndFail(w, r, err, "send text")
		return
	}

	status := http.StatusCreated
	if msg.Status == message.StatusScheduled {
		status = http.StatusAccepted
	}
	apipkg.WriteJSON(w, r, status, fromDomain(msg))
}

// SendMedia handles POST /v1/instances/{instanceID}/messages/media.
func (h *Handler) SendMedia(w http.ResponseWriter, r *http.Request) {
	instanceID := apipkg.Param(r, "instanceID")

	var req SendMediaRequest
	if !apipkg.DecodeJSON(w, r, &req) {
		return
	}
	if !apipkg.RequireFields(w, r, map[string]string{
		"to":        req.To,
		"type":      req.Type,
		"data":      req.DataB64,
		"mime_type": req.MimeType,
	}) {
		return
	}

	data, err := base64.StdEncoding.DecodeString(req.DataB64)
	if err != nil {
		apipkg.WriteError(w, r, apipkg.NewError(apipkg.ErrCodeValidation, "data must be valid base64"))
		return
	}

	mediaType := engine.MediaType(req.Type)
	switch mediaType {
	case engine.MediaTypeImage, engine.MediaTypeVideo, engine.MediaTypeAudio,
		engine.MediaTypeDocument, engine.MediaTypeSticker:
	default:
		apipkg.WriteError(w, r, apipkg.NewError(apipkg.ErrCodeValidation, "type must be one of: image, video, audio, document, sticker"))
		return
	}

	msg, err := h.svc.SendMedia(r.Context(), instanceID, req.To, req.SendAt, engine.MediaPayload{
		Type: mediaType, Data: data, FileName: req.FileName, MimeType: req.MimeType, Caption: req.Caption,
	})
	if err != nil {
		apipkg.LogAndFail(w, r, err, "send media")
		return
	}

	status := http.StatusCreated
	if msg.Status == message.StatusScheduled {
		status = http.StatusAccepted
	}
	apipkg.WriteJSON(w, r, status, fromDomain(msg))
}

// BatchSend handles POST /v1/instances/{instanceID}/messages/batch.
func (h *Handler) BatchSend(w http.ResponseWriter, r *http.Request) {
	instanceID := apipkg.Param(r, "instanceID")

	var req BatchTextRequest
	if !apipkg.DecodeJSON(w, r, &req) {
		return
	}
	if len(req.Messages) == 0 {
		apipkg.WriteError(w, r, apipkg.NewError(apipkg.ErrCodeValidation, "messages array must not be empty"))
		return
	}
	if len(req.Messages) > 100 {
		apipkg.WriteError(w, r, apipkg.NewError(apipkg.ErrCodeValidation, "batch size limit is 100 messages"))
		return
	}

	items := make([]message.BatchItem, len(req.Messages))
	for i, m := range req.Messages {
		items[i] = message.BatchItem{To: m.To, Text: m.Text, SendAt: m.SendAt}
	}

	results := h.svc.BatchSendText(r.Context(), instanceID, items)

	out := make([]BatchResultItem, len(results))
	for i, res := range results {
		out[i] = BatchResultItem{Index: res.Index, Error: res.Error}
		if res.Message != nil {
			out[i].Message = fromDomain(res.Message)
		}
	}
	apipkg.WriteJSON(w, r, http.StatusMultiStatus, out)
}

// ---------------------------------------------------------------------------
// Scheduling handlers
// ---------------------------------------------------------------------------

// ListScheduled handles GET /v1/instances/{instanceID}/messages/scheduled.
func (h *Handler) ListScheduled(w http.ResponseWriter, r *http.Request) {
	instanceID := apipkg.Param(r, "instanceID")
	pg := apipkg.ParsePagination(r)

	msgs, err := h.svc.ListScheduled(r.Context(), instanceID, pg.Limit, pg.Offset)
	if err != nil {
		apipkg.LogAndFail(w, r, err, "list scheduled")
		return
	}

	out := make([]*MessageResponse, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, fromDomain(m))
	}
	apipkg.WriteJSON(w, r, http.StatusOK, out)
}

// CancelScheduled handles DELETE /v1/instances/{instanceID}/messages/{msgID}/schedule.
func (h *Handler) CancelScheduled(w http.ResponseWriter, r *http.Request) {
	instanceID := apipkg.Param(r, "instanceID")
	msgID := apipkg.Param(r, "msgID")

	if err := h.svc.CancelScheduled(r.Context(), instanceID, msgID); err != nil {
		apipkg.LogAndFail(w, r, err, "cancel scheduled")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---------------------------------------------------------------------------
// Other handlers
// ---------------------------------------------------------------------------

// SendReaction handles POST /v1/instances/{instanceID}/messages/reaction.
func (h *Handler) SendReaction(w http.ResponseWriter, r *http.Request) {
	instanceID := apipkg.Param(r, "instanceID")

	var req SendReactionRequest
	if !apipkg.DecodeJSON(w, r, &req) {
		return
	}
	if !apipkg.RequireFields(w, r, map[string]string{"to": req.To, "message_id": req.MessageID}) {
		return
	}

	if err := h.svc.SendReaction(r.Context(), instanceID, req.To, req.MessageID, req.Reaction, req.FromMe); err != nil {
		apipkg.LogAndFail(w, r, err, "send reaction")
		return
	}
	apipkg.WriteJSON(w, r, http.StatusOK, map[string]string{"status": "sent"})
}

// MarkAsRead handles POST /v1/instances/{instanceID}/messages/read.
func (h *Handler) MarkAsRead(w http.ResponseWriter, r *http.Request) {
	instanceID := apipkg.Param(r, "instanceID")

	var req MarkReadRequest
	if !apipkg.DecodeJSON(w, r, &req) {
		return
	}
	if req.Chat == "" || len(req.MessageIDs) == 0 {
		apipkg.WriteError(w, r, apipkg.NewErrorWithDetails(apipkg.ErrCodeValidation, "Validation failed", map[string]string{
			"chat": "required", "message_ids": "required, at least one",
		}))
		return
	}

	if err := h.svc.MarkAsRead(r.Context(), instanceID, req.Chat, req.MessageIDs); err != nil {
		apipkg.LogAndFail(w, r, err, "mark read")
		return
	}
	apipkg.WriteJSON(w, r, http.StatusOK, map[string]string{"status": "ok"})
}

// ListByChat handles GET /v1/instances/{instanceID}/messages?chat=JID&limit=50&offset=0.
func (h *Handler) ListByChat(w http.ResponseWriter, r *http.Request) {
	instanceID := apipkg.Param(r, "instanceID")

	chatJID := r.URL.Query().Get("chat")
	if chatJID == "" {
		apipkg.WriteError(w, r, apipkg.NewError(apipkg.ErrCodeValidation, "chat query parameter required"))
		return
	}

	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))

	msgs, err := h.svc.ListByChat(r.Context(), instanceID, chatJID, limit, offset)
	if err != nil {
		apipkg.LogAndFail(w, r, err, "list messages")
		return
	}

	out := make([]*MessageResponse, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, fromDomain(m))
	}
	apipkg.WriteJSON(w, r, http.StatusOK, out)
}

// RevokeMessage handles DELETE /v1/instances/{instanceID}/messages/{msgID}.
func (h *Handler) RevokeMessage(w http.ResponseWriter, r *http.Request) {
	instanceID := apipkg.Param(r, "instanceID")
	msgID := apipkg.Param(r, "msgID")

	var req RevokeRequest
	if !apipkg.DecodeJSON(w, r, &req) {
		return
	}
	if !apipkg.RequireFields(w, r, map[string]string{"to": req.To}) {
		return
	}

	if err := h.svc.RevokeMessage(r.Context(), instanceID, req.To, msgID); err != nil {
		apipkg.LogAndFail(w, r, err, "revoke message")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// SendLocation handles POST /v1/instances/{instanceID}/messages/location.
func (h *Handler) SendLocation(w http.ResponseWriter, r *http.Request) {
	instanceID := apipkg.Param(r, "instanceID")
	var req SendLocationRequest
	if !apipkg.DecodeJSON(w, r, &req) {
		return
	}
	if !apipkg.RequireFields(w, r, map[string]string{"to": req.To}) {
		return
	}
	msg, err := h.svc.SendLocation(r.Context(), instanceID, req.To, req.Lat, req.Lng, req.Name, req.Address)
	if err != nil {
		apipkg.LogAndFail(w, r, err, "send location")
		return
	}
	apipkg.WriteJSON(w, r, http.StatusCreated, fromDomain(msg))
}

// SendPoll handles POST /v1/instances/{instanceID}/messages/poll.
func (h *Handler) SendPoll(w http.ResponseWriter, r *http.Request) {
	instanceID := apipkg.Param(r, "instanceID")
	var req SendPollRequest
	if !apipkg.DecodeJSON(w, r, &req) {
		return
	}
	if !apipkg.RequireFields(w, r, map[string]string{"to": req.To, "question": req.Question}) {
		return
	}
	if len(req.Options) < 2 {
		apipkg.WriteError(w, r, apipkg.NewError(apipkg.ErrCodeValidation, "poll requires at least 2 options"))
		return
	}
	msg, err := h.svc.SendPoll(r.Context(), instanceID, req.To, req.Question, req.Options, req.MultiSelect)
	if err != nil {
		apipkg.LogAndFail(w, r, err, "send poll")
		return
	}
	apipkg.WriteJSON(w, r, http.StatusCreated, fromDomain(msg))
}

// SendContact handles POST /v1/instances/{instanceID}/messages/contact.
func (h *Handler) SendContact(w http.ResponseWriter, r *http.Request) {
	instanceID := apipkg.Param(r, "instanceID")
	var req SendContactRequest
	if !apipkg.DecodeJSON(w, r, &req) {
		return
	}
	if !apipkg.RequireFields(w, r, map[string]string{"to": req.To}) {
		return
	}
	if len(req.Contacts) == 0 {
		apipkg.WriteError(w, r, apipkg.NewError(apipkg.ErrCodeValidation, "contacts must not be empty"))
		return
	}
	cards := make([]engine.ContactCard, len(req.Contacts))
	for i, c := range req.Contacts {
		cards[i] = engine.ContactCard{DisplayName: c.Name, Phone: c.Phone}
	}
	msg, err := h.svc.SendContact(r.Context(), instanceID, req.To, cards)
	if err != nil {
		apipkg.LogAndFail(w, r, err, "send contact")
		return
	}
	apipkg.WriteJSON(w, r, http.StatusCreated, fromDomain(msg))
}

// EditMessage handles POST /v1/instances/{instanceID}/messages/edit.
func (h *Handler) EditMessage(w http.ResponseWriter, r *http.Request) {
	instanceID := apipkg.Param(r, "instanceID")
	var req EditMessageRequest
	if !apipkg.DecodeJSON(w, r, &req) {
		return
	}
	if !apipkg.RequireFields(w, r, map[string]string{
		"to": req.To, "message_id": req.MessageID, "text": req.Text,
	}) {
		return
	}
	sent, err := h.svc.EditMessage(r.Context(), instanceID, req.To, req.MessageID, req.Text)
	if err != nil {
		apipkg.LogAndFail(w, r, err, "edit message")
		return
	}
	apipkg.WriteJSON(w, r, http.StatusOK, sent)
}

// SetDisappearingTimer handles POST /v1/instances/{instanceID}/messages/disappearing.
func (h *Handler) SetDisappearingTimer(w http.ResponseWriter, r *http.Request) {
	instanceID := apipkg.Param(r, "instanceID")
	var req SetDisappearingTimerRequest
	if !apipkg.DecodeJSON(w, r, &req) {
		return
	}
	if req.Chat == "" {
		apipkg.WriteError(w, r, apipkg.NewError(apipkg.ErrCodeValidation, "chat is required"))
		return
	}
	if req.Seconds < 0 {
		apipkg.WriteError(w, r, apipkg.NewError(apipkg.ErrCodeValidation, "seconds must be >= 0 (0 = off)"))
		return
	}
	if err := h.svc.SetDisappearingTimer(r.Context(), instanceID, req.Chat, req.Seconds); err != nil {
		apipkg.LogAndFail(w, r, err, "set disappearing timer")
		return
	}
	apipkg.WriteJSON(w, r, http.StatusOK, map[string]string{"status": "ok"})
}

// SendStatus handles POST /v1/instances/{instanceID}/messages/status.
func (h *Handler) SendStatus(w http.ResponseWriter, r *http.Request) {
	instanceID := apipkg.Param(r, "instanceID")

	var req SendStatusRequest
	if !apipkg.DecodeJSON(w, r, &req) {
		return
	}
	if !apipkg.RequireFields(w, r, map[string]string{"type": req.Type}) {
		return
	}

	statusType := engine.StatusType(req.Type)
	switch statusType {
	case engine.StatusTypeText, engine.StatusTypeImage, engine.StatusTypeVideo:
	default:
		apipkg.WriteError(w, r, apipkg.NewError(apipkg.ErrCodeValidation, "type must be one of: text, image, video"))
		return
	}

	if statusType == engine.StatusTypeText && req.Caption == "" {
		apipkg.WriteError(w, r, apipkg.NewError(apipkg.ErrCodeValidation, "caption required for text status"))
		return
	}

	payload := engine.StatusPayload{
		Type:     statusType,
		Caption:  req.Caption,
		FontType: req.FontType,
	}

	if req.BackgroundColor != "" {
		color, err := parseHexColor(req.BackgroundColor)
		if err != nil {
			apipkg.WriteError(w, r, apipkg.NewError(apipkg.ErrCodeValidation, "background_color must be a valid hex color, e.g. \"#FF0000\""))
			return
		}
		payload.BackgroundColor = color
	}

	if statusType == engine.StatusTypeImage || statusType == engine.StatusTypeVideo {
		if req.DataB64 == "" || req.MimeType == "" {
			apipkg.WriteError(w, r, apipkg.NewErrorWithDetails(apipkg.ErrCodeValidation, "Validation failed", map[string]string{
				"data":      "required for image/video status",
				"mime_type": "required for image/video status",
			}))
			return
		}
		data, err := base64.StdEncoding.DecodeString(req.DataB64)
		if err != nil {
			apipkg.WriteError(w, r, apipkg.NewError(apipkg.ErrCodeValidation, "data must be valid base64"))
			return
		}
		payload.Data = data
		payload.MimeType = req.MimeType
	}

	sent, err := h.svc.SendStatusUpdate(r.Context(), instanceID, payload)
	if err != nil {
		apipkg.LogAndFail(w, r, err, "send status")
		return
	}
	apipkg.WriteJSON(w, r, http.StatusCreated, sent)
}

// parseHexColor converts a hex color string ("#RRGGBB" or "RRGGBB") to an ARGB uint32
// with full opacity (alpha=0xFF).
func parseHexColor(s string) (uint32, error) {
	s = strings.TrimPrefix(s, "#")
	if len(s) != 6 {
		return 0, fmt.Errorf("expected 6 hex digits")
	}
	var r, g, b uint32
	if _, err := fmt.Sscanf(s, "%02x%02x%02x", &r, &g, &b); err != nil {
		return 0, err
	}
	return 0xFF000000 | (r << 16) | (g << 8) | b, nil
}

// SearchMessages handles GET /v1/instances/{instanceID}/messages/search?q=...&from=...&to=...
func (h *Handler) SearchMessages(w http.ResponseWriter, r *http.Request) {
	instanceID := apipkg.Param(r, "instanceID")
	q := r.URL.Query().Get("q")
	if q == "" {
		apipkg.WriteError(w, r, apipkg.NewError(apipkg.ErrCodeValidation, "q query parameter required"))
		return
	}
	pg := apipkg.ParsePagination(r)
	var from, to *time.Time
	if s := r.URL.Query().Get("from"); s != "" {
		if t, err := time.Parse(time.RFC3339, s); err == nil {
			from = &t
		}
	}
	if s := r.URL.Query().Get("to"); s != "" {
		if t, err := time.Parse(time.RFC3339, s); err == nil {
			to = &t
		}
	}
	msgs, err := h.svc.Search(r.Context(), instanceID, q, from, to, pg.Limit, pg.Offset)
	if err != nil {
		apipkg.LogAndFail(w, r, err, "search messages")
		return
	}
	out := make([]*MessageResponse, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, fromDomain(m))
	}
	apipkg.WriteJSON(w, r, http.StatusOK, out)
}
