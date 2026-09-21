package sweeper

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mmrzaf/sms-gatway/internal/billing"
	"github.com/mmrzaf/sms-gatway/internal/message"
)

// expire ends messages whose TTL has passed and that no worker is sending,
// and refunds them. Without this task a message could wait forever during a
// full provider outage, when no worker claims anything.
func (s *Sweeper) expire(ctx context.Context, tx pgx.Tx) (int, error) {
	rows, err := tx.Query(ctx, `
		WITH due AS (
		    SELECT message_id FROM queue
		    WHERE expires_at <= now()
		      AND (lease_owner IS NULL OR next_attempt_at <= now())
		    ORDER BY message_id
		    LIMIT $1
		    FOR UPDATE SKIP LOCKED
		)
		UPDATE messages m
		SET status = 'expired',
		    failure_reason = 'expired',
		    completed_at = now(),
		    sla_breached = m.sla_breached OR m.type = 'express',
		    updated_at = now()
		FROM due
		WHERE m.id = due.message_id AND m.status = 'accepted'
		RETURNING m.id, m.customer_id, m.cost`,
		s.cfg.BatchSize)
	if err != nil {
		return 0, fmt.Errorf("expire messages: %w", err)
	}
	refunds, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (billing.Refund, error) {
		var r billing.Refund
		err := row.Scan(&r.MessageID, &r.CustomerID, &r.Amount)
		return r, err
	})
	if err != nil {
		return 0, fmt.Errorf("expire messages: %w", err)
	}
	if err := billing.ApplyRefunds(ctx, tx, refunds); err != nil {
		return 0, err
	}
	ids := make([]uuid.UUID, len(refunds))
	for i, r := range refunds {
		ids[i] = r.MessageID
	}
	if _, err := tx.Exec(ctx, `DELETE FROM queue WHERE message_id = ANY($1::uuid[])`, ids); err != nil {
		return 0, fmt.Errorf("dequeue expired messages: %w", err)
	}
	return len(refunds), nil
}

// flagSLA marks Express messages that are still waiting beyond the SLA, so
// breaches are visible while they happen rather than only once they end.
func (s *Sweeper) flagSLA(ctx context.Context, tx pgx.Tx) (int, error) {
	tag, err := tx.Exec(ctx, `
		UPDATE messages m
		SET sla_breached = true, updated_at = now()
		WHERE m.id IN (
		    SELECT q.message_id
		    FROM queue q
		    JOIN messages w ON w.id = q.message_id
		    WHERE q.lane = $1
		      AND w.status = 'accepted'
		      AND NOT w.sla_breached
		      AND w.accepted_at < now() - $2 * interval '1 millisecond'
		    LIMIT $3
		)`,
		message.ExpressLane, s.cfg.ExpressSLA.Milliseconds(), s.cfg.BatchSize)
	if err != nil {
		return 0, fmt.Errorf("flag sla breaches: %w", err)
	}
	return int(tag.RowsAffected()), nil
}

// removeOrphans deletes queue rows of messages that are no longer accepted
// and that no worker holds. They appear when a delivery report finalizes a
// message that was waiting for a retry.
func (s *Sweeper) removeOrphans(ctx context.Context, tx pgx.Tx) (int, error) {
	tag, err := tx.Exec(ctx, `
		DELETE FROM queue
		WHERE message_id IN (
		    SELECT q.message_id
		    FROM queue q
		    JOIN messages m ON m.id = q.message_id
		    WHERE m.status <> 'accepted'
		      AND (q.lease_owner IS NULL OR q.next_attempt_at <= now())
		    LIMIT $1
		    FOR UPDATE OF q SKIP LOCKED
		)`,
		s.cfg.BatchSize)
	if err != nil {
		return 0, fmt.Errorf("remove orphaned queue rows: %w", err)
	}
	return int(tag.RowsAffected()), nil
}

// reassignLanes moves rows out of normal lanes that no longer exist after
// NORMAL_LANES was lowered, to the lane their customer now hashes to.
func (s *Sweeper) reassignLanes(ctx context.Context, tx pgx.Tx) (int, error) {
	rows, err := tx.Query(ctx, `
		SELECT q.message_id, m.customer_id, q.lane
		FROM queue q
		JOIN messages m ON m.id = q.message_id
		WHERE q.lane LIKE 'normal-%' AND substring(q.lane FROM 8)::int >= $2
		LIMIT $1
		FOR UPDATE OF q SKIP LOCKED`,
		s.cfg.BatchSize, s.cfg.NormalLanes)
	if err != nil {
		return 0, fmt.Errorf("find rows in removed lanes: %w", err)
	}
	var ids []uuid.UUID
	var lanes []string
	var id, customer uuid.UUID
	var lane string
	_, err = pgx.ForEachRow(rows, []any{&id, &customer, &lane}, func() error {
		if laneIndex(lane) >= s.cfg.NormalLanes {
			ids = append(ids, id)
			lanes = append(lanes, message.Lane(message.Normal, customer, s.cfg.NormalLanes))
		}
		return nil
	})
	if err != nil || len(ids) == 0 {
		return 0, err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE queue q SET lane = r.lane
		FROM unnest($1::uuid[], $2::text[]) AS r(id, lane)
		WHERE q.message_id = r.id`,
		ids, lanes); err != nil {
		return 0, fmt.Errorf("reassign lanes: %w", err)
	}
	return len(ids), nil
}

// laneIndex parses "normal-<i>", returning -1 for any other name.
func laneIndex(lane string) int {
	n, err := strconv.Atoi(strings.TrimPrefix(lane, "normal-"))
	if err != nil || !strings.HasPrefix(lane, "normal-") {
		return -1
	}
	return n
}

// removeStaleWorkers deletes records of workers that stopped without
// removing themselves.
func (s *Sweeper) removeStaleWorkers(ctx context.Context, tx pgx.Tx) (int, error) {
	tag, err := tx.Exec(ctx, `DELETE FROM workers WHERE last_seen < now() - $1 * interval '1 millisecond'`,
		staleWorkerAge.Milliseconds())
	if err != nil {
		return 0, fmt.Errorf("remove stale workers: %w", err)
	}
	return int(tag.RowsAffected()), nil
}
