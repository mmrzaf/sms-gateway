package dispatch

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mmrzaf/sms-gatway/internal/message"
)

// job is a claimed message.
type job struct {
	ID         uuid.UUID
	CustomerID uuid.UUID
	Type       message.Type
	Recipient  string
	Body       string
	Attempts   int
	AcceptedAt time.Time
	ExpiresAt  time.Time
	// DBNow is the database time at claim; expiry decisions use it so that
	// application clocks never decide state.
	DBNow     time.Time
	claimedAt time.Time
}

// dbNow estimates the current database time.
func (j job) dbNow() time.Time {
	return j.DBNow.Add(time.Since(j.claimedAt))
}

// claim leases up to n ready rows of lane. Setting next_attempt_at to the
// lease deadline is the lease: the same condition, next_attempt_at <= now(),
// then selects both never-claimed rows and rows whose lease expired, so a
// crashed worker's messages are reclaimed by this ordinary query. Only
// messages still in the accepted status are claimed.
func (w *Worker) claim(ctx context.Context, lane string, n int) ([]job, error) {
	rows, err := w.db.Query(ctx, `
		WITH picked AS (
		    SELECT q.message_id
		    FROM queue q
		    JOIN messages m ON m.id = q.message_id AND m.status = 'accepted'
		    WHERE q.lane = $1 AND q.next_attempt_at <= now()
		    ORDER BY q.next_attempt_at
		    LIMIT $2
		    FOR UPDATE OF q SKIP LOCKED
		)
		UPDATE queue q
		SET lease_owner = $3,
		    next_attempt_at = now() + $4 * interval '1 millisecond'
		FROM picked, messages m
		WHERE q.message_id = picked.message_id AND m.id = q.message_id
		RETURNING m.id, m.customer_id, m.type, m.recipient, m.body, m.attempts,
		          m.accepted_at, m.expires_at, now()`,
		lane, n, w.id, w.cfg.LeaseDuration.Milliseconds())
	if err != nil {
		return nil, fmt.Errorf("claim %s: %w", lane, err)
	}
	claimedAt := time.Now()
	jobs, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (job, error) {
		var j job
		var typ string
		err := row.Scan(&j.ID, &j.CustomerID, &typ, &j.Recipient, &j.Body, &j.Attempts,
			&j.AcceptedAt, &j.ExpiresAt, &j.DBNow)
		j.Type, j.claimedAt = message.Type(typ), claimedAt
		return j, err
	})
	if err != nil {
		return nil, fmt.Errorf("claim %s: %w", lane, err)
	}
	return jobs, nil
}
