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

// Get returns one of the customer's messages, or ErrNotFound.
func (s *Service) Get(ctx context.Context, customerID, id uuid.UUID) (Message, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT `+columns+` FROM messages WHERE id = $1 AND customer_id = $2`, id, customerID)
	if err != nil {
		return Message{}, fmt.Errorf("get message: %w", err)
	}
	m, err := pgx.CollectExactlyOneRow(rows, scan)
	if errors.Is(err, pgx.ErrNoRows) {
		return Message{}, ErrNotFound
	}
	if err != nil {
		return Message{}, fmt.Errorf("get message: %w", err)
	}
	return m, nil
}

// Filter selects messages for List. Zero values mean "no filter".
type Filter struct {
	Status    Status
	Type      Type
	Recipient string
	Since     time.Time
	Until     time.Time
	// After is the last ID of the previous page; uuid.Nil starts at the newest.
	After uuid.UUID
	Limit int
}

// List returns a page of the customer's messages, newest first, and whether
// more pages follow. Time filters are applied through UUIDv7 bounds on the
// primary key, so the customer index serves every combination of filters.
func (s *Service) List(ctx context.Context, customerID uuid.UUID, f Filter) ([]Message, bool, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT `+columns+`
		FROM messages
		WHERE customer_id = $1
		  AND ($2::text IS NULL OR status = $2)
		  AND ($3::text IS NULL OR type = $3)
		  AND ($4::text IS NULL OR recipient = $4)
		  AND ($5::uuid IS NULL OR id >= $5)
		  AND ($6::uuid IS NULL OR id < $6)
		  AND ($7::uuid IS NULL OR id < $7)
		ORDER BY id DESC
		LIMIT $8`,
		customerID,
		store.NullString(string(f.Status)),
		store.NullString(string(f.Type)),
		store.NullString(f.Recipient),
		store.NullLowerBound(f.Since),
		store.NullLowerBound(f.Until),
		store.NullID(f.After),
		f.Limit+1)
	if err != nil {
		return nil, false, fmt.Errorf("list messages: %w", err)
	}
	list, err := pgx.CollectRows(rows, scan)
	if err != nil {
		return nil, false, fmt.Errorf("list messages: %w", err)
	}
	more := len(list) > f.Limit
	if more {
		list = list[:f.Limit]
	}
	return list, more, nil
}
