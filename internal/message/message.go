// Package message accepts customer messages and answers queries about them.
//
// Acceptance is one database transaction that inserts the messages, records a
// debit per message, enqueues them for dispatch, and takes the total cost from
// the customer's balance. Either all of it commits or none of it does.
package message

import (
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mmrzaf/sms-gatway/internal/segment"
)

// Type is the service class of a message.
type Type string

// Service classes.
const (
	Normal  Type = "normal"
	Express Type = "express"
)

// ParseType validates a service class name.
func ParseType(s string) (Type, bool) {
	switch t := Type(s); t {
	case Normal, Express:
		return t, true
	}
	return "", false
}

// Status is the lifecycle status of a message.
type Status string

// Message statuses.
const (
	StatusAccepted    Status = "accepted"
	StatusSent        Status = "sent"
	StatusDelivered   Status = "delivered"
	StatusUndelivered Status = "undelivered"
	StatusFailed      Status = "failed"
	StatusExpired     Status = "expired"
)

// Statuses lists every status in lifecycle order.
var Statuses = []Status{StatusAccepted, StatusSent, StatusDelivered, StatusUndelivered, StatusFailed, StatusExpired}

// ParseStatus validates a status name.
func ParseStatus(s string) (Status, bool) {
	for _, st := range Statuses {
		if string(st) == s {
			return st, true
		}
	}
	return "", false
}

// Terminal reports whether no further transition can leave the status.
func (s Status) Terminal() bool {
	switch s {
	case StatusDelivered, StatusUndelivered, StatusFailed, StatusExpired:
		return true
	}
	return false
}

// FailureReason explains why a message ended failed or expired.
type FailureReason string

// Failure reasons.
const (
	ReasonRejected          FailureReason = "rejected"
	ReasonAttemptsExhausted FailureReason = "attempts_exhausted"
	ReasonExpired           FailureReason = "expired"
)

// ErrNotFound means the message does not exist or belongs to another customer.
var ErrNotFound = errors.New("message not found")

// Message is a stored message.
type Message struct {
	ID            uuid.UUID
	CustomerID    uuid.UUID
	Type          Type
	Recipient     string
	Body          string
	Encoding      segment.Encoding
	Segments      int
	Cost          int64
	Status        Status
	Attempts      int
	Provider      *string
	ProviderRef   *string
	LastError     *string
	FailureReason *FailureReason
	SLABreached   bool
	ClientRef     *string
	AcceptedAt    time.Time
	ExpiresAt     time.Time
	SentAt        *time.Time
	CompletedAt   *time.Time
}

const columns = `id, customer_id, type, recipient, body, encoding, segments, cost, status, attempts,
	provider, provider_ref, last_error, failure_reason, sla_breached, client_ref,
	accepted_at, expires_at, sent_at, completed_at`

func scan(row pgx.CollectableRow) (Message, error) {
	return scanWith(row)
}

// scanWith scans the message columns followed by extra destinations.
func scanWith(row pgx.CollectableRow, extra ...any) (Message, error) {
	var (
		m                     Message
		typ, encoding, status string
		failureReason         *string
	)
	dest := []any{&m.ID, &m.CustomerID, &typ, &m.Recipient, &m.Body, &encoding, &m.Segments,
		&m.Cost, &status, &m.Attempts, &m.Provider, &m.ProviderRef, &m.LastError, &failureReason,
		&m.SLABreached, &m.ClientRef, &m.AcceptedAt, &m.ExpiresAt, &m.SentAt, &m.CompletedAt}
	if err := row.Scan(append(dest, extra...)...); err != nil {
		return Message{}, err
	}
	m.Type, m.Encoding, m.Status = Type(typ), segment.Encoding(encoding), Status(status)
	if failureReason != nil {
		r := FailureReason(*failureReason)
		m.FailureReason = &r
	}
	return m, nil
}

// prefixed qualifies every column in a comma-separated list with prefix.
func prefixed(prefix, cols string) string {
	parts := strings.Split(cols, ",")
	for i, p := range parts {
		parts[i] = prefix + strings.TrimSpace(p)
	}
	return strings.Join(parts, ", ")
}
