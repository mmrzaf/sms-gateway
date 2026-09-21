package store

import (
	"errors"

	"github.com/jackc/pgx/v5/pgconn"
)

// PostgreSQL error codes used by the application.
const (
	codeUniqueViolation     = "23505"
	codeCheckViolation      = "23514"
	codeForeignKeyViolation = "23503"
)

// IsUniqueViolation reports whether err is a unique-constraint violation on
// the named constraint or index. An empty name matches any unique violation.
func IsUniqueViolation(err error, constraint string) bool {
	return hasCode(err, codeUniqueViolation, constraint)
}

// IsCheckViolation reports whether err is a check-constraint violation on the
// named constraint. An empty name matches any check violation.
func IsCheckViolation(err error, constraint string) bool {
	return hasCode(err, codeCheckViolation, constraint)
}

// IsForeignKeyViolation reports whether err is a foreign-key violation on the
// named constraint. An empty name matches any foreign-key violation.
func IsForeignKeyViolation(err error, constraint string) bool {
	return hasCode(err, codeForeignKeyViolation, constraint)
}

func hasCode(err error, code, constraint string) bool {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != code {
		return false
	}
	return constraint == "" || pgErr.ConstraintName == constraint
}
