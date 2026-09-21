package message

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mmrzaf/sms-gatway/internal/store"
)

// QueueState is a message's queue row, as operators see it.
type QueueState struct {
	Lane          string
	NextAttemptAt time.Time
	LeaseOwner    *string
}

// Inspected is a message with operator details: its customer's name and its
// queue row, if any.
type Inspected struct {
	Message
	CustomerName string
	Queue        *QueueState
}

// AdminFilter selects messages across customers. Zero values mean "no
// filter"; without a customer, only messages accepted within the last 24
// hours are listed.
type AdminFilter struct {
	CustomerID uuid.UUID
	Filter
}

// ListAll returns a page of messages across customers, newest first.
func (s *Service) ListAll(ctx context.Context, f AdminFilter, now time.Time) ([]Inspected, bool, error) {
	since := f.Since
	if f.CustomerID == uuid.Nil && since.IsZero() {
		since = now.Add(-24 * time.Hour)
	}
	rows, err := s.pool.Query(ctx, `
		SELECT `+prefixed("m.", columns)+`, c.name
		FROM messages m
		JOIN customers c ON c.id = m.customer_id
		WHERE ($1::uuid IS NULL OR m.customer_id = $1)
		  AND ($2::text IS NULL OR m.status = $2)
		  AND ($3::text IS NULL OR m.type = $3)
		  AND ($4::uuid IS NULL OR m.id >= $4)
		  AND ($5::uuid IS NULL OR m.id < $5)
		  AND ($6::uuid IS NULL OR m.id < $6)
		ORDER BY m.id DESC
		LIMIT $7`,
		store.NullID(f.CustomerID),
		store.NullString(string(f.Status)),
		store.NullString(string(f.Type)),
		store.NullLowerBound(since),
		store.NullLowerBound(f.Until),
		store.NullID(f.After),
		f.Limit+1)
	if err != nil {
		return nil, false, fmt.Errorf("list messages: %w", err)
	}
	list, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (Inspected, error) {
		var in Inspected
		m, err := scanWith(row, &in.CustomerName)
		in.Message = m
		return in, err
	})
	if err != nil {
		return nil, false, fmt.Errorf("list messages: %w", err)
	}
	more := len(list) > f.Limit
	if more {
		list = list[:f.Limit]
	}
	return list, more, nil
}

// Inspect returns any message with its customer's name and queue row.
func (s *Service) Inspect(ctx context.Context, id uuid.UUID) (Inspected, error) {
	var (
		in          Inspected
		lane        *string
		nextAttempt *time.Time
		leaseOwner  *string
	)
	rows, err := s.pool.Query(ctx, `
		SELECT `+prefixed("m.", columns)+`, c.name, q.lane, q.next_attempt_at, q.lease_owner
		FROM messages m
		JOIN customers c ON c.id = m.customer_id
		LEFT JOIN queue q ON q.message_id = m.id
		WHERE m.id = $1`, id)
	if err != nil {
		return Inspected{}, fmt.Errorf("inspect message: %w", err)
	}
	in, err = pgx.CollectExactlyOneRow(rows, func(row pgx.CollectableRow) (Inspected, error) {
		var r Inspected
		m, err := scanWith(row, &r.CustomerName, &lane, &nextAttempt, &leaseOwner)
		r.Message = m
		return r, err
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return Inspected{}, ErrNotFound
	}
	if err != nil {
		return Inspected{}, fmt.Errorf("inspect message: %w", err)
	}
	if lane != nil {
		in.Queue = &QueueState{Lane: *lane, NextAttemptAt: *nextAttempt, LeaseOwner: leaseOwner}
	}
	return in, nil
}
