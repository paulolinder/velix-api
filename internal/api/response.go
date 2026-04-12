package api

import (
	"encoding/json"
	"net/http"
	"time"

	chimw "github.com/go-chi/chi/v5/middleware"
)

// Response is the unified envelope for every API response.
type Response struct {
	Success bool      `json:"success"`
	Data    any       `json:"data,omitempty"`
	Error   *APIError `json:"error,omitempty"`
	Meta    Meta      `json:"meta"`
}

// Meta carries request-level metadata returned on every response.
type Meta struct {
	RequestID string    `json:"request_id,omitempty"`
	Timestamp time.Time `json:"timestamp"`
}

// WriteJSON writes a successful JSON response.
func WriteJSON(w http.ResponseWriter, r *http.Request, status int, data any) {
	resp := Response{
		Success: true,
		Data:    data,
		Meta: Meta{
			RequestID: chimw.GetReqID(r.Context()),
			Timestamp: time.Now().UTC(),
		},
	}
	writeResponse(w, status, resp)
}

// WriteError writes an error JSON response with the correct HTTP status code.
func WriteError(w http.ResponseWriter, r *http.Request, err *APIError) {
	resp := Response{
		Success: false,
		Error:   err,
		Meta: Meta{
			RequestID: chimw.GetReqID(r.Context()),
			Timestamp: time.Now().UTC(),
		},
	}
	writeResponse(w, HTTPStatus(err), resp)
}

// writeResponse serialises v as JSON and sets the Content-Type header.
func writeResponse(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
}
