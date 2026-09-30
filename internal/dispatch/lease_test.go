package dispatch

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/mmrzaf/sms-gatway/internal/message"
	"github.com/mmrzaf/sms-gatway/internal/store"
	"github.com/mmrzaf/sms-gatway/internal/testutil"
)

func TestReclaimedLeaseRejectsLateCompletion(t *testing.T) {
	for _, sameWorker := range []bool{false, true} {
		for _, kind := range []outcomeKind{outcomeSent, outcomeRetry, outcomeFailed, outcomeExpired, outcomeDeferred} {
			t.Run(fmt.Sprintf("same_worker=%t/kind=%d", sameWorker, kind), func(t *testing.T) {
				ctx := context.Background()
				db := testutil.DB(t)
				svc := newMessages(db, 4)
				customer, _ := testutil.Customer(t, db, 100)
				m := accept(t, svc, customer.ID, message.Normal, "+989121234567")
				first := New(testConfig("http://unused"), db, discard)
				next := first
				if !sameWorker {
					next = New(testConfig("http://unused"), db, discard)
				}
				lane := message.Lane(message.Normal, customer.ID, 4)
				old, err := first.claim(ctx, lane, 1)
				if err != nil || len(old) != 1 {
					t.Fatalf("first claim: %v, %v", old, err)
				}
				if _, err := db.Exec(ctx, `UPDATE queue SET next_attempt_at = now() - interval '1 second' WHERE message_id = $1`, m.ID); err != nil {
					t.Fatal(err)
				}
				current, err := next.claim(ctx, lane, 1)
				if err != nil || len(current) != 1 {
					t.Fatalf("reclaim: %v, %v", current, err)
				}
				if current[0].LeaseVersion != old[0].LeaseVersion+1 {
					t.Fatal("claim did not advance lease generation")
				}
				reason := message.ReasonRejected
				if kind == outcomeExpired {
					reason = message.ReasonExpired
				}
				late := outcome{kind: kind, job: old[0], provider: "A", providerRef: "late", reason: reason, attempted: true, delay: time.Hour}
				first.completer.flush(ctx, []outcome{late})
				if first.counters.Sent.Load() != 0 || first.counters.Retried.Load() != 0 || first.counters.Failed.Load() != 0 || first.counters.Expired.Load() != 0 || first.counters.Deferred.Load() != 0 {
					t.Fatal("stale outcome changed completion counters")
				}
				got := reload(t, svc, m)
				if got.Status != message.StatusAccepted || got.Attempts != 0 || got.SentAt != nil {
					t.Fatalf("late outcome changed message: %+v", got)
				}
				var owner string
				var version, balance int64
				if err := db.QueryRow(ctx, `SELECT lease_owner, lease_version FROM queue WHERE message_id = $1`, m.ID).Scan(&owner, &version); err != nil {
					t.Fatal(err)
				}
				if owner != next.id || version != current[0].LeaseVersion {
					t.Fatalf("late outcome changed lease: %s/%d", owner, version)
				}
				if err := db.QueryRow(ctx, `SELECT balance FROM customers WHERE id = $1`, customer.ID).Scan(&balance); err != nil {
					t.Fatal(err)
				}
				if balance != 99 {
					t.Fatalf("late outcome refunded balance: %d", balance)
				}
				final := outcome{kind: outcomeSent, job: current[0], provider: "B", providerRef: "current", attempted: true}
				if err := store.WithTx(ctx, db, func(tx pgx.Tx) error { return next.completer.commit(ctx, tx, []outcome{final}) }); err != nil {
					t.Fatal(err)
				}
				if got := reload(t, svc, m); got.Status != message.StatusSent || got.Attempts != 1 {
					t.Fatalf("current completion: %+v", got)
				}
				testutil.AssertInvariants(t, db)
			})
		}
	}
}

func TestCompletionCountersReflectAppliedRows(t *testing.T) {
	ctx := context.Background()
	db := testutil.DB(t)
	svc := newMessages(db, 4)
	customer, _ := testutil.Customer(t, db, 100)
	w := New(testConfig("http://unused"), db, discard)
	lane := message.Lane(message.Normal, customer.ID, 4)
	m := accept(t, svc, customer.ID, message.Normal, "+989121234567")
	jobs, err := w.claim(ctx, lane, 1)
	if err != nil || len(jobs) != 1 {
		t.Fatalf("claim: %v, %v", jobs, err)
	}
	sent := outcome{kind: outcomeSent, job: jobs[0], provider: "A", providerRef: "current", attempted: true}
	w.completer.flush(ctx, []outcome{sent})
	w.completer.flush(ctx, []outcome{sent})
	if w.counters.Sent.Load() != 1 {
		t.Fatalf("repeated success counted: %d", w.counters.Sent.Load())
	}
	if got := reload(t, svc, m); got.Attempts != 1 {
		t.Fatalf("attempts: %d", got.Attempts)
	}

	for _, kind := range []outcomeKind{outcomeFailed, outcomeRetry} {
		m := accept(t, svc, customer.ID, message.Normal, "+989121234568")
		jobs, err := w.claim(ctx, lane, 1)
		if err != nil || len(jobs) != 1 {
			t.Fatalf("claim: %v, %v", jobs, err)
		}
		if _, err := db.Exec(ctx, `UPDATE messages SET status = 'delivered', completed_at = now() WHERE id = $1`, m.ID); err != nil {
			t.Fatal(err)
		}
		w.completer.flush(ctx, []outcome{{kind: kind, job: jobs[0], reason: message.ReasonRejected, attempted: true}})
		if w.counters.Failed.Load() != 0 || w.counters.Retried.Load() != 0 {
			t.Fatal("superseded outcome counted")
		}
		if got := reload(t, svc, m); got.Status != message.StatusDelivered {
			t.Fatalf("terminal status changed: %s", got.Status)
		}
	}
	testutil.AssertInvariants(t, db)
}
