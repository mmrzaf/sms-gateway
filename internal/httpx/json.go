package httpx

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strings"
)

// MaxBodyBytes is the largest request body accepted by DecodeJSON.
const MaxBodyBytes = 1 << 20

// DecodeJSON decodes a single JSON object from the request body into dst.
// It requires a JSON content type, limits the body to MaxBodyBytes, and
// rejects unknown fields and trailing data. Failures are returned as *Error.
func DecodeJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		return NewError(http.StatusUnsupportedMediaType, CodeUnsupportedMediaType,
			"The request body must be JSON with Content-Type: application/json.")
	}

	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, MaxBodyBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return decodeError(err)
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return NewError(http.StatusBadRequest, CodeInvalidJSON, "The request body must contain a single JSON object.")
	}
	return nil
}

func decodeError(err error) *Error {
	var (
		syntaxErr *json.SyntaxError
		typeErr   *json.UnmarshalTypeError
		sizeErr   *http.MaxBytesError
	)
	switch {
	case errors.As(err, &sizeErr):
		return NewError(http.StatusRequestEntityTooLarge, CodePayloadTooLarge,
			fmt.Sprintf("The request body must not exceed %d bytes.", MaxBodyBytes))
	case errors.As(err, &syntaxErr):
		return NewError(http.StatusBadRequest, CodeInvalidJSON,
			fmt.Sprintf("The request body is not valid JSON (at byte %d).", syntaxErr.Offset))
	case errors.As(err, &typeErr):
		return NewError(http.StatusBadRequest, CodeInvalidJSON,
			fmt.Sprintf("Field %q must be of type %s.", typeErr.Field, typeErr.Type))
	case errors.Is(err, io.EOF):
		return NewError(http.StatusBadRequest, CodeInvalidJSON, "The request body is empty.")
	case errors.Is(err, io.ErrUnexpectedEOF):
		return NewError(http.StatusBadRequest, CodeInvalidJSON, "The request body is not valid JSON.")
	case strings.HasPrefix(err.Error(), "json: unknown field "):
		field := strings.TrimPrefix(err.Error(), "json: unknown field ")
		return NewError(http.StatusBadRequest, CodeInvalidJSON, fmt.Sprintf("Unknown field %s.", field))
	default:
		return NewError(http.StatusBadRequest, CodeInvalidJSON, "The request body is not valid JSON.")
	}
}

// WriteJSON writes v as a JSON response with the given status.
func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	// The status line is already sent; an encoding error can only be a
	// broken connection, which the client observes directly.
	_ = json.NewEncoder(w).Encode(v)
}
