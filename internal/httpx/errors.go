// Package httpx holds HTTP plumbing shared by every server in the system:
// JSON decoding and encoding, the error envelope, middleware, health
// endpoints, and server lifecycle.
package httpx

import (
	"errors"
	"net/http"
)

// Error codes returned in the "code" field of the error envelope.
const (
	CodeInvalidJSON          = "invalid_json"
	CodeUnauthorized         = "unauthorized"
	CodeInsufficientCredits  = "insufficient_credits"
	CodeNotFound             = "not_found"
	CodeMethodNotAllowed     = "method_not_allowed"
	CodeIdempotencyConflict  = "idempotency_conflict"
	CodePayloadTooLarge      = "payload_too_large"
	CodeUnsupportedMediaType = "unsupported_media_type"
	CodeValidationFailed     = "validation_failed"
	CodeRateLimited          = "rate_limited"
	CodeInternal             = "internal_error"
	CodeUnavailable          = "unavailable"
)

// FieldError describes one invalid field in a validation or conflict error.
type FieldError struct {
	Field   string `json:"field"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Error is an error with a defined HTTP representation.
type Error struct {
	Status  int
	Code    string
	Message string
	Details []FieldError
}

func (e *Error) Error() string {
	return e.Code + ": " + e.Message
}

// NewError returns an Error without details.
func NewError(status int, code, message string) *Error {
	return &Error{Status: status, Code: code, Message: message}
}

// ValidationError returns a 422 error listing the invalid fields.
func ValidationError(details ...FieldError) *Error {
	return &Error{
		Status:  http.StatusUnprocessableEntity,
		Code:    CodeValidationFailed,
		Message: "The request contains invalid fields.",
		Details: details,
	}
}

type errorBody struct {
	Error errorPayload `json:"error"`
}

type errorPayload struct {
	Code      string       `json:"code"`
	Message   string       `json:"message"`
	Details   []FieldError `json:"details"`
	RequestID string       `json:"request_id"`
}

// WriteError writes err as the standard error envelope. Errors that are not
// an *Error are logged and reported as 500 internal_error, so internal
// details never reach the client.
func WriteError(w http.ResponseWriter, r *http.Request, err error) {
	var e *Error
	if !errors.As(err, &e) {
		Logger(r.Context()).Error("request failed", "error", err)
		e = NewError(http.StatusInternalServerError, CodeInternal, "An unexpected error occurred.")
	}
	WriteJSON(w, e.Status, errorBody{Error: errorPayload{
		Code:      e.Code,
		Message:   e.Message,
		Details:   e.Details,
		RequestID: RequestID(r.Context()),
	}})
}

// NotFound is a handler that answers every request with 404 not_found.
func NotFound(w http.ResponseWriter, r *http.Request) {
	WriteError(w, r, NewError(http.StatusNotFound, CodeNotFound, "The requested resource does not exist."))
}
