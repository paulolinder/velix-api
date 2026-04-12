package instance

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	qrcode "github.com/skip2/go-qrcode"

	apipkg "velix/internal/api"
	"velix/internal/domain/auth"
	"velix/internal/domain/instance"
)

// Handler holds the service dependency for all instance HTTP handlers.
type Handler struct {
	svc    *instance.Service
	keySvc *auth.Service
}

// NewHandler creates a new instance HTTP handler.
func NewHandler(svc *instance.Service, keySvc ...*auth.Service) *Handler {
	h := &Handler{svc: svc}
	if len(keySvc) > 0 {
		h.keySvc = keySvc[0]
	}
	return h
}

// Routes mounts all instance routes on r.
func Routes(svc *instance.Service) http.Handler {
	h := NewHandler(svc)
	r := chi.NewRouter()

	r.Post("/", h.Create)
	r.Get("/", h.List)
	r.Get("/{instanceID}", h.Get)
	r.Delete("/{instanceID}", h.Delete)

	r.Post("/{instanceID}/connect", h.Connect)
	r.Post("/{instanceID}/disconnect", h.Disconnect)
	r.Post("/{instanceID}/logout", h.Logout)
	r.Get("/{instanceID}/status", h.GetStatus)

	// QR auth flows
	r.Get("/{instanceID}/qr", h.GetQR)
	r.Post("/{instanceID}/pair-code", h.PairCode)

	return r
}

// SettingsRoutes mounts settings endpoints for a specific instance.
// Mounted under /v1/instances/{instanceID}/settings.
func SettingsRoutes(svc *instance.Service) http.Handler {
	h := NewHandler(svc)
	r := chi.NewRouter()
	r.Get("/", h.GetSettings)
	r.Patch("/", h.UpdateSettings)
	return r
}

// handleErr maps domain errors to HTTP responses. Returns true if err was handled.
func handleErr(w http.ResponseWriter, r *http.Request, err error, context string) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, instance.ErrNotFound) {
		apipkg.WriteError(w, r, apipkg.ErrInstanceNotFound)
		return true
	}
	apipkg.LogAndFail(w, r, err, context)
	return true
}

// ---------------------------------------------------------------------------
// Handlers
// ---------------------------------------------------------------------------

// Create handles POST /v1/instances.
func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	var req CreateRequest
	if !apipkg.DecodeJSON(w, r, &req) {
		return
	}
	if !apipkg.RequireFields(w, r, map[string]string{"name": req.Name}) {
		return
	}

	wsID := apipkg.WorkspaceID(r)
	claims := auth.ClaimsFromContext(r.Context())
	inst, err := h.svc.Create(r.Context(), wsID, req.Name, req.ProxyURL)
	if handleErr(w, r, err, "create instance") {
		return
	}

	// Auto-generate a permanent, instance-scoped API key.
	var rawKey string
	if h.keySvc != nil && claims != nil {
		_, rawKey, _ = h.keySvc.CreateAPIKey(
			r.Context(),
			wsID,
			claims.UserID,
			"Instância: "+inst.Name,
			nil, // never expires
			[]string{"instance:" + inst.ID},
		)
	}

	apipkg.WriteJSON(w, r, http.StatusCreated, &CreateResponse{
		Instance: fromDomain(inst),
		APIKey:   rawKey,
	})
}

// List handles GET /v1/instances?limit=50&offset=0.
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	pg := apipkg.ParsePagination(r)
	instances, err := h.svc.List(r.Context(), apipkg.WorkspaceID(r), pg.Limit, pg.Offset)
	if handleErr(w, r, err, "list instances") {
		return
	}

	out := make([]*Response, 0, len(instances))
	for _, inst := range instances {
		out = append(out, fromDomain(inst))
	}
	apipkg.WriteJSON(w, r, http.StatusOK, out)
}

// Get handles GET /v1/instances/{id}.
func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	inst, err := h.svc.Get(r.Context(), apipkg.WorkspaceID(r), apipkg.Param(r, "instanceID"))
	if handleErr(w, r, err, "get instance") {
		return
	}
	apipkg.WriteJSON(w, r, http.StatusOK, fromDomain(inst))
}

// Delete handles DELETE /v1/instances/{id}.
func (h *Handler) Delete(w http.ResponseWriter, r *http.Request) {
	err := h.svc.Delete(r.Context(), apipkg.WorkspaceID(r), apipkg.Param(r, "instanceID"))
	if handleErr(w, r, err, "delete instance") {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// Connect handles POST /v1/instances/{id}/connect.
func (h *Handler) Connect(w http.ResponseWriter, r *http.Request) {
	err := h.svc.Connect(r.Context(), apipkg.WorkspaceID(r), apipkg.Param(r, "instanceID"))
	if handleErr(w, r, err, "connect instance") {
		return
	}
	apipkg.WriteJSON(w, r, http.StatusOK, map[string]string{"status": "connecting"})
}

// Disconnect handles POST /v1/instances/{id}/disconnect.
func (h *Handler) Disconnect(w http.ResponseWriter, r *http.Request) {
	err := h.svc.Disconnect(r.Context(), apipkg.WorkspaceID(r), apipkg.Param(r, "instanceID"))
	if handleErr(w, r, err, "disconnect instance") {
		return
	}
	apipkg.WriteJSON(w, r, http.StatusOK, map[string]string{"status": "disconnected"})
}

// Logout handles POST /v1/instances/{id}/logout.
func (h *Handler) Logout(w http.ResponseWriter, r *http.Request) {
	err := h.svc.Logout(r.Context(), apipkg.WorkspaceID(r), apipkg.Param(r, "instanceID"))
	if handleErr(w, r, err, "logout instance") {
		return
	}
	apipkg.WriteJSON(w, r, http.StatusOK, map[string]string{"status": "logged_out"})
}

// GetStatus handles GET /v1/instances/{id}/status.
func (h *Handler) GetStatus(w http.ResponseWriter, r *http.Request) {
	wsID := apipkg.WorkspaceID(r)
	id := apipkg.Param(r, "instanceID")

	status, err := h.svc.GetStatus(wsID, id)
	if err != nil {
		apipkg.WriteError(w, r, apipkg.ErrInstanceNotFound)
		return
	}

	resp := &StatusResponse{
		InstanceID: id,
		Status:     string(status),
	}

	// Enrich with DB fields (phone, platform, last_seen).
	if inst, err := h.svc.Get(r.Context(), wsID, id); err == nil {
		resp.Phone = inst.PhoneNumber
		resp.Platform = inst.Platform
		resp.LastSeen = inst.LastConnectedAt
	}

	apipkg.WriteJSON(w, r, http.StatusOK, resp)
}

// GetQR handles GET /v1/instances/{id}/qr as a Server-Sent Events stream.
func (h *Handler) GetQR(w http.ResponseWriter, r *http.Request) {
	wsID := apipkg.WorkspaceID(r)
	id := apipkg.Param(r, "instanceID")

	flusher, ok := w.(http.Flusher)
	if !ok {
		apipkg.WriteError(w, r, apipkg.NewError(apipkg.ErrCodeInternal, "SSE not supported"))
		return
	}

	qrChan, err := h.svc.StartQRFlow(r.Context(), wsID, id)
	if handleErr(w, r, err, "start QR flow") {
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	_, _ = fmt.Fprintf(w, ": connected\n\n")
	flusher.Flush()

	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)

	for {
		select {
		case <-r.Context().Done():
			return
		case qrEvt, open := <-qrChan:
			if !open {
				_, _ = fmt.Fprintf(w, "event: paired\ndata: {}\n\n")
				flusher.Flush()
				return
			}
			if qrEvt.Error != nil {
				errData, _ := json.Marshal(map[string]string{"message": qrEvt.Error.Error()})
				_, _ = fmt.Fprintf(w, "event: error\ndata: %s\n\n", errData)
				flusher.Flush()
				return
			}
			payload := map[string]any{
				"code":            qrEvt.Code,
				"timeout_seconds": int(qrEvt.Timeout / time.Second),
			}
			// Generate QR code image server-side so the frontend needs no JS lib.
			if png, err := qrcode.Encode(qrEvt.Code, qrcode.Medium, 256); err == nil {
				payload["image"] = "data:image/png;base64," + base64.StdEncoding.EncodeToString(png)
			}
			_, _ = fmt.Fprintf(w, "event: qr\ndata: ")
			_ = enc.Encode(payload)
			_, _ = fmt.Fprintf(w, "\n")
			flusher.Flush()
		}
	}
}

// PairCode handles POST /v1/instances/{id}/pair-code.
func (h *Handler) PairCode(w http.ResponseWriter, r *http.Request) {
	var req PairCodeRequest
	if !apipkg.DecodeJSON(w, r, &req) {
		return
	}
	if !apipkg.RequireFields(w, r, map[string]string{"phone": req.Phone}) {
		return
	}

	code, err := h.svc.RequestPairCode(r.Context(), apipkg.WorkspaceID(r), apipkg.Param(r, "instanceID"), req.Phone)
	if handleErr(w, r, err, "request pair code") {
		return
	}
	apipkg.WriteJSON(w, r, http.StatusOK, &PairCodeResponse{Code: code})
}

// GetSettings handles GET /v1/instances/{instanceID}/settings.
func (h *Handler) GetSettings(w http.ResponseWriter, r *http.Request) {
	settings, err := h.svc.GetSettings(r.Context(), apipkg.WorkspaceID(r), apipkg.Param(r, "instanceID"))
	if handleErr(w, r, err, "get settings") {
		return
	}
	apipkg.WriteJSON(w, r, http.StatusOK, settingsFromDomain(settings))
}

// UpdateSettings handles PATCH /v1/instances/{instanceID}/settings.
func (h *Handler) UpdateSettings(w http.ResponseWriter, r *http.Request) {
	wsID := apipkg.WorkspaceID(r)
	id := apipkg.Param(r, "instanceID")

	current, err := h.svc.GetSettings(r.Context(), wsID, id)
	if handleErr(w, r, err, "load settings for patch") {
		return
	}

	var req UpdateSettingsRequest
	if !apipkg.DecodeJSON(w, r, &req) {
		return
	}

	// Merge: only overwrite fields that were actually sent.
	if req.RejectCall != nil {
		current.RejectCall = *req.RejectCall
	}
	if req.ReadMessages != nil {
		current.ReadMessages = *req.ReadMessages
	}
	if req.AlwaysOnline != nil {
		current.AlwaysOnline = *req.AlwaysOnline
	}
	if req.IgnoreGroups != nil {
		current.IgnoreGroups = *req.IgnoreGroups
	}
	if req.SyncFullHistory != nil {
		current.SyncFullHistory = *req.SyncFullHistory
	}
	if req.WebhookURL != nil {
		current.WebhookURL = *req.WebhookURL
	}
	if req.WebhookEvents != nil {
		current.WebhookEvents = *req.WebhookEvents
	}
	if req.HumanPauseDuration != nil {
		current.HumanPauseDuration = *req.HumanPauseDuration
	}
	if req.ChatwootEnabled != nil {
		current.ChatwootEnabled = *req.ChatwootEnabled
	}
	if req.ChatwootURL != nil {
		current.ChatwootURL = *req.ChatwootURL
	}
	if req.ChatwootToken != nil {
		current.ChatwootToken = *req.ChatwootToken
	}
	if req.ChatwootAccountID != nil {
		current.ChatwootAccountID = *req.ChatwootAccountID
	}
	if req.ChatwootInboxID != nil {
		current.ChatwootInboxID = *req.ChatwootInboxID
	}
	if req.ChatwootSignMsgs != nil {
		current.ChatwootSignMsgs = *req.ChatwootSignMsgs
	}
	if req.ChatwootReopenConv != nil {
		current.ChatwootReopenConv = *req.ChatwootReopenConv
	}
	if req.ChatwootConvPending != nil {
		current.ChatwootConvPending = *req.ChatwootConvPending
	}

	updated, err := h.svc.UpdateSettings(r.Context(), wsID, id, current)
	if handleErr(w, r, err, "update settings") {
		return
	}
	apipkg.WriteJSON(w, r, http.StatusOK, settingsFromDomain(updated))
}
