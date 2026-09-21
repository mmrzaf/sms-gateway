// Package dlr receives delivery reports from providers and applies them to
// messages. Reports are committed in batches, and each provider callback is
// acknowledged only after its report's batch has committed.
package dlr

import (
	"context"
	"fmt"
	"slices"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mmrzaf/sms-gatway/internal/store"
)

// Report is one delivery report.
type Report struct {
	MessageID   uuid.UUID
	Provider    string
	ProviderRef string
	// Status is "delivered" or "undelivered".
	Status string
}

// Outcome describes what applying a report did.
type Outcome string

// Report outcomes. None of them would change by resending the report, so all
// are acknowledged.
const (
	// Applied means the message moved to the reported status.
	Applied Outcome = "applied"
	// Duplicate means the message already had the reported status.
	Duplicate Outcome = "duplicate"
	// IgnoredTerminal means the message is in another terminal status, for
	// example failed after every response from the provider was lost.
	IgnoredTerminal Outcome = "ignored_terminal"
	// Unknown means no such message exists, for example because its customer
	// was deleted.
	Unknown Outcome = "unknown"
)

// Apply applies reports in one transaction and returns an outcome per report.
// A message may move from accepted as well as from sent: a report can arrive
// before the worker's own record of the provider's acceptance has committed.
// When a batch holds several reports for one message, the first one wins.
func Apply(ctx context.Context, db *pgxpool.Pool, reports []Report) ([]Outcome, error) {
	first := make(map[uuid.UUID]int, len(reports))
	var unique []Report
	for i, r := range reports {
		if _, seen := first[r.MessageID]; !seen {
			first[r.MessageID] = i
			unique = append(unique, r)
		}
	}
	// Lock rows in ID order, as the dispatch completer does.
	slices.SortFunc(unique, func(a, b Report) int { return compareIDs(a.MessageID, b.MessageID) })

	n := len(unique)
	ids, statuses, providers, refs := make([]uuid.UUID, n), make([]string, n), make([]string, n), make([]string, n)
	for i, r := range unique {
		ids[i], statuses[i], providers[i], refs[i] = r.MessageID, r.Status, r.Provider, r.ProviderRef
	}

	applied := make(map[uuid.UUID]bool, n)
	current := make(map[uuid.UUID]string, n)
	err := store.WithTx(ctx, db, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			UPDATE messages m
			SET status = r.status,
			    completed_at = now(),
			    provider = COALESCE(m.provider, r.provider),
			    provider_ref = COALESCE(m.provider_ref, r.provider_ref),
			    updated_at = now()
			FROM unnest($1::uuid[], $2::text[], $3::text[], $4::text[]) AS r(id, status, provider, provider_ref)
			WHERE m.id = r.id AND m.status IN ('accepted', 'sent')
			RETURNING m.id`,
			ids, statuses, providers, refs)
		if err != nil {
			return fmt.Errorf("apply delivery reports: %w", err)
		}
		updated, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
		if err != nil {
			return fmt.Errorf("apply delivery reports: %w", err)
		}
		for _, id := range updated {
			applied[id] = true
		}
		if len(updated) == n {
			return nil
		}

		// Classify the rest, for metrics and logs only.
		var rest []uuid.UUID
		for _, id := range ids {
			if !applied[id] {
				rest = append(rest, id)
			}
		}
		rows, err = tx.Query(ctx, `SELECT id, status FROM messages WHERE id = ANY($1::uuid[])`, rest)
		if err != nil {
			return fmt.Errorf("classify delivery reports: %w", err)
		}
		var id uuid.UUID
		var status string
		_, err = pgx.ForEachRow(rows, []any{&id, &status}, func() error {
			current[id] = status
			return nil
		})
		return err
	})
	if err != nil {
		return nil, err
	}

	out := make([]Outcome, len(reports))
	for i, r := range reports {
		switch status, known := current[r.MessageID]; {
		case applied[r.MessageID] && first[r.MessageID] == i:
			out[i] = Applied
		case applied[r.MessageID]:
			out[i] = Duplicate
		case !known:
			out[i] = Unknown
		case status == r.Status:
			out[i] = Duplicate
		default:
			out[i] = IgnoredTerminal
		}
	}
	return out, nil
}

func compareIDs(a, b uuid.UUID) int {
	for i := range a {
		if a[i] != b[i] {
			return int(a[i]) - int(b[i])
		}
	}
	return 0
}
