package store

import (
	"errors"

	"github.com/jackc/pgx/v5/pgconn"
)

// codeForeignKeyViolation is the PostgreSQL error code for a foreign-key violation.
const codeForeignKeyViolation = "23503"

// IsForeignKeyViolation reports whether err is a foreign-key violation on the
// named constraint. An empty name matches any foreign-key violation.
func IsForeignKeyViolation(err error, constraint string) bool {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != codeForeignKeyViolation {
		return false
	}
	return constraint == "" || pgErr.ConstraintName == constraint
}
