package store

import (
	"encoding/base64"
	"errors"

	"github.com/google/uuid"
)

// ErrInvalidCursor is returned when a pagination cursor cannot be decoded.
var ErrInvalidCursor = errors.New("invalid cursor")

// EncodeCursor turns the last ID of a page into an opaque cursor.
func EncodeCursor(id uuid.UUID) string {
	return base64.RawURLEncoding.EncodeToString(id[:])
}

// DecodeCursor reverses EncodeCursor. An empty cursor decodes to uuid.Nil,
// which means "first page".
func DecodeCursor(s string) (uuid.UUID, error) {
	if s == "" {
		return uuid.Nil, nil
	}
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil || len(b) != 16 {
		return uuid.Nil, ErrInvalidCursor
	}
	return uuid.UUID(b), nil
}
