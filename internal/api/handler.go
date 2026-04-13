package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog/log"

	"velix/internal/domain/auth"
)

// DecodeJSON reads and decodes the request body into dst.
// Returns false and writes a 422 error if decoding fails.
func DecodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	if err := json.NewDecoder(r.Body).Decode(dst); err != nil {
		WriteError(w, r, NewError(ErrCodeValidation, "Invalid JSON body"))
		return false
	}
	return true
}

// RequireFields checks that all values are non-empty.
// Returns false and writes a 422 error listing missing fields.
func RequireFields(w http.ResponseWriter, r *http.Request, fields map[string]string) bool {
	missing := make(map[string]string)
	for name, value := range fields {
		if value == "" {
			missing[name] = "required"
		}
	}
	if len(missing) > 0 {
		WriteError(w, r, NewErrorWithDetails(ErrCodeValidation, "Validation failed", missing))
		return false
	}
	return true
}

// WorkspaceID extracts the workspace ID from the auth context.
// Uses auth.ClaimsFromContext to avoid import cycle with middleware package.
func WorkspaceID(r *http.Request) string {
	if c := auth.ClaimsFromContext(r.Context()); c != nil {
		return c.WorkspaceID
	}
	return ""
}

// Param extracts a URL parameter by name (shorthand for chi.URLParam).
func Param(r *http.Request, name string) string {
	return chi.URLParam(r, name)
}

// Pagination holds parsed limit/offset from query string.
type Pagination struct {
	Limit  int
	Offset int
}

// ParsePagination reads "limit" and "offset" query params with safe defaults.
// Max limit is 100, default is 50.
func ParsePagination(r *http.Request) Pagination {
	limit := 50
	offset := 0
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	if v := r.URL.Query().Get("offset"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			offset = n
		}
	}
	if limit > 100 {
		limit = 100
	}
	return Pagination{Limit: limit, Offset: offset}
}

// LogAndFail logs an internal error and writes a 500 response.
// Use this for unexpected errors from service/repo layers.
func LogAndFail(w http.ResponseWriter, r *http.Request, err error, msg string) {
	// Detect rate limit errors from the engine and return 429 instead of 500.
	if strings.Contains(err.Error(), "rate limit") {
		w.Header().Set("Retry-After", "5")
		WriteError(w, r, NewError(ErrCodeRateLimit, "Message rate limit exceeded — try again in a few seconds"))
		return
	}
	log.Error().Err(err).Str("path", r.URL.Path).Msg(msg)
	WriteError(w, r, ErrInternal)
}
