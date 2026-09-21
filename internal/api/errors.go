package api

import (
	"errors"
	"net/http"

	"github.com/mmrzaf/sms-gatway/internal/auth"
	"github.com/mmrzaf/sms-gatway/internal/billing"
	"github.com/mmrzaf/sms-gatway/internal/httpx"
	"github.com/mmrzaf/sms-gatway/internal/message"
	"github.com/mmrzaf/sms-gatway/internal/store"
)

// toHTTPError maps domain errors to their HTTP representation. Errors it
// does not recognize pass through and become 500 internal_error.
func toHTTPError(err error) error {
	var (
		validation *message.ValidationError
		conflict   *message.ConflictError
	)
	switch {
	case errors.As(err, &validation):
		return httpx.ValidationError(fieldErrors(validation.Fields)...)
	case errors.As(err, &conflict):
		return &httpx.Error{
			Status:  http.StatusConflict,
			Code:    httpx.CodeIdempotencyConflict,
			Message: "The client_ref was already used for a different request.",
			Details: fieldErrors(conflict.Fields),
		}
	case errors.Is(err, billing.ErrChargeConflict):
		return &httpx.Error{
			Status:  http.StatusConflict,
			Code:    httpx.CodeIdempotencyConflict,
			Message: "The client_ref was already used for a different request.",
			Details: []httpx.FieldError{{Field: "client_ref", Code: message.CodeConflict,
				Message: "was already used for a charge with a different amount"}},
		}
	case errors.Is(err, billing.ErrInsufficientCredits):
		return httpx.NewError(http.StatusPaymentRequired, httpx.CodeInsufficientCredits,
			"The balance does not cover the cost of the request.")
	case errors.Is(err, message.ErrNotFound):
		return httpx.NewError(http.StatusNotFound, httpx.CodeNotFound, "The message does not exist.")
	case errors.Is(err, auth.ErrUnauthorized), errors.Is(err, billing.ErrCustomerNotFound):
		return httpx.NewError(http.StatusUnauthorized, httpx.CodeUnauthorized,
			"A valid API key is required: Authorization: Bearer sk_...")
	case store.IsUnavailable(err):
		return httpx.NewError(http.StatusServiceUnavailable, httpx.CodeUnavailable,
			"The service is temporarily unavailable. Retry with the same client_ref.")
	}
	return err
}

func fieldErrors(fields []message.FieldError) []httpx.FieldError {
	out := make([]httpx.FieldError, len(fields))
	for i, f := range fields {
		out[i] = httpx.FieldError{Field: f.Field, Code: f.Code, Message: f.Message}
	}
	return out
}

// invalid returns a validation error for a single field.
func invalid(field, code, msg string) error {
	return &message.ValidationError{Fields: []message.FieldError{{Field: field, Code: code, Message: msg}}}
}
