// Package invariant verifies the properties the system guarantees at every
// commit: credits add up, every message is charged exactly once and refunded
// only when it should be, and the queue agrees with message statuses.
//
// The schema already rules out several violations (negative balances,
// double debits, double refunds). These checks cover what depends on
// application logic. They run in one REPEATABLE READ transaction, so they see
// a single consistent snapshot even while traffic flows.
package invariant

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mmrzaf/sms-gatway/internal/store"
)

// Config holds the thresholds of the time-based checks.
type Config struct {
	LeaseDuration time.Duration
	SweepInterval time.Duration
}

// sampleSize is how many violating IDs a check reports.
const sampleSize = 10

// Check is the result of one invariant check.
type Check struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	OK          bool     `json:"ok"`
	Violations  int      `json:"violations"`
	Sample      []string `json:"sample"`
}

// Report is the result of every check.
type Report struct {
	OK        bool      `json:"ok"`
	CheckedAt time.Time `json:"checked_at"`
	Checks    []Check   `json:"checks"`
}

// definition is a check: a query returning the IDs that violate it.
type definition struct {
	name        string
	description string
	query       string
	args        func(Config) []any
}

var definitions = []definition{
	{
		name:        "balance_matches_transactions",
		description: "Every balance equals the sum of its customer's transactions.",
		query: `
			SELECT c.id FROM customers c
			LEFT JOIN transactions t ON t.customer_id = c.id
			GROUP BY c.id, c.balance
			HAVING c.balance <> COALESCE(sum(t.amount), 0)`,
	},
	{
		name:        "one_debit_per_message",
		description: "Every message has exactly one debit, equal to its cost.",
		query: `
			SELECT m.id FROM messages m
			LEFT JOIN transactions t ON t.message_id = m.id AND t.kind = 'debit'
			WHERE t.id IS NULL OR t.amount <> -m.cost`,
	},
	{
		name:        "refund_iff_failed_or_expired",
		description: "A message has a refund equal to its cost exactly when it is failed or expired.",
		query: `
			SELECT m.id FROM messages m
			LEFT JOIN transactions t ON t.message_id = m.id AND t.kind = 'refund'
			WHERE (m.status IN ('failed', 'expired')) <> (t.id IS NOT NULL)
			   OR (t.id IS NOT NULL AND t.amount <> m.cost)`,
	},
	{
		name:        "accepted_messages_are_queued",
		description: "Every accepted message has a queue row.",
		query: `
			SELECT m.id FROM messages m
			LEFT JOIN queue q ON q.message_id = m.id
			WHERE m.status = 'accepted' AND q.message_id IS NULL`,
	},
	{
		name:        "finished_messages_leave_the_queue",
		description: "No message that left the accepted status keeps a queue row beyond two leases and sweeps.",
		query: `
			SELECT q.message_id FROM queue q
			JOIN messages m ON m.id = q.message_id
			WHERE m.status <> 'accepted'
			  AND m.updated_at < now() - $1 * interval '1 millisecond'`,
		args: func(c Config) []any {
			return []any{2 * (c.LeaseDuration + c.SweepInterval).Milliseconds()}
		},
	},
	{
		name:        "no_abandoned_leases",
		description: "No lease has been expired for more than a minute; otherwise no worker serves its lane.",
		query: `
			SELECT message_id FROM queue
			WHERE lease_owner IS NOT NULL AND next_attempt_at < now() - interval '1 minute'`,
	},
	{
		name:        "timestamps_consistent",
		description: "Status timestamps are present when required and never precede acceptance.",
		query: `
			SELECT id FROM messages
			WHERE (status = 'sent' AND sent_at IS NULL)
			   OR (status IN ('delivered', 'undelivered', 'failed', 'expired') AND completed_at IS NULL)
			   OR (sent_at IS NOT NULL AND sent_at < accepted_at)
			   OR (completed_at IS NOT NULL AND completed_at < accepted_at)`,
	},
}

// Names lists the checks in the order they run.
func Names() []string {
	names := make([]string, len(definitions))
	for i, d := range definitions {
		names[i] = d.name
	}
	return names
}

// Run executes every check in one snapshot.
func Run(ctx context.Context, db *pgxpool.Pool, cfg Config) (Report, error) {
	report := Report{OK: true}
	err := store.WithTxOptions(ctx, db, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly},
		func(tx pgx.Tx) error {
			if err := tx.QueryRow(ctx, `SELECT now()`).Scan(&report.CheckedAt); err != nil {
				return err
			}
			for _, d := range definitions {
				c, err := run(ctx, tx, d, cfg)
				if err != nil {
					return err
				}
				report.OK = report.OK && c.OK
				report.Checks = append(report.Checks, c)
			}
			return nil
		})
	if err != nil {
		return Report{}, err
	}
	return report, nil
}

func run(ctx context.Context, tx pgx.Tx, d definition, cfg Config) (Check, error) {
	var args []any
	if d.args != nil {
		args = d.args(cfg)
	}
	rows, err := tx.Query(ctx, `
		SELECT v.id::text, count(*) OVER ()
		FROM (`+d.query+`) AS v(id)
		ORDER BY v.id
		LIMIT `+fmt.Sprint(sampleSize), args...)
	if err != nil {
		return Check{}, fmt.Errorf("check %s: %w", d.name, err)
	}
	c := Check{Name: d.name, Description: d.description, Sample: []string{}}
	var id string
	var total int
	_, err = pgx.ForEachRow(rows, []any{&id, &total}, func() error {
		c.Sample = append(c.Sample, id)
		c.Violations = total
		return nil
	})
	if err != nil {
		return Check{}, fmt.Errorf("check %s: %w", d.name, err)
	}
	c.OK = c.Violations == 0
	return c, nil
}
