// Package customer manages customers (tenants): creation with an API key and
// initial credits, updates, key rotation, and deletion.
package customer

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mmrzaf/sms-gatway/internal/auth"
	"github.com/mmrzaf/sms-gatway/internal/billing"
	"github.com/mmrzaf/sms-gatway/internal/store"
)

// ErrNotFound means the customer does not exist.
var ErrNotFound = errors.New("customer not found")

// Customer is a tenant of the gateway.
type Customer struct {
	ID           uuid.UUID
	Name         string
	APIKeyPrefix string
	Balance      int64
	RateLimitRPS int
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// New describes a customer to create.
type New struct {
	Name           string
	InitialCredits int64
	RateLimitRPS   int
}

// Create inserts a customer with a new API key and, if InitialCredits is
// positive, a charge for those credits. It returns the customer and the full
// API key, which is not stored and cannot be retrieved again.
func Create(ctx context.Context, pool *pgxpool.Pool, n New) (Customer, string, error) {
	key := auth.GenerateKey()
	c := Customer{
		ID:           store.NewID(),
		Name:         n.Name,
		APIKeyPrefix: auth.DisplayPrefix(key),
		RateLimitRPS: n.RateLimitRPS,
	}
	err := store.WithTx(ctx, pool, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `
			INSERT INTO customers (id, name, api_key_hash, api_key_prefix, rate_limit_rps)
			VALUES ($1, $2, $3, $4, $5)
			RETURNING created_at, updated_at`,
			c.ID, c.Name, auth.HashKey(key), c.APIKeyPrefix, c.RateLimitRPS).
			Scan(&c.CreatedAt, &c.UpdatedAt)
		if err != nil {
			return fmt.Errorf("insert customer: %w", err)
		}
		if n.InitialCredits > 0 {
			res, err := billing.Charge(ctx, tx, c.ID, n.InitialCredits, AdminChargeRef())
			if err != nil {
				return err
			}
			c.Balance = res.Balance
		}
		return nil
	})
	if err != nil {
		return Customer{}, "", err
	}
	return c, key, nil
}

// AdminChargeRef returns a unique client_ref for a charge made by an operator.
func AdminChargeRef() string {
	return "admin-" + store.NewID().String()
}

const columns = `id, name, api_key_prefix, balance, rate_limit_rps, created_at, updated_at`

func scan(row pgx.CollectableRow) (Customer, error) {
	var c Customer
	err := row.Scan(&c.ID, &c.Name, &c.APIKeyPrefix, &c.Balance, &c.RateLimitRPS, &c.CreatedAt, &c.UpdatedAt)
	return c, err
}

func one(rows pgx.Rows, err error) (Customer, error) {
	if err != nil {
		return Customer{}, err
	}
	c, err := pgx.CollectExactlyOneRow(rows, scan)
	if errors.Is(err, pgx.ErrNoRows) {
		return Customer{}, ErrNotFound
	}
	return c, err
}

// Get returns a customer by ID.
func Get(ctx context.Context, q store.Querier, id uuid.UUID) (Customer, error) {
	return one(q.Query(ctx, `SELECT `+columns+` FROM customers WHERE id = $1`, id))
}

// FindByName returns the oldest customer with the given name.
func FindByName(ctx context.Context, q store.Querier, name string) (Customer, error) {
	return one(q.Query(ctx, `SELECT `+columns+` FROM customers WHERE name = $1 ORDER BY id LIMIT 1`, name))
}

// List returns a page of customers, newest first, and whether more follow.
// after is the last ID of the previous page; uuid.Nil starts at the newest.
func List(ctx context.Context, q store.Querier, after uuid.UUID, limit int) ([]Customer, bool, error) {
	rows, err := q.Query(ctx, `
		SELECT `+columns+` FROM customers
		WHERE ($1::uuid IS NULL OR id < $1)
		ORDER BY id DESC
		LIMIT $2`,
		store.NullID(after), limit+1)
	if err != nil {
		return nil, false, fmt.Errorf("list customers: %w", err)
	}
	list, err := pgx.CollectRows(rows, scan)
	if err != nil {
		return nil, false, fmt.Errorf("list customers: %w", err)
	}
	more := len(list) > limit
	if more {
		list = list[:limit]
	}
	return list, more, nil
}

// Update changes a customer's name and rate limit. Nil fields are unchanged.
// The balance is never edited directly; credits are added with a charge.
func Update(ctx context.Context, q store.Querier, id uuid.UUID, name *string, rateLimitRPS *int) (Customer, error) {
	return one(q.Query(ctx, `
		UPDATE customers
		SET name = COALESCE($2, name),
		    rate_limit_rps = COALESCE($3, rate_limit_rps),
		    updated_at = now()
		WHERE id = $1
		RETURNING `+columns,
		id, name, rateLimitRPS))
}

// Delete removes a customer with its messages, transactions, and queue rows.
func Delete(ctx context.Context, q store.Querier, id uuid.UUID) error {
	tag, err := q.Exec(ctx, `DELETE FROM customers WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("delete customer: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// RotateKey replaces the customer's API key and returns the new one.
func RotateKey(ctx context.Context, q store.Querier, id uuid.UUID) (Customer, string, error) {
	key := auth.GenerateKey()
	c, err := one(q.Query(ctx, `
		UPDATE customers
		SET api_key_hash = $2, api_key_prefix = $3, updated_at = now()
		WHERE id = $1
		RETURNING `+columns,
		id, auth.HashKey(key), auth.DisplayPrefix(key)))
	if err != nil {
		return Customer{}, "", err
	}
	return c, key, nil
}
