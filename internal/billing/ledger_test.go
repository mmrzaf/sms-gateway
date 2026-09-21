package billing_test

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/mmrzaf/sms-gatway/internal/billing"
	"github.com/mmrzaf/sms-gatway/internal/store"
	"github.com/mmrzaf/sms-gatway/internal/testutil"
)

// IT07: charge idempotency.
func TestChargeIsIdempotentByClientRef(t *testing.T) {
	pool := testutil.DB(t)
	c, _ := testutil.Customer(t, pool, 0)
	ctx := context.Background()

	first, err := billing.ChargeCustomer(ctx, pool, c.ID, 500, "topup-1")
	if err != nil || first.Replayed || first.Balance != 500 {
		t.Fatalf("first charge: %+v, %v", first, err)
	}
	again, err := billing.ChargeCustomer(ctx, pool, c.ID, 500, "topup-1")
	if err != nil || !again.Replayed || again.Balance != 500 || again.Transaction.ID != first.Transaction.ID {
		t.Fatalf("replay: %+v, %v", again, err)
	}
	if _, err := billing.ChargeCustomer(ctx, pool, c.ID, 700, "topup-1"); !errors.Is(err, billing.ErrChargeConflict) {
		t.Fatalf("different amount: %v", err)
	}
	b, _ := billing.GetBalance(ctx, pool, c.ID)
	if b.Amount != 500 {
		t.Errorf("balance = %d, want 500", b.Amount)
	}
	testutil.AssertLedgerConsistent(t, pool)
}

func TestChargeUnknownCustomer(t *testing.T) {
	pool := testutil.DB(t)
	_, err := billing.ChargeCustomer(context.Background(), pool, store.NewID(), 1, "x")
	if !errors.Is(err, billing.ErrCustomerNotFound) {
		t.Fatalf("got %v", err)
	}
}

func TestDebitBalanceRefusesOverspend(t *testing.T) {
	pool := testutil.DB(t)
	c, _ := testutil.Customer(t, pool, 10)
	ctx := context.Background()

	err := store.WithTx(ctx, pool, func(tx pgx.Tx) error {
		_, err := billing.DebitBalance(ctx, tx, c.ID, 11)
		return err
	})
	if !errors.Is(err, billing.ErrInsufficientCredits) {
		t.Fatalf("got %v", err)
	}
	var left int64
	err = store.WithTx(ctx, pool, func(tx pgx.Tx) error {
		var err error
		left, err = billing.DebitBalance(ctx, tx, c.ID, 10)
		return err
	})
	if err != nil || left != 0 {
		t.Fatalf("exact debit: %d, %v", left, err)
	}
}

func TestApplyRefundsOnce(t *testing.T) {
	pool := testutil.DB(t)
	c, _ := testutil.Customer(t, pool, 10)
	ctx := context.Background()

	// A message with its debit, as the accept transaction records it.
	msgID := store.NewID()
	err := store.WithTx(ctx, pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `
			INSERT INTO messages (id, customer_id, type, recipient, body, encoding, segments, cost, expires_at)
			VALUES ($1, $2, 'normal', '+989121234567', 'hi', 'gsm7', 1, 3, now() + interval '1 hour')`,
			msgID, c.ID); err != nil {
			return err
		}
		if err := billing.RecordDebits(ctx, tx, c.ID, []billing.Debit{{MessageID: msgID, Amount: 3}}); err != nil {
			return err
		}
		_, err := billing.DebitBalance(ctx, tx, c.ID, 3)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}

	refund := []billing.Refund{{CustomerID: c.ID, MessageID: msgID, Amount: 3}}
	for range 2 {
		if err := store.WithTx(ctx, pool, func(tx pgx.Tx) error {
			return billing.ApplyRefunds(ctx, tx, refund)
		}); err != nil {
			t.Fatal(err)
		}
	}
	b, _ := billing.GetBalance(ctx, pool, c.ID)
	if b.Amount != 10 {
		t.Errorf("balance = %d, want 10 after one refund", b.Amount)
	}
	testutil.AssertLedgerConsistent(t, pool)
}

func TestListTransactions(t *testing.T) {
	pool := testutil.DB(t)
	c, _ := testutil.Customer(t, pool, 5) // one charge
	ctx := context.Background()
	for _, r := range []string{"a", "b"} {
		if _, err := billing.ChargeCustomer(ctx, pool, c.ID, 1, r); err != nil {
			t.Fatal(err)
		}
	}
	page, more, err := billing.ListTransactions(ctx, pool, c.ID, billing.TransactionFilter{Limit: 2})
	if err != nil || len(page) != 2 || !more || *page[0].ClientRef != "b" {
		t.Fatalf("first page: %v, %d, %v", err, len(page), more)
	}
	rest, more, err := billing.ListTransactions(ctx, pool, c.ID, billing.TransactionFilter{Limit: 2, After: page[1].ID})
	if err != nil || len(rest) != 1 || more || rest[0].Amount != 5 {
		t.Fatalf("second page: %v, %d, %v", err, len(rest), more)
	}
	debits, _, err := billing.ListTransactions(ctx, pool, c.ID, billing.TransactionFilter{Kind: billing.KindDebit, Limit: 10})
	if err != nil || len(debits) != 0 {
		t.Fatalf("debit filter: %v, %d", err, len(debits))
	}
}
