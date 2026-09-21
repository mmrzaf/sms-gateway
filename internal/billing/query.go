package billing

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mmrzaf/sms-gatway/internal/store"
)

// Balance is a customer's current balance.
type Balance struct {
	Amount    int64
	UpdatedAt time.Time
}

// GetBalance returns the customer's balance.
func GetBalance(ctx context.Context, q store.Querier, customerID uuid.UUID) (Balance, error) {
	var b Balance
	err := q.QueryRow(ctx, `SELECT balance, updated_at FROM customers WHERE id = $1`, customerID).
		Scan(&b.Amount, &b.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Balance{}, ErrCustomerNotFound
	}
	if err != nil {
		return Balance{}, fmt.Errorf("load balance: %w", err)
	}
	return b, nil
}

// TransactionFilter selects transactions for ListTransactions. Zero values
// mean "no filter".
type TransactionFilter struct {
	Kind  Kind
	Since time.Time
	Until time.Time
	// After is the last ID of the previous page; uuid.Nil starts at the newest.
	After uuid.UUID
	Limit int
}

// ListTransactions returns a page of the customer's transactions, newest
// first, and whether more pages follow.
func ListTransactions(ctx context.Context, q store.Querier, customerID uuid.UUID, f TransactionFilter) ([]Transaction, bool, error) {
	rows, err := q.Query(ctx, `
		SELECT `+transactionColumns+`
		FROM transactions
		WHERE customer_id = $1
		  AND ($2::text IS NULL OR kind = $2)
		  AND ($3::uuid IS NULL OR id >= $3)
		  AND ($4::uuid IS NULL OR id < $4)
		  AND ($5::uuid IS NULL OR id < $5)
		ORDER BY id DESC
		LIMIT $6`,
		customerID, store.NullString(string(f.Kind)), store.NullLowerBound(f.Since), store.NullLowerBound(f.Until),
		store.NullID(f.After), f.Limit+1)
	if err != nil {
		return nil, false, fmt.Errorf("list transactions: %w", err)
	}
	list, err := pgx.CollectRows(rows, scanTransaction)
	if err != nil {
		return nil, false, fmt.Errorf("list transactions: %w", err)
	}
	more := len(list) > f.Limit
	if more {
		list = list[:f.Limit]
	}
	return list, more, nil
}

const transactionColumns = `id, customer_id, kind, amount, message_id, client_ref, created_at`

func scanTransaction(row pgx.CollectableRow) (Transaction, error) {
	var t Transaction
	err := row.Scan(&t.ID, &t.CustomerID, &t.Kind, &t.Amount, &t.MessageID, &t.ClientRef, &t.CreatedAt)
	return t, err
}
