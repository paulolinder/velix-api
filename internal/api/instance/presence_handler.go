package instance

import (
	"encoding/base64"
	"net/http"

	apipkg "velix/internal/api"
	"velix/internal/domain/instance"
)

// PresenceRequest is the body for POST /v1/instances/{instanceID}/presence.
type PresenceRequest struct {
	To   string `json:"to,omitempty"` // JID for chat presence (typing/recording/paused); blank for global
	Type string `json:"type"`         // typing | recording | paused | available | unavailable
}

// ProfileUpdateRequest is the body for PATCH /v1/instances/{instanceID}/profile.
type ProfileUpdateRequest struct {
	Name     string `json:"name,omitempty"`
	PhotoB64 string `json:"photo_b64,omitempty"` // base64-encoded image bytes
}

// PresenceHandler returns an http.HandlerFunc for POST /presence.
func PresenceHandler(svc *instance.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		instanceID := apipkg.Param(r, "instanceID")
		wsID := apipkg.WorkspaceID(r)

		var req PresenceRequest
		if !apipkg.DecodeJSON(w, r, &req) {
			return
		}

		validTypes := map[string]bool{
			"typing": true, "recording": true, "paused": true,
			"available": true, "unavailable": true,
		}
		if !validTypes[req.Type] {
			apipkg.WriteError(w, r, apipkg.NewError(apipkg.ErrCodeValidation, "type must be one of: typing, recording, paused, available, unavailable"))
			return
		}

		if err := svc.SetPresence(r.Context(), wsID, instanceID, req.To, req.Type); err != nil {
			apipkg.LogAndFail(w, r, err, "set presence")
			return
		}
		apipkg.WriteJSON(w, r, http.StatusOK, map[string]string{"status": "ok"})
	}
}

// StatusMessageRequest is the body for PATCH /v1/instances/{instanceID}/status-message.
type StatusMessageRequest struct {
	Status string `json:"status"`
}

// StatusMessageHandler returns an http.HandlerFunc for PATCH /status-message.
func StatusMessageHandler(svc *instance.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		instanceID := apipkg.Param(r, "instanceID")
		wsID := apipkg.WorkspaceID(r)

		var req StatusMessageRequest
		if !apipkg.DecodeJSON(w, r, &req) {
			return
		}

		if err := svc.SetStatusMessage(r.Context(), wsID, instanceID, req.Status); err != nil {
			apipkg.LogAndFail(w, r, err, "set status message")
			return
		}
		apipkg.WriteJSON(w, r, http.StatusOK, map[string]string{"status": "ok"})
	}
}

// ProfileHandler returns an http.HandlerFunc for PATCH /profile.
func ProfileHandler(svc *instance.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		instanceID := apipkg.Param(r, "instanceID")
		wsID := apipkg.WorkspaceID(r)

		var req ProfileUpdateRequest
		if !apipkg.DecodeJSON(w, r, &req) {
			return
		}

		var photoData []byte
		if req.PhotoB64 != "" {
			var err error
			photoData, err = base64.StdEncoding.DecodeString(req.PhotoB64)
			if err != nil {
				apipkg.WriteError(w, r, apipkg.NewError(apipkg.ErrCodeValidation, "photo_b64 must be valid base64"))
				return
			}
		}

		if err := svc.UpdateProfile(r.Context(), wsID, instanceID, req.Name, photoData); err != nil {
			apipkg.LogAndFail(w, r, err, "update profile")
			return
		}
		apipkg.WriteJSON(w, r, http.StatusOK, map[string]string{"status": "ok"})
	}
}
