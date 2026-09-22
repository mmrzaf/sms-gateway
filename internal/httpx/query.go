package httpx

import (
	"fmt"
	"net/url"
	"strconv"
	"time"

	"github.com/google/uuid"

	"github.com/mmrzaf/sms-gatway/internal/store"
)

// Field error codes used in validation details.
const (
	FieldInvalidFormat = "invalid_format"
	FieldOutOfRange    = "out_of_range"
)

// Query reads URL query parameters and collects every problem, so a request
// with several bad parameters gets one response listing all of them.
type Query struct {
	values   url.Values
	problems []FieldError
}

// NewQuery wraps query parameters.
func NewQuery(v url.Values) *Query { return &Query{values: v} }

// Fail records a problem with a parameter.
func (q *Query) Fail(field, code, msg string) {
	q.problems = append(q.problems, FieldError{Field: field, Code: code, Message: msg})
}

// Err returns a 422 validation error listing every problem, or nil.
func (q *Query) Err() error {
	if len(q.problems) == 0 {
		return nil
	}
	return ValidationError(q.problems...)
}

// String returns a parameter, or "" if absent.
func (q *Query) String(name string) string { return q.values.Get(name) }

// Time parses an optional RFC 3339 timestamp.
func (q *Query) Time(name string) time.Time {
	v := q.values.Get(name)
	if v == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339Nano, v)
	if err != nil {
		q.Fail(name, FieldInvalidFormat, "must be an RFC 3339 timestamp such as 2026-09-21T10:15:30Z")
		return time.Time{}
	}
	return t
}

// UUID parses an optional UUID.
func (q *Query) UUID(name string) uuid.UUID {
	v := q.values.Get(name)
	if v == "" {
		return uuid.Nil
	}
	id, err := uuid.Parse(v)
	if err != nil {
		q.Fail(name, FieldInvalidFormat, "must be a UUID")
	}
	return id
}

// Limit parses the page size, between 1 and maxLimit.
func (q *Query) Limit(def, maxLimit int) int {
	v := q.values.Get("limit")
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 1 || n > maxLimit {
		q.Fail("limit", FieldOutOfRange, fmt.Sprintf("must be an integer from 1 to %d", maxLimit))
		return def
	}
	return n
}

// Cursor decodes the pagination cursor; uuid.Nil means the first page.
func (q *Query) Cursor() uuid.UUID {
	id, err := store.DecodeCursor(q.values.Get("cursor"))
	if err != nil {
		q.Fail("cursor", FieldInvalidFormat, "must be a next_cursor value from a previous response")
	}
	return id
}

// TimeRange checks that since precedes until when both are given.
func (q *Query) TimeRange(since, until time.Time) {
	if !since.IsZero() && !until.IsZero() && !since.Before(until) {
		q.Fail("since", FieldOutOfRange, "must be earlier than until")
	}
}

// Page is the envelope of every list response.
type Page[T any] struct {
	Data       []T     `json:"data"`
	NextCursor *string `json:"next_cursor"`
}

// NewPage builds a page; when more is true, the cursor points after lastID.
func NewPage[T any](data []T, more bool, lastID func() uuid.UUID) Page[T] {
	if data == nil {
		data = []T{}
	}
	p := Page[T]{Data: data}
	if more {
		c := store.EncodeCursor(lastID())
		p.NextCursor = &c
	}
	return p
}
