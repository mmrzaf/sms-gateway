package testutil

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mmrzaf/sms-gatway/internal/customer"
)

// Customer creates a customer with the given credits and a rate limit high
// enough not to interfere with tests. It returns the customer and its API key.
func Customer(t testing.TB, pool *pgxpool.Pool, credits int64) (customer.Customer, string) {
	t.Helper()
	c, key, err := customer.Create(context.Background(), pool, customer.New{
		Name: "test-" + randomHex(4), InitialCredits: credits, RateLimitRPS: 100_000,
	})
	if err != nil {
		t.Fatalf("create customer: %v", err)
	}
	return c, key
}

// AssertLedgerConsistent fails the test if any balance differs from the sum
// of its transactions, or if any message lacks exactly one debit equal to its
// cost. Tests call it after exercising the credit paths.
func AssertLedgerConsistent(t testing.TB, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	var badBalances, badDebits int
	err := pool.QueryRow(ctx, `
		SELECT count(*) FROM (
			SELECT c.id FROM customers c
			LEFT JOIN transactions t ON t.customer_id = c.id
			GROUP BY c.id, c.balance
			HAVING c.balance <> COALESCE(sum(t.amount), 0)
		) AS v`).Scan(&badBalances)
	if err != nil {
		t.Fatalf("check balances: %v", err)
	}
	err = pool.QueryRow(ctx, `
		SELECT count(*) FROM messages m
		WHERE (SELECT count(*) FROM transactions t
		       WHERE t.message_id = m.id AND t.kind = 'debit' AND t.amount = -m.cost) <> 1`).Scan(&badDebits)
	if err != nil {
		t.Fatalf("check debits: %v", err)
	}
	if badBalances > 0 || badDebits > 0 {
		t.Errorf("ledger inconsistent: %d balances differ from their transactions, %d messages without exactly one debit",
			badBalances, badDebits)
	}
}
