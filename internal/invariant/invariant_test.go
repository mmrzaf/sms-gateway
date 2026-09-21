package invariant_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mmrzaf/sms-gatway/internal/invariant"
	"github.com/mmrzaf/sms-gatway/internal/message"
	"github.com/mmrzaf/sms-gatway/internal/store"
	"github.com/mmrzaf/sms-gatway/internal/testutil"
)

var cfg = invariant.Config{LeaseDuration: 30 * time.Second, SweepInterval: 10 * time.Second}

// setup creates a customer with one accepted message.
func setup(t *testing.T) (*pgxpool.Pool, uuid.UUID, uuid.UUID) {
	t.Helper()
	pool := testutil.DB(t)
	c, _ := testutil.Customer(t, pool, 10)
	svc := message.NewService(pool, message.Config{Prices: message.Prices{Normal: 1, Express: 3},
		MaxSegments: 10, NormalLanes: 4, NormalTTL: time.Hour, ExpressTTL: time.Minute})
	m, _, err := svc.Accept(context.Background(), c.ID, message.Request{To: "+989121234567", Text: "hi"})
	if err != nil {
		t.Fatal(err)
	}
	return pool, c.ID, m.ID
}

func TestConsistentDataPasses(t *testing.T) {
	pool, _, _ := setup(t)
	report, err := invariant.Run(context.Background(), pool, cfg)
	if err != nil || !report.OK || len(report.Checks) != len(invariant.Names()) {
		t.Fatalf("report: %+v, %v", report, err)
	}
}

// IT22: every check detects the corruption it guards against, and reports
// exactly the corrupted row.
func TestChecksDetectCorruption(t *testing.T) {
	tests := []struct {
		check string
		// corrupt damages the data and returns the ID the check must report.
		corrupt func(q store.Querier, customer, msg uuid.UUID) (uuid.UUID, []string)
	}{
		{"balance_matches_transactions", func(_ store.Querier, customer, _ uuid.UUID) (uuid.UUID, []string) {
			return customer, []string{`UPDATE customers SET balance = balance + 5`}
		}},
		{"one_debit_per_message", func(_ store.Querier, _, msg uuid.UUID) (uuid.UUID, []string) {
			return msg, []string{`DELETE FROM transactions WHERE kind = 'debit'`}
		}},
		{"refund_iff_failed_or_expired", func(_ store.Querier, _, msg uuid.UUID) (uuid.UUID, []string) {
			return msg, []string{`INSERT INTO transactions (id, customer_id, kind, amount, message_id)
				SELECT '` + store.NewID().String() + `', customer_id, 'refund', cost, id FROM messages`}
		}},
		{"accepted_messages_are_queued", func(_ store.Querier, _, msg uuid.UUID) (uuid.UUID, []string) {
			return msg, []string{`DELETE FROM queue`}
		}},
		{"finished_messages_leave_the_queue", func(_ store.Querier, _, msg uuid.UUID) (uuid.UUID, []string) {
			return msg, []string{`UPDATE messages SET status = 'delivered',
				accepted_at = now() - interval '3 hours', sent_at = now() - interval '2 hours',
				completed_at = now() - interval '2 hours', updated_at = now() - interval '2 hours'`}
		}},
		{"no_abandoned_leases", func(_ store.Querier, _, msg uuid.UUID) (uuid.UUID, []string) {
			return msg, []string{`UPDATE queue SET lease_owner = 'gone', next_attempt_at = now() - interval '2 minutes'`}
		}},
		{"timestamps_consistent", func(_ store.Querier, _, msg uuid.UUID) (uuid.UUID, []string) {
			return msg, []string{`UPDATE messages SET status = 'sent'`}
		}},
	}
	for _, tc := range tests {
		t.Run(tc.check, func(t *testing.T) {
			pool, customer, msg := setup(t)
			ctx := context.Background()
			want, statements := tc.corrupt(pool, customer, msg)
			for _, stmt := range statements {
				if _, err := pool.Exec(ctx, stmt); err != nil {
					t.Fatalf("corrupt: %v", err)
				}
			}

			report, err := invariant.Run(ctx, pool, cfg)
			if err != nil {
				t.Fatal(err)
			}
			if report.OK {
				t.Fatal("report is OK despite corruption")
			}
			for _, c := range report.Checks {
				if c.Name != tc.check {
					continue
				}
				if c.OK || c.Violations != 1 || len(c.Sample) != 1 || c.Sample[0] != want.String() {
					t.Errorf("%s: %+v, want exactly %s", c.Name, c, want)
				}
				return
			}
			t.Fatalf("check %s was not run", tc.check)
		})
	}
}
