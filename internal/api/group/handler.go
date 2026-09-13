// Package group contains HTTP handlers for WhatsApp group management.
package group

import (
	"encoding/base64"
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"

	apipkg "velix/internal/api"
	"velix/internal/domain/auth"
	"velix/internal/engine"
	"velix/internal/server/middleware"
)

// Routes mounts group routes under /v1/instances/{instanceID}/groups.
func Routes(eng engine.Engine) http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequirePermission(auth.PermContactsManage))

	r.Get("/", listHandler(eng))
	r.Post("/", createHandler(eng))
	r.Post("/join", joinHandler(eng))
	r.Get("/{groupID}", infoHandler(eng))
	r.Post("/{groupID}/participants", updateParticipantsHandler(eng))
	r.Delete("/{groupID}/leave", leaveHandler(eng))
	r.Get("/{groupID}/invite-link", inviteLinkHandler(eng))
	r.Patch("/{groupID}", updateGroupHandler(eng))
	r.Put("/{groupID}/photo", groupPhotoHandler(eng))

	return r
}

// listHandler handles GET /v1/instances/{instanceID}/groups.
func listHandler(eng engine.Engine) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		instanceID := apipkg.Param(r, "instanceID")

		groups, err := eng.GetJoinedGroups(r.Context(), instanceID)
		if err != nil {
			apipkg.WriteError(w, r, apipkg.NewError(apipkg.ErrCodeInternal, err.Error()))
			return
		}

		apipkg.WriteJSON(w, r, http.StatusOK, groups)
	}
}

// createHandler handles POST /v1/instances/{instanceID}/groups.
// Body: {"name": "Group Name", "participants": ["5511999990001", "5521888880002"]}
func createHandler(eng engine.Engine) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		instanceID := apipkg.Param(r, "instanceID")

		var req engine.CreateGroupRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			apipkg.WriteError(w, r, apipkg.NewError(apipkg.ErrCodeValidation, "Invalid JSON body"))
			return
		}
		if req.Name == "" {
			apipkg.WriteError(w, r, apipkg.NewErrorWithDetails(apipkg.ErrCodeValidation, "Validation failed", map[string]string{
				"name": "required",
			}))
			return
		}

		group, err := eng.CreateGroup(r.Context(), instanceID, req)
		if err != nil {
			apipkg.WriteError(w, r, apipkg.NewError(apipkg.ErrCodeInternal, err.Error()))
			return
		}

		apipkg.WriteJSON(w, r, http.StatusCreated, group)
	}
}

// infoHandler handles GET /v1/instances/{instanceID}/groups/{groupID}.
func infoHandler(eng engine.Engine) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		instanceID := apipkg.Param(r, "instanceID")
		groupID := apipkg.Param(r, "groupID")

		info, err := eng.GetGroupInfo(r.Context(), instanceID, groupID)
		if err != nil {
			apipkg.WriteError(w, r, apipkg.NewError(apipkg.ErrCodeInternal, err.Error()))
			return
		}

		apipkg.WriteJSON(w, r, http.StatusOK, info)
	}
}

// updateParticipantsHandler handles POST /v1/instances/{instanceID}/groups/{groupID}/participants.
// Body: {"action": "add|remove|promote|demote", "participants": ["5511999990001"]}
func updateParticipantsHandler(eng engine.Engine) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		instanceID := apipkg.Param(r, "instanceID")
		groupID := apipkg.Param(r, "groupID")

		var body struct {
			Action       string   `json:"action"`
			Participants []string `json:"participants"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			apipkg.WriteError(w, r, apipkg.NewError(apipkg.ErrCodeValidation, "Invalid JSON body"))
			return
		}
		if body.Action == "" || len(body.Participants) == 0 {
			apipkg.WriteError(w, r, apipkg.NewErrorWithDetails(apipkg.ErrCodeValidation, "Validation failed", map[string]string{
				"action":       "required: add|remove|promote|demote",
				"participants": "required, non-empty array",
			}))
			return
		}

		if err := eng.UpdateGroupParticipants(r.Context(), instanceID, groupID, body.Participants, body.Action); err != nil {
			apipkg.WriteError(w, r, apipkg.NewError(apipkg.ErrCodeInternal, err.Error()))
			return
		}

		apipkg.WriteJSON(w, r, http.StatusOK, map[string]string{"status": "ok"})
	}
}

// leaveHandler handles DELETE /v1/instances/{instanceID}/groups/{groupID}/leave.
func leaveHandler(eng engine.Engine) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		instanceID := apipkg.Param(r, "instanceID")
		groupID := apipkg.Param(r, "groupID")

		if err := eng.LeaveGroup(r.Context(), instanceID, groupID); err != nil {
			apipkg.WriteError(w, r, apipkg.NewError(apipkg.ErrCodeInternal, err.Error()))
			return
		}

		w.WriteHeader(http.StatusNoContent)
	}
}

// inviteLinkHandler handles GET /v1/instances/{instanceID}/groups/{groupID}/invite-link.
// Query param: ?reset=true to revoke and regenerate the link.
func inviteLinkHandler(eng engine.Engine) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		instanceID := apipkg.Param(r, "instanceID")
		groupID := apipkg.Param(r, "groupID")
		reset := r.URL.Query().Get("reset") == "true"

		link, err := eng.GetGroupInviteLink(r.Context(), instanceID, groupID, reset)
		if err != nil {
			apipkg.WriteError(w, r, apipkg.NewError(apipkg.ErrCodeInternal, err.Error()))
			return
		}
		apipkg.WriteJSON(w, r, http.StatusOK, map[string]string{"link": link})
	}
}

// joinHandler handles POST /v1/instances/{instanceID}/groups/join.
// Body: {"link": "https://chat.whatsapp.com/CODE"}
func joinHandler(eng engine.Engine) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		instanceID := apipkg.Param(r, "instanceID")

		var body struct {
			Link string `json:"link"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Link == "" {
			apipkg.WriteError(w, r, apipkg.NewErrorWithDetails(apipkg.ErrCodeValidation, "Validation failed", map[string]string{
				"link": "required",
			}))
			return
		}

		groupJID, err := eng.JoinGroupWithLink(r.Context(), instanceID, body.Link)
		if err != nil {
			apipkg.WriteError(w, r, apipkg.NewError(apipkg.ErrCodeInternal, err.Error()))
			return
		}
		apipkg.WriteJSON(w, r, http.StatusOK, map[string]string{"group_jid": groupJID})
	}
}

// updateGroupHandler handles PATCH /v1/instances/{instanceID}/groups/{groupID}.
// Body: {"name": "...", "description": "..."} — send only the fields you want to change.
func updateGroupHandler(eng engine.Engine) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		instanceID := apipkg.Param(r, "instanceID")
		groupID := apipkg.Param(r, "groupID")

		var body struct {
			Name        *string `json:"name"`
			Description *string `json:"description"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			apipkg.WriteError(w, r, apipkg.NewError(apipkg.ErrCodeValidation, "Invalid JSON body"))
			return
		}
		if body.Name == nil && body.Description == nil {
			apipkg.WriteError(w, r, apipkg.NewError(apipkg.ErrCodeValidation, "at least one of name or description is required"))
			return
		}

		if body.Name != nil {
			if err := eng.SetGroupName(r.Context(), instanceID, groupID, *body.Name); err != nil {
				apipkg.WriteError(w, r, apipkg.NewError(apipkg.ErrCodeInternal, err.Error()))
				return
			}
		}
		if body.Description != nil {
			if err := eng.SetGroupDescription(r.Context(), instanceID, groupID, *body.Description); err != nil {
				apipkg.WriteError(w, r, apipkg.NewError(apipkg.ErrCodeInternal, err.Error()))
				return
			}
		}

		apipkg.WriteJSON(w, r, http.StatusOK, map[string]string{"status": "ok"})
	}
}

// groupPhotoHandler handles PUT /v1/instances/{instanceID}/groups/{groupID}/photo.
// Body: {"data": "<base64>"}
func groupPhotoHandler(eng engine.Engine) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		instanceID := apipkg.Param(r, "instanceID")
		groupID := apipkg.Param(r, "groupID")

		var body struct {
			DataB64 string `json:"data"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.DataB64 == "" {
			apipkg.WriteError(w, r, apipkg.NewErrorWithDetails(apipkg.ErrCodeValidation, "Validation failed", map[string]string{
				"data": "required (base64-encoded image)",
			}))
			return
		}

		photo, err := base64.StdEncoding.DecodeString(body.DataB64)
		if err != nil {
			apipkg.WriteError(w, r, apipkg.NewError(apipkg.ErrCodeValidation, "data must be valid base64"))
			return
		}

		if err := eng.SetGroupPhoto(r.Context(), instanceID, groupID, photo); err != nil {
			apipkg.WriteError(w, r, apipkg.NewError(apipkg.ErrCodeInternal, err.Error()))
			return
		}
		apipkg.WriteJSON(w, r, http.StatusOK, map[string]string{"status": "ok"})
	}
}
