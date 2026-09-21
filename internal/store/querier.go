package store

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Querier is satisfied by *pgxpool.Pool, *pgxpool.Conn, and pgx.Tx, so query
// functions can run either on their own or inside a caller's transaction.
type Querier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// IsUnavailable reports whether err means the database could not be reached
// or did not answer in time, as opposed to rejecting the request.
func IsUnavailable(err error) bool {
	var connectErr *pgconn.ConnectError
	return errors.As(err, &connectErr) || pgconn.Timeout(err)
}
