// Package monitor answers operational questions about the running system:
// queue depth per lane, live workers, throughput, and Express latency. The
// admin API and the sweeper's metric sampling use it.
package monitor

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mmrzaf/sms-gatway/internal/store"
)

// StaleAfter is how long a worker may go without a heartbeat before it is
// shown as stale.
const StaleAfter = 15 * time.Second

// LaneStats describes one queue lane.
type LaneStats struct {
	Lane     string `json:"lane"`
	Ready    int64  `json:"ready"`
	Delayed  int64  `json:"delayed"`
	InFlight int64  `json:"in_flight"`
	// OldestReadyAge is how long the oldest ready row has been ready, in seconds.
	OldestReadyAge float64 `json:"oldest_ready_age_s"`
}

// QueueStats counts rows per lane and state. Every lane in lanes is present,
// with zeros if it has no rows; other lanes found in the queue are appended.
func QueueStats(ctx context.Context, q store.Querier, lanes []string) ([]LaneStats, error) {
	rows, err := q.Query(ctx, `
		SELECT lane,
		       count(*) FILTER (WHERE next_attempt_at <= now()),
		       count(*) FILTER (WHERE lease_owner IS NULL AND next_attempt_at > now()),
		       count(*) FILTER (WHERE lease_owner IS NOT NULL AND next_attempt_at > now()),
		       COALESCE(EXTRACT(EPOCH FROM now() - min(next_attempt_at) FILTER (WHERE next_attempt_at <= now())), 0)
		FROM queue
		GROUP BY lane`)
	if err != nil {
		return nil, fmt.Errorf("queue stats: %w", err)
	}
	found := make(map[string]LaneStats)
	var s LaneStats
	if _, err := pgx.ForEachRow(rows, []any{&s.Lane, &s.Ready, &s.Delayed, &s.InFlight, &s.OldestReadyAge}, func() error {
		found[s.Lane] = s
		return nil
	}); err != nil {
		return nil, fmt.Errorf("queue stats: %w", err)
	}

	out := make([]LaneStats, 0, len(lanes)+len(found))
	for _, lane := range lanes {
		st := found[lane]
		st.Lane = lane
		out = append(out, st)
		delete(found, lane)
	}
	var extra []string
	for lane := range found {
		extra = append(extra, lane)
	}
	sort.Strings(extra)
	for _, lane := range extra {
		out = append(out, found[lane])
	}
	return out, nil
}

// Worker is a worker process as recorded by its heartbeat.
type Worker struct {
	ID        string          `json:"id"`
	Role      string          `json:"role"`
	StartedAt time.Time       `json:"started_at"`
	LastSeen  time.Time       `json:"last_seen"`
	Stale     bool            `json:"stale"`
	Stats     json.RawMessage `json:"stats"`
}

// Workers lists worker records, most recently started first.
func Workers(ctx context.Context, q store.Querier) ([]Worker, error) {
	rows, err := q.Query(ctx, `
		SELECT id, role, started_at, last_seen, now() - last_seen > $1 * interval '1 millisecond', stats
		FROM workers ORDER BY started_at DESC`, StaleAfter.Milliseconds())
	if err != nil {
		return nil, fmt.Errorf("list workers: %w", err)
	}
	list, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (Worker, error) {
		var w Worker
		err := row.Scan(&w.ID, &w.Role, &w.StartedAt, &w.LastSeen, &w.Stale, &w.Stats)
		return w, err
	})
	if err != nil {
		return nil, fmt.Errorf("list workers: %w", err)
	}
	return list, nil
}

// AcceptedPerSecond is the acceptance rate over the last minute. It counts
// messages by the time embedded in their UUIDv7 primary key.
func AcceptedPerSecond(ctx context.Context, q store.Querier, now time.Time) (float64, error) {
	var n int64
	if err := q.QueryRow(ctx, `SELECT count(*) FROM messages WHERE id >= $1`,
		store.LowerBound(now.Add(-time.Minute))).Scan(&n); err != nil {
		return 0, fmt.Errorf("throughput: %w", err)
	}
	return float64(n) / 60, nil
}

// ExpressLatency summarizes Express messages accepted within the window:
// percentiles of acceptance-to-sent latency and the number of SLA breaches.
type ExpressLatency struct {
	Window      string  `json:"window"`
	P50         float64 `json:"p50"`
	P95         float64 `json:"p95"`
	P99         float64 `json:"p99"`
	SLABreaches int64   `json:"sla_breaches"`
}

// Express computes ExpressLatency over the window ending now.
func Express(ctx context.Context, q store.Querier, now time.Time, window time.Duration) (ExpressLatency, error) {
	out := ExpressLatency{Window: window.String()}
	var p50, p95, p99 *float64
	err := q.QueryRow(ctx, `
		SELECT percentile_cont(0.50) WITHIN GROUP (ORDER BY lat) FILTER (WHERE lat IS NOT NULL),
		       percentile_cont(0.95) WITHIN GROUP (ORDER BY lat) FILTER (WHERE lat IS NOT NULL),
		       percentile_cont(0.99) WITHIN GROUP (ORDER BY lat) FILTER (WHERE lat IS NOT NULL),
		       count(*) FILTER (WHERE sla_breached)
		FROM (
		    SELECT EXTRACT(EPOCH FROM sent_at - accepted_at)::float8 AS lat, sla_breached
		    FROM messages
		    WHERE id >= $1 AND type = 'express'
		) AS e`,
		store.LowerBound(now.Add(-window))).Scan(&p50, &p95, &p99, &out.SLABreaches)
	if err != nil {
		return ExpressLatency{}, fmt.Errorf("express latency: %w", err)
	}
	for dst, v := range map[*float64]*float64{&out.P50: p50, &out.P95: p95, &out.P99: p99} {
		if v != nil {
			*dst = *v
		}
	}
	return out, nil
}
