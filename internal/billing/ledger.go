// Package billing records credit movements. Every change to a customer's
// balance goes through this package, always inside the caller's transaction
// and always paired with a row in the transactions table, so that a balance
// equals the sum of its transactions at every commit.
package billing

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mmrzaf/sms-gatway/internal/store"
)

// Kind is the type of a credit transaction.
type Kind string

// Transaction kinds.
const (
	KindCharge Kind = "charge"
	KindDebit  Kind = "debit"
	KindRefund Kind = "refund"
)

// ParseKind validates a transaction kind.
func ParseKind(s string) (Kind, bool) {
	switch k := Kind(s); k {
	case KindCharge, KindDebit, KindRefund:
		return k, true
	}
	return "", false
}

// MaxChargeAmount is the largest amount a single charge may add.
const MaxChargeAmount = 1_000_000_000

var (
	// ErrInsufficientCredits means the balance is lower than the amount to debit.
	ErrInsufficientCredits = errors.New("insufficient credits")
	// ErrChargeConflict means a charge reused a client_ref with a different amount.
	ErrChargeConflict = errors.New("charge client_ref reused with a different amount")
	// ErrCustomerNotFound means the customer does not exist.
	ErrCustomerNotFound = errors.New("customer not found")
)

// Transaction is one credit movement.
type Transaction struct {
	ID         uuid.UUID
	CustomerID uuid.UUID
	Kind       Kind
	Amount     int64
	MessageID  *uuid.UUID
	ClientRef  *string
	CreatedAt  time.Time
}

// Debit is one message's debit, recorded by RecordDebits.
type Debit struct {
	MessageID uuid.UUID
	Amount    int64 // positive; stored as a negative transaction amount
}

// RecordDebits inserts one debit transaction per message. It does not change
// the balance; the caller takes the total with DebitBalance in the same
// transaction.
func RecordDebits(ctx context.Context, tx pgx.Tx, customerID uuid.UUID, debits []Debit) error {
	ids := make([]uuid.UUID, len(debits))
	messageIDs := make([]uuid.UUID, len(debits))
	amounts := make([]int64, len(debits))
	for i, d := range debits {
		ids[i] = store.NewID()
		messageIDs[i] = d.MessageID
		amounts[i] = -d.Amount
	}
	_, err := tx.Exec(ctx, `
		INSERT INTO transactions (id, customer_id, kind, amount, message_id)
		SELECT d.id, $1, 'debit', d.amount, d.message_id
		FROM unnest($2::uuid[], $3::bigint[], $4::uuid[]) AS d(id, amount, message_id)`,
		customerID, ids, amounts, messageIDs)
	if err != nil {
		return fmt.Errorf("record debits: %w", err)
	}
	return nil
}

// DebitBalance subtracts amount from the customer's balance if the balance
// covers it, and returns the new balance. It returns ErrInsufficientCredits
// otherwise.
//
// The conditional update is evaluated after the row lock is acquired, against
// the latest committed balance, so concurrent debits can never overspend.
// Callers run it as the last statement before commit to hold the customer's
// row lock as briefly as possible.
func DebitBalance(ctx context.Context, tx pgx.Tx, customerID uuid.UUID, amount int64) (int64, error) {
	var balance int64
	err := tx.QueryRow(ctx, `
		UPDATE customers
		SET balance = balance - $2, updated_at = now()
		WHERE id = $1 AND balance >= $2
		RETURNING balance`,
		customerID, amount).Scan(&balance)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrInsufficientCredits
	}
	if err != nil {
		return 0, fmt.Errorf("debit balance: %w", err)
	}
	return balance, nil
}

// ChargeResult is the outcome of a charge.
type ChargeResult struct {
	Transaction Transaction
	Balance     int64
	// Replayed is true when the client_ref was already used with the same
	// amount; no credits were added.
	Replayed bool
}

// Charge adds amount credits to the customer's balance, idempotently by
// clientRef. Repeating a charge with the same clientRef and amount returns the
// original transaction; a different amount returns ErrChargeConflict.
func Charge(ctx context.Context, tx pgx.Tx, customerID uuid.UUID, amount int64, clientRef string) (ChargeResult, error) {
	t := Transaction{
		ID:         store.NewID(),
		CustomerID: customerID,
		Kind:       KindCharge,
		Amount:     amount,
		ClientRef:  &clientRef,
	}
	err := tx.QueryRow(ctx, `
		INSERT INTO transactions (id, customer_id, kind, amount, client_ref)
		VALUES ($1, $2, 'charge', $3, $4)
		ON CONFLICT (customer_id, client_ref) WHERE kind = 'charge' DO NOTHING
		RETURNING created_at`,
		t.ID, customerID, amount, clientRef).Scan(&t.CreatedAt)

	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return replayCharge(ctx, tx, customerID, amount, clientRef)
	case store.IsForeignKeyViolation(err, ""):
		return ChargeResult{}, ErrCustomerNotFound
	case err != nil:
		return ChargeResult{}, fmt.Errorf("insert charge: %w", err)
	}

	var balance int64
	err = tx.QueryRow(ctx, `
		UPDATE customers SET balance = balance + $2, updated_at = now()
		WHERE id = $1
		RETURNING balance`,
		customerID, amount).Scan(&balance)
	if err != nil {
		return ChargeResult{}, fmt.Errorf("credit balance: %w", err)
	}
	return ChargeResult{Transaction: t, Balance: balance}, nil
}

// ChargeCustomer runs Charge in a transaction of its own.
func ChargeCustomer(ctx context.Context, pool *pgxpool.Pool, customerID uuid.UUID, amount int64, clientRef string) (ChargeResult, error) {
	var res ChargeResult
	err := store.WithTx(ctx, pool, func(tx pgx.Tx) error {
		var err error
		res, err = Charge(ctx, tx, customerID, amount, clientRef)
		return err
	})
	return res, err
}

func replayCharge(ctx context.Context, tx pgx.Tx, customerID uuid.UUID, amount int64, clientRef string) (ChargeResult, error) {
	rows, err := tx.Query(ctx, `
		SELECT `+transactionColumns+`
		FROM transactions
		WHERE customer_id = $1 AND kind = 'charge' AND client_ref = $2`,
		customerID, clientRef)
	if err != nil {
		return ChargeResult{}, fmt.Errorf("load charge: %w", err)
	}
	original, err := pgx.CollectExactlyOneRow(rows, scanTransaction)
	if err != nil {
		return ChargeResult{}, fmt.Errorf("load charge: %w", err)
	}
	if original.Amount != amount {
		return ChargeResult{}, ErrChargeConflict
	}
	balance, err := currentBalance(ctx, tx, customerID)
	if err != nil {
		return ChargeResult{}, err
	}
	return ChargeResult{Transaction: original, Balance: balance, Replayed: true}, nil
}

// Refund returns one message's cost to its customer.
type Refund struct {
	CustomerID uuid.UUID
	MessageID  uuid.UUID
	Amount     int64
}

// ApplyRefunds records refund transactions and credits the balances. A
// message that already has a refund is skipped, so applying the same refund
// twice has no effect. Balances are updated in ascending customer order so
// that concurrent callers cannot deadlock.
func ApplyRefunds(ctx context.Context, tx pgx.Tx, refunds []Refund) error {
	if len(refunds) == 0 {
		return nil
	}
	ids := make([]uuid.UUID, len(refunds))
	customers := make([]uuid.UUID, len(refunds))
	messages := make([]uuid.UUID, len(refunds))
	amounts := make([]int64, len(refunds))
	for i, r := range refunds {
		ids[i], customers[i], messages[i], amounts[i] = store.NewID(), r.CustomerID, r.MessageID, r.Amount
	}
	rows, err := tx.Query(ctx, `
		INSERT INTO transactions (id, customer_id, kind, amount, message_id)
		SELECT r.id, r.customer_id, 'refund', r.amount, r.message_id
		FROM unnest($1::uuid[], $2::uuid[], $3::bigint[], $4::uuid[]) AS r(id, customer_id, amount, message_id)
		ON CONFLICT (message_id, kind) WHERE message_id IS NOT NULL DO NOTHING
		RETURNING customer_id, amount`,
		ids, customers, amounts, messages)
	if err != nil {
		return fmt.Errorf("record refunds: %w", err)
	}
	totals := make(map[uuid.UUID]int64)
	var customerID uuid.UUID
	var amount int64
	if _, err := pgx.ForEachRow(rows, []any{&customerID, &amount}, func() error {
		totals[customerID] += amount
		return nil
	}); err != nil {
		return fmt.Errorf("record refunds: %w", err)
	}

	order := make([]uuid.UUID, 0, len(totals))
	for id := range totals {
		order = append(order, id)
	}
	sort.Slice(order, func(i, j int) bool { return order[i].String() < order[j].String() })
	for _, id := range order {
		if _, err := tx.Exec(ctx, `
			UPDATE customers SET balance = balance + $2, updated_at = now() WHERE id = $1`,
			id, totals[id]); err != nil {
			return fmt.Errorf("credit refund: %w", err)
		}
	}
	return nil
}

func currentBalance(ctx context.Context, q store.Querier, customerID uuid.UUID) (int64, error) {
	var balance int64
	err := q.QueryRow(ctx, `SELECT balance FROM customers WHERE id = $1`, customerID).Scan(&balance)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrCustomerNotFound
	}
	if err != nil {
		return 0, fmt.Errorf("load balance: %w", err)
	}
	return balance, nil
}
