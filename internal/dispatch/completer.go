package dispatch

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mmrzaf/sms-gatway/internal/billing"
	"github.com/mmrzaf/sms-gatway/internal/message"
	"github.com/mmrzaf/sms-gatway/internal/metrics"
	"github.com/mmrzaf/sms-gatway/internal/store"
)

// completer commits dispatch outcomes in batches: one transaction per batch
// of up to CompleterBatchSize outcomes, or per CompleterFlushInterval,
// whichever comes first. Batching turns thousands of per-message commits
// into a few dozen per second.
//
// Every statement is guarded twice: message updates by a status
// compare-and-set, and queue changes by lease_owner, so the outcome of a
// worker whose lease expired cannot overwrite another worker's work.
type completer struct {
	w    *Worker
	in   chan outcome
	quit chan struct{}
	// refunded is the credits refunded by the commit in progress; it is
	// published only once the commit succeeds.
	refunded int64
}

func newCompleter(w *Worker) *completer {
	return &completer{
		w:    w,
		in:   make(chan outcome, 2*w.cfg.CompleterBatchSize),
		quit: make(chan struct{}),
	}
}

func (c *completer) submit(o outcome) { c.in <- o }

// stop flushes what is pending and ends run. Call it after the last submit.
func (c *completer) stop() { close(c.quit) }

func (c *completer) run() {
	size := c.w.cfg.CompleterBatchSize
	batch := make([]outcome, 0, size)
	timer := time.NewTimer(time.Hour)
	timer.Stop()

	flush := func() {
		timer.Stop()
		if len(batch) > 0 {
			c.flush(batch)
			batch = batch[:0]
		}
	}
	for {
		select {
		case o := <-c.in:
			if len(batch) == 0 {
				timer.Reset(c.w.cfg.CompleterFlushInterval)
			}
			batch = append(batch, o)
			if len(batch) >= size {
				flush()
			}
		case <-timer.C:
			flush()
		case <-c.quit:
			for {
				select {
				case o := <-c.in:
					batch = append(batch, o)
					if len(batch) >= size {
						flush()
					}
				default:
					flush()
					return
				}
			}
		}
	}
}

// flush commits a batch, retrying transient failures. If the batch still
// cannot be committed its outcomes are dropped: the leases expire and the
// messages are dispatched again, which providers deduplicate.
func (c *completer) flush(batch []outcome) {
	ctx := context.Background()
	var err error
	for attempt := 1; attempt <= 3; attempt++ {
		c.refunded = 0
		err = store.WithTx(ctx, c.w.db, func(tx pgx.Tx) error {
			return c.commit(ctx, tx, batch)
		})
		if err == nil {
			c.count(batch)
			metrics.CompleterBatchSize.Observe(float64(len(batch)))
			metrics.CreditsRefunded.Add(float64(c.refunded))
			return
		}
		time.Sleep(time.Duration(attempt) * 100 * time.Millisecond)
	}
	c.w.logger.Error("dropping dispatch outcomes after repeated commit failures",
		"outcomes", len(batch), "error", err)
}

// count publishes committed outcomes to the worker's counters and metrics.
func (c *completer) count(batch []outcome) {
	now := time.Now()
	for _, o := range batch {
		typ := string(o.job.Type)
		express := o.job.Type == message.Express
		latency := now.Sub(o.job.AcceptedAt)
		switch o.kind {
		case outcomeSent:
			c.w.counters.Sent.Add(1)
			metrics.MessagesCompleted.With(typ, "sent").Inc()
			metrics.MessageLatency.With(typ, "sent").Observe(latency.Seconds())
			if express && latency > c.w.cfg.ExpressSLA {
				metrics.ExpressSLABreaches.Inc()
			}
		case outcomeRetry:
			c.w.counters.Retried.Add(1)
		case outcomeDeferred:
			c.w.counters.Deferred.Add(1)
			metrics.Deferrals.With(typ).Inc()
		case outcomeFailed, outcomeExpired:
			status := "failed"
			if o.kind == outcomeExpired {
				status = "expired"
				c.w.counters.Expired.Add(1)
			} else {
				c.w.counters.Failed.Add(1)
			}
			metrics.MessagesCompleted.With(typ, status).Inc()
			metrics.MessageLatency.With(typ, "completed").Observe(latency.Seconds())
			if express {
				metrics.ExpressSLABreaches.Inc()
			}
		}
	}
}

// commit applies a batch inside tx. Outcomes are sorted by message ID so
// that concurrent transactions lock rows in the same order.
func (c *completer) commit(ctx context.Context, tx pgx.Tx, batch []outcome) error {
	sorted := slices.Clone(batch)
	slices.SortFunc(sorted, func(a, b outcome) int { return compareIDs(a.job.ID, b.job.ID) })

	var sent, retry, deferred, terminal []outcome
	for _, o := range sorted {
		switch o.kind {
		case outcomeSent:
			sent = append(sent, o)
		case outcomeRetry:
			retry = append(retry, o)
		case outcomeDeferred:
			deferred = append(deferred, o)
		default:
			terminal = append(terminal, o)
		}
	}
	for _, step := range []struct {
		outcomes []outcome
		apply    func(context.Context, pgx.Tx, []outcome) error
	}{
		{sent, c.commitSent},
		{retry, c.commitRetry},
		{deferred, c.commitDeferred},
		{terminal, c.commitTerminal},
	} {
		if len(step.outcomes) == 0 {
			continue
		}
		if err := step.apply(ctx, tx, step.outcomes); err != nil {
			return err
		}
	}
	return nil
}

// commitSent records provider acceptance. A status already made terminal by
// an early delivery report is kept, but the provider details are recorded.
// The attempt is counted only on the accepted-to-sent transition, so a
// retried commit never counts twice.
func (c *completer) commitSent(ctx context.Context, tx pgx.Tx, outcomes []outcome) error {
	ids, providers, refs := make([]uuid.UUID, len(outcomes)), make([]string, len(outcomes)), make([]string, len(outcomes))
	snaps := make([]int32, len(outcomes))
	for i, o := range outcomes {
		ids[i], providers[i], refs[i] = o.job.ID, o.provider, o.providerRef
		snaps[i] = int32(o.job.Attempts)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE messages m
		SET status = CASE WHEN m.status = 'accepted' THEN 'sent' ELSE m.status END,
		    sent_at = COALESCE(m.sent_at, now()),
		    provider = CASE WHEN m.status = 'accepted' THEN r.provider ELSE m.provider END,
		    provider_ref = CASE WHEN m.status = 'accepted' THEN r.provider_ref ELSE m.provider_ref END,
		    attempts = m.attempts + CASE WHEN m.attempts = r.snap THEN 1 ELSE 0 END,
		    sla_breached = m.sla_breached
		                   OR (m.type = 'express' AND now() - m.accepted_at > $5 * interval '1 millisecond'),
		    updated_at = now()
		FROM unnest($1::uuid[], $2::text[], $3::text[], $4::int[]) AS r(id, provider, provider_ref, snap)
		WHERE m.id = r.id AND m.status NOT IN ('failed', 'expired')`,
		ids, providers, refs, snaps, c.w.cfg.ExpressSLA.Milliseconds()); err != nil {
		return fmt.Errorf("record sent messages: %w", err)
	}
	return c.deleteQueueRows(ctx, tx, ids)
}

// commitRetry counts the failed attempt and schedules the next one. Messages
// that a delivery report has meanwhile made terminal are dequeued instead.
func (c *completer) commitRetry(ctx context.Context, tx pgx.Tx, outcomes []outcome) error {
	n := len(outcomes)
	ids, providers, errs := make([]uuid.UUID, n), make([]string, n), make([]string, n)
	snaps := make([]int32, n)
	delays := make(map[uuid.UUID]int64, n)
	for i, o := range outcomes {
		ids[i], providers[i], errs[i] = o.job.ID, o.provider, o.err
		snaps[i] = int32(o.job.Attempts)
		delays[o.job.ID] = o.delay.Milliseconds()
	}
	rows, err := tx.Query(ctx, `
		UPDATE messages m
		SET attempts = m.attempts + CASE WHEN m.attempts = r.snap THEN 1 ELSE 0 END,
		    provider = r.provider, last_error = r.error, updated_at = now()
		FROM unnest($1::uuid[], $2::text[], $3::text[], $4::int[]) AS r(id, provider, error, snap)
		WHERE m.id = r.id AND m.status = 'accepted'
		RETURNING m.id`,
		ids, providers, errs, snaps)
	if err != nil {
		return fmt.Errorf("record failed attempts: %w", err)
	}
	pending, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil {
		return fmt.Errorf("record failed attempts: %w", err)
	}

	pendingDelays := make([]int64, len(pending))
	isPending := make(map[uuid.UUID]bool, len(pending))
	for i, id := range pending {
		pendingDelays[i] = delays[id]
		isPending[id] = true
	}
	if _, err := tx.Exec(ctx, `
		UPDATE queue q
		SET lease_owner = NULL, next_attempt_at = now() + r.delay_ms * interval '1 millisecond'
		FROM unnest($1::uuid[], $2::bigint[]) AS r(id, delay_ms)
		WHERE q.message_id = r.id AND q.lease_owner = $3`,
		pending, pendingDelays, c.w.id); err != nil {
		return fmt.Errorf("reschedule messages: %w", err)
	}

	var finished []uuid.UUID
	for _, id := range ids {
		if !isPending[id] {
			finished = append(finished, id)
		}
	}
	return c.deleteQueueRows(ctx, tx, finished)
}

// commitDeferred returns messages to the queue without counting an attempt,
// because no provider was usable. Each outcome carries its own delay: circuit
// deferrals wait out the outage, lease deferrals release immediately.
func (c *completer) commitDeferred(ctx context.Context, tx pgx.Tx, outcomes []outcome) error {
	n := len(outcomes)
	ids := make([]uuid.UUID, n)
	delays := make([]int64, n)
	for i, o := range outcomes {
		ids[i] = o.job.ID
		delays[i] = o.delay.Milliseconds()
	}
	if _, err := tx.Exec(ctx, `
		UPDATE queue q
		SET lease_owner = NULL, next_attempt_at = now() + r.delay_ms * interval '1 millisecond'
		FROM unnest($1::uuid[], $2::bigint[]) AS r(id, delay_ms)
		WHERE q.message_id = r.id AND lease_owner = $3`,
		ids, delays, c.w.id); err != nil {
		return fmt.Errorf("defer messages: %w", err)
	}
	return nil
}

// commitTerminal ends messages as failed or expired and refunds exactly the
// messages whose status this statement changed, so no message is refunded twice.
func (c *completer) commitTerminal(ctx context.Context, tx pgx.Tx, outcomes []outcome) error {
	n := len(outcomes)
	var (
		ids       = make([]uuid.UUID, n)
		statuses  = make([]string, n)
		reasons   = make([]string, n)
		increment = make([]int32, n)
		providers = make([]*string, n)
		errs      = make([]*string, n)
	)
	for i, o := range outcomes {
		ids[i], reasons[i] = o.job.ID, string(o.reason)
		statuses[i] = "failed"
		if o.kind == outcomeExpired {
			statuses[i] = "expired"
		}
		if o.attempted {
			increment[i] = 1
		}
		providers[i], errs[i] = store.NullString(o.provider), store.NullString(o.err)
	}
	rows, err := tx.Query(ctx, `
		UPDATE messages m
		SET status = r.status,
		    failure_reason = r.reason,
		    completed_at = now(),
		    attempts = m.attempts + r.increment,
		    provider = COALESCE(r.provider, m.provider),
		    last_error = COALESCE(r.error, m.last_error),
		    sla_breached = m.sla_breached OR m.type = 'express',
		    updated_at = now()
		FROM unnest($1::uuid[], $2::text[], $3::text[], $4::int[], $5::text[], $6::text[])
		     AS r(id, status, reason, increment, provider, error)
		WHERE m.id = r.id AND m.status = 'accepted'
		RETURNING m.id, m.customer_id, m.cost`,
		ids, statuses, reasons, increment, providers, errs)
	if err != nil {
		return fmt.Errorf("finish messages: %w", err)
	}
	refunds, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (billing.Refund, error) {
		var r billing.Refund
		err := row.Scan(&r.MessageID, &r.CustomerID, &r.Amount)
		return r, err
	})
	if err != nil {
		return fmt.Errorf("finish messages: %w", err)
	}
	if err := billing.ApplyRefunds(ctx, tx, refunds); err != nil {
		return err
	}
	for _, r := range refunds {
		c.refunded += r.Amount
	}
	return c.deleteQueueRows(ctx, tx, ids)
}

func (c *completer) deleteQueueRows(ctx context.Context, tx pgx.Tx, ids []uuid.UUID) error {
	if len(ids) == 0 {
		return nil
	}
	if _, err := tx.Exec(ctx, `DELETE FROM queue WHERE message_id = ANY($1::uuid[]) AND lease_owner = $2`,
		ids, c.w.id); err != nil {
		return fmt.Errorf("dequeue messages: %w", err)
	}
	return nil
}

func compareIDs(a, b uuid.UUID) int {
	for i := range a {
		if a[i] != b[i] {
			return int(a[i]) - int(b[i])
		}
	}
	return 0
}
