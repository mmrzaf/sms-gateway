package api

import (
	"fmt"
	"net/url"
	"strconv"
	"time"

	"github.com/google/uuid"

	"github.com/mmrzaf/sms-gatway/internal/message"
	"github.com/mmrzaf/sms-gatway/internal/store"
)

// Page size bounds for list endpoints.
const (
	defaultLimit = 50
	maxLimit     = 200
)

// query reads URL query parameters and collects every problem, so a request
// with several bad parameters gets one response listing all of them.
type query struct {
	values   url.Values
	problems []message.FieldError
}

func newQuery(v url.Values) *query { return &query{values: v} }

func (q *query) fail(field, code, msg string) {
	q.problems = append(q.problems, message.FieldError{Field: field, Code: code, Message: msg})
}

func (q *query) err() error {
	if len(q.problems) == 0 {
		return nil
	}
	return &message.ValidationError{Fields: q.problems}
}

func (q *query) string(name string) string {
	return q.values.Get(name)
}

// time parses an optional RFC 3339 timestamp.
func (q *query) time(name string) time.Time {
	v := q.values.Get(name)
	if v == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339Nano, v)
	if err != nil {
		q.fail(name, message.CodeInvalidFormat, "must be an RFC 3339 timestamp such as 2026-09-21T10:15:30Z")
		return time.Time{}
	}
	return t
}

func (q *query) limit() int {
	v := q.values.Get("limit")
	if v == "" {
		return defaultLimit
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 1 || n > maxLimit {
		q.fail("limit", message.CodeOutOfRange, fmt.Sprintf("must be an integer from 1 to %d", maxLimit))
		return defaultLimit
	}
	return n
}

func (q *query) cursor() uuid.UUID {
	id, err := store.DecodeCursor(q.values.Get("cursor"))
	if err != nil {
		q.fail("cursor", message.CodeInvalidFormat, "must be a next_cursor value from a previous response")
	}
	return id
}

// timeRange checks that since precedes until when both are given.
func (q *query) timeRange(since, until time.Time) {
	if !since.IsZero() && !until.IsZero() && !since.Before(until) {
		q.fail("since", message.CodeOutOfRange, "must be earlier than until")
	}
}

// page is the envelope of every list response.
type page[T any] struct {
	Data       []T     `json:"data"`
	NextCursor *string `json:"next_cursor"`
}

func newPage[T any](data []T, more bool, lastID func() uuid.UUID) page[T] {
	if data == nil {
		data = []T{}
	}
	p := page[T]{Data: data}
	if more {
		c := store.EncodeCursor(lastID())
		p.NextCursor = &c
	}
	return p
}
