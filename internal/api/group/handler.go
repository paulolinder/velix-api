// Package group contains HTTP handlers for WhatsApp group management.
package group

import (
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
	r.Get("/{groupID}", infoHandler(eng))
	r.Post("/{groupID}/participants", updateParticipantsHandler(eng))
	r.Delete("/{groupID}/leave", leaveHandler(eng))

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
