package dispatch

import (
	"errors"
	"fmt"
	"net/http"
)

// SendError describes a failed provider request.
type SendError struct {
	// Status is the HTTP status, or 0 if no response arrived.
	Status int
	// Permanent means retrying cannot succeed: the provider rejected the message.
	Permanent bool
	// ProviderFault means the failure counts toward opening the circuit.
	ProviderFault bool
	Err           error
}

func (e *SendError) Error() string {
	if e.Status == 0 {
		return e.Err.Error()
	}
	return fmt.Sprintf("HTTP %d %s", e.Status, http.StatusText(e.Status))
}

func (e *SendError) Unwrap() error { return e.Err }

// classifyStatus turns a non-200 response into a SendError.
//
//	429           retryable, not the provider's fault (throttling)
//	5xx           retryable, counts toward the circuit
//	other 4xx     permanent rejection
func classifyStatus(status int) *SendError {
	switch {
	case status == http.StatusTooManyRequests:
		return &SendError{Status: status}
	case status >= 500:
		return &SendError{Status: status, ProviderFault: true}
	default:
		return &SendError{Status: status, Permanent: true}
	}
}

// classifyTransportError turns a failed round trip (timeout, refused or reset
// connection) into a retryable SendError that counts toward the circuit.
func classifyTransportError(err error) *SendError {
	return &SendError{ProviderFault: true, Err: err}
}

// asSendError extracts a SendError, treating anything else as a transport failure.
func asSendError(err error) *SendError {
	var se *SendError
	if errors.As(err, &se) {
		return se
	}
	return classifyTransportError(err)
}
