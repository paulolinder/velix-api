// Package api contains shared HTTP response helpers and error definitions.
package api

import "net/http"

// ErrorCode is a machine-readable string that clients can match against.
type ErrorCode string

const (
	// Generic
	ErrCodeNotFound     ErrorCode = "NOT_FOUND"
	ErrCodeUnauthorized ErrorCode = "UNAUTHORIZED"
	ErrCodeForbidden    ErrorCode = "FORBIDDEN"
	ErrCodeValidation   ErrorCode = "VALIDATION_ERROR"
	ErrCodeConflict     ErrorCode = "CONFLICT"
	ErrCodeInternal     ErrorCode = "INTERNAL_ERROR"

	// Instance-specific
	ErrCodeInstanceNotFound  ErrorCode = "INSTANCE_NOT_FOUND"
	ErrCodeInstanceBanned    ErrorCode = "INSTANCE_BANNED"
	ErrCodeNotConnected      ErrorCode = "INSTANCE_NOT_CONNECTED"
	ErrCodeAlreadyConnected  ErrorCode = "INSTANCE_ALREADY_CONNECTED"
	ErrCodeQRExpired         ErrorCode = "QR_CODE_EXPIRED"

	// Messaging
	ErrCodeRateLimit    ErrorCode = "RATE_LIMIT_EXCEEDED"
	ErrCodeMediaTooLarge ErrorCode = "MEDIA_TOO_LARGE"
	ErrCodeInvalidPhone  ErrorCode = "INVALID_PHONE_NUMBER"
)

// APIError is the standard error envelope returned by every endpoint.
type APIError struct {
	Code    ErrorCode `json:"code"`
	Message string    `json:"message"`
	Details any       `json:"details,omitempty"`
}

func (e *APIError) Error() string {
	return string(e.Code) + ": " + e.Message
}

// NewError creates a simple APIError without details.
func NewError(code ErrorCode, message string) *APIError {
	return &APIError{Code: code, Message: message}
}

// NewErrorWithDetails creates an APIError with an optional details payload.
func NewErrorWithDetails(code ErrorCode, message string, details any) *APIError {
	return &APIError{Code: code, Message: message, Details: details}
}

// ---------------------------------------------------------------------------
// Pre-defined error values — reuse these instead of creating new ones.
// ---------------------------------------------------------------------------

var (
	ErrNotFound          = NewError(ErrCodeNotFound, "Resource not found")
	ErrUnauthorized      = NewError(ErrCodeUnauthorized, "Authentication required")
	ErrForbidden         = NewError(ErrCodeForbidden, "Insufficient permissions")
	ErrInternal          = NewError(ErrCodeInternal, "Internal server error")
	ErrInstanceNotFound  = NewError(ErrCodeInstanceNotFound, "Instance not found")
	ErrNotConnected      = NewError(ErrCodeNotConnected, "Instance is not connected to WhatsApp")
	ErrAlreadyConnected  = NewError(ErrCodeAlreadyConnected, "Instance is already connected")
	ErrRateLimit         = NewError(ErrCodeRateLimit, "Rate limit exceeded — slow down and retry")
	ErrQRExpired         = NewError(ErrCodeQRExpired, "QR code expired — request a new one")
)

// ---------------------------------------------------------------------------
// HTTP status mapping
// ---------------------------------------------------------------------------

var httpStatus = map[ErrorCode]int{
	ErrCodeNotFound:         http.StatusNotFound,
	ErrCodeUnauthorized:     http.StatusUnauthorized,
	ErrCodeForbidden:        http.StatusForbidden,
	ErrCodeValidation:       http.StatusUnprocessableEntity,
	ErrCodeConflict:         http.StatusConflict,
	ErrCodeInternal:         http.StatusInternalServerError,
	ErrCodeInstanceNotFound: http.StatusNotFound,
	ErrCodeInstanceBanned:   http.StatusForbidden,
	ErrCodeNotConnected:     http.StatusServiceUnavailable,
	ErrCodeAlreadyConnected: http.StatusConflict,
	ErrCodeQRExpired:        http.StatusGone,
	ErrCodeRateLimit:        http.StatusTooManyRequests,
	ErrCodeMediaTooLarge:    http.StatusRequestEntityTooLarge,
	ErrCodeInvalidPhone:     http.StatusUnprocessableEntity,
}

// HTTPStatus returns the correct HTTP status code for an APIError.
func HTTPStatus(err *APIError) int {
	if s, ok := httpStatus[err.Code]; ok {
		return s
	}
	return http.StatusInternalServerError
}
