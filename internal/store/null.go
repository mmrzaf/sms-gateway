package store

import (
	"time"

	"github.com/google/uuid"
)

// The helpers below turn zero values into SQL NULL, for optional query
// filters written as "($n::type IS NULL OR column = $n)".

// NullString returns nil for "" and a pointer to s otherwise.
func NullString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// NullID returns nil for uuid.Nil and a pointer to id otherwise.
func NullID(id uuid.UUID) *uuid.UUID {
	if id == uuid.Nil {
		return nil
	}
	return &id
}

// NullLowerBound returns nil for the zero time and a pointer to
// LowerBound(t) otherwise.
func NullLowerBound(t time.Time) *uuid.UUID {
	if t.IsZero() {
		return nil
	}
	id := LowerBound(t)
	return &id
}
