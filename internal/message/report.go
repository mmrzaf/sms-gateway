package message

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mmrzaf/sms-gatway/internal/store"
)

// MaxReportRange is the longest time range a summary may cover.
const MaxReportRange = 31 * 24 * time.Hour

// Summary aggregates a customer's messages accepted in a time range.
type Summary struct {
	Since           time.Time
	Until           time.Time
	Messages        int64
	Segments        int64
	CreditsSpent    int64
	CreditsRefunded int64
	ByStatus        map[Status]int64
	ByType          map[Type]TypeSummary
}

// TypeSummary aggregates one service class.
type TypeSummary struct {
	Messages    int64
	Credits     int64
	SLABreached int64
}

// Summarize aggregates the customer's messages accepted in [since, until).
// Every status and type is present in the result, with zero counts where
// there are no messages.
func (s *Service) Summarize(ctx context.Context, customerID uuid.UUID, since, until time.Time) (Summary, error) {
	sum := Summary{
		Since:    since,
		Until:    until,
		ByStatus: make(map[Status]int64, len(Statuses)),
		ByType:   map[Type]TypeSummary{Normal: {}, Express: {}},
	}
	for _, st := range Statuses {
		sum.ByStatus[st] = 0
	}

	rows, err := s.pool.Query(ctx, `
		SELECT m.type, m.status, count(*), sum(m.segments), sum(m.cost),
		       count(*) FILTER (WHERE m.sla_breached),
		       COALESCE(sum(r.amount), 0)
		FROM messages m
		LEFT JOIN transactions r ON r.message_id = m.id AND r.kind = 'refund'
		WHERE m.customer_id = $1 AND m.id >= $2 AND m.id < $3
		GROUP BY m.type, m.status`,
		customerID, store.LowerBound(since), store.LowerBound(until))
	if err != nil {
		return Summary{}, fmt.Errorf("summarize messages: %w", err)
	}
	var (
		typ, status                                  string
		count, segments, credits, breached, refunded int64
	)
	_, err = pgx.ForEachRow(rows, []any{&typ, &status, &count, &segments, &credits, &breached, &refunded}, func() error {
		sum.Messages += count
		sum.Segments += segments
		sum.CreditsSpent += credits
		sum.CreditsRefunded += refunded
		sum.ByStatus[Status(status)] += count

		ts := sum.ByType[Type(typ)]
		ts.Messages += count
		ts.Credits += credits
		ts.SLABreached += breached
		sum.ByType[Type(typ)] = ts
		return nil
	})
	if err != nil {
		return Summary{}, fmt.Errorf("summarize messages: %w", err)
	}
	return sum, nil
}
