// Package contact contains HTTP handlers for contact-related endpoints.
package contact

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	apipkg "velix/internal/api"
	"velix/internal/domain/auth"
	"velix/internal/engine"
	"velix/internal/server/middleware"
)

// Routes mounts contact routes under /v1/instances/{instanceID}/contacts.
func Routes(eng engine.Engine) http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequirePermission(auth.PermContactsManage))
	r.Post("/check", checkHandler(eng))
	r.Get("/blocklist", blocklistHandler(eng))
	r.Get("/{jid}", infoHandler(eng))
	r.Get("/{jid}/picture", pictureHandler(eng))
	r.Post("/{jid}/block", blockHandler(eng))
	r.Delete("/{jid}/block", unblockHandler(eng))
	return r
}

// checkHandler handles POST /v1/instances/{instanceID}/contacts/check.
// Body: {"phones": ["5511999990001", "5521888880001"]}
func checkHandler(eng engine.Engine) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		instanceID := apipkg.Param(r, "instanceID")

		var body struct {
			Phones []string `json:"phones"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || len(body.Phones) == 0 {
			apipkg.WriteError(w, r, apipkg.NewErrorWithDetails(apipkg.ErrCodeValidation, "Validation failed", map[string]string{
				"phones": "required, non-empty array",
			}))
			return
		}

		results, err := eng.IsOnWhatsApp(r.Context(), instanceID, body.Phones)
		if err != nil {
			apipkg.WriteError(w, r, apipkg.NewError(apipkg.ErrCodeInternal, err.Error()))
			return
		}

		apipkg.WriteJSON(w, r, http.StatusOK, results)
	}
}

// infoHandler handles GET /v1/instances/{instanceID}/contacts/{jid}.
func infoHandler(eng engine.Engine) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		instanceID := apipkg.Param(r, "instanceID")
		jid := apipkg.Param(r, "jid")

		// Chi encodes slashes; restore "@" from URL encoding.
		jid = strings.ReplaceAll(jid, "%40", "@")

		info, err := eng.GetContactInfo(r.Context(), instanceID, jid)
		if err != nil {
			apipkg.WriteError(w, r, apipkg.NewError(apipkg.ErrCodeInternal, err.Error()))
			return
		}

		apipkg.WriteJSON(w, r, http.StatusOK, info)
	}
}

// blocklistHandler handles GET /v1/instances/{instanceID}/contacts/blocklist.
func blocklistHandler(eng engine.Engine) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		instanceID := apipkg.Param(r, "instanceID")

		jids, err := eng.GetBlocklist(r.Context(), instanceID)
		if err != nil {
			apipkg.WriteError(w, r, apipkg.NewError(apipkg.ErrCodeInternal, err.Error()))
			return
		}

		apipkg.WriteJSON(w, r, http.StatusOK, map[string][]string{"jids": jids})
	}
}

// blockHandler handles POST /v1/instances/{instanceID}/contacts/{jid}/block.
func blockHandler(eng engine.Engine) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		instanceID := apipkg.Param(r, "instanceID")
		jid := strings.ReplaceAll(apipkg.Param(r, "jid"), "%40", "@")

		if err := eng.BlockContact(r.Context(), instanceID, jid); err != nil {
			apipkg.WriteError(w, r, apipkg.NewError(apipkg.ErrCodeInternal, err.Error()))
			return
		}
		apipkg.WriteJSON(w, r, http.StatusOK, map[string]string{"status": "ok"})
	}
}

// unblockHandler handles DELETE /v1/instances/{instanceID}/contacts/{jid}/block.
func unblockHandler(eng engine.Engine) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		instanceID := apipkg.Param(r, "instanceID")
		jid := strings.ReplaceAll(apipkg.Param(r, "jid"), "%40", "@")

		if err := eng.UnblockContact(r.Context(), instanceID, jid); err != nil {
			apipkg.WriteError(w, r, apipkg.NewError(apipkg.ErrCodeInternal, err.Error()))
			return
		}
		apipkg.WriteJSON(w, r, http.StatusOK, map[string]string{"status": "ok"})
	}
}

// pictureHandler handles GET /v1/instances/{instanceID}/contacts/{jid}/picture.
func pictureHandler(eng engine.Engine) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		instanceID := apipkg.Param(r, "instanceID")
		jid := apipkg.Param(r, "jid")

		// Chi encodes slashes; restore "@" from URL encoding.
		jid = strings.ReplaceAll(jid, "%40", "@")

		url, err := eng.GetProfilePicture(r.Context(), instanceID, jid)
		if err != nil {
			apipkg.WriteError(w, r, apipkg.ErrNotFound)
			return
		}
		apipkg.WriteJSON(w, r, http.StatusOK, map[string]string{"url": url})
	}
}
