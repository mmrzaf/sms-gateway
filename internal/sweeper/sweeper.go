// Package sweeper performs periodic queue maintenance in worker processes:
// it expires and refunds messages whose TTL passed while no worker could
// claim them, flags Express SLA breaches while they happen, removes orphaned
// queue rows, moves rows out of lanes that no longer exist, and deletes
// stale worker records.
//
// Every worker runs the sweeper, but each task starts its transaction by
// taking a transaction-scoped advisory lock, so at any moment only one
// worker performs a given task and the others skip it for that cycle.
package sweeper

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mmrzaf/sms-gatway/internal/store"
)

// Config configures the sweeper.
type Config struct {
	Interval    time.Duration
	ExpressSLA  time.Duration
	NormalLanes int
	// BatchSize bounds the rows each task handles per cycle.
	BatchSize int
}

// DefaultBatchSize is the per-task row limit per cycle.
const DefaultBatchSize = 1000

// staleWorkerAge is how long a worker may go without a heartbeat before its
// record is deleted.
const staleWorkerAge = time.Hour

// Result counts the rows each task handled in one sweep.
type Result struct {
	Expired    int
	Flagged    int
	Orphans    int
	Reassigned int
	Workers    int
}

// Sweeper runs the maintenance tasks.
type Sweeper struct {
	db     *pgxpool.Pool
	cfg    Config
	logger *slog.Logger
}

// New returns a sweeper.
func New(cfg Config, db *pgxpool.Pool, logger *slog.Logger) *Sweeper {
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = DefaultBatchSize
	}
	return &Sweeper{db: db, cfg: cfg, logger: logger}
}

// Run sweeps every interval until ctx is cancelled.
func (s *Sweeper) Run(ctx context.Context) {
	ticker := time.NewTicker(s.cfg.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			res, err := s.Sweep(ctx)
			if err != nil && ctx.Err() == nil {
				s.logger.Error("sweep failed", "error", err)
			}
			if res != (Result{}) {
				s.logger.Info("sweep", "expired", res.Expired, "sla_flagged", res.Flagged,
					"orphans", res.Orphans, "lanes_reassigned", res.Reassigned, "workers_removed", res.Workers)
			}
		}
	}
}

// task identifies a sweeper task; its value is the advisory lock key.
type task int64

const (
	taskExpire task = 7_300_001 + iota
	taskFlagSLA
	taskOrphans
	taskLanes
	taskWorkers
)

var taskNames = map[task]string{
	taskExpire:  "expire",
	taskFlagSLA: "flag_sla",
	taskOrphans: "orphans",
	taskLanes:   "lanes",
	taskWorkers: "workers",
}

// Sweep runs every task once and reports what each did. A task whose lock is
// held by another worker is skipped and counts zero.
func (s *Sweeper) Sweep(ctx context.Context) (Result, error) {
	var res Result
	steps := []struct {
		task task
		run  func(context.Context, pgx.Tx) (int, error)
		dst  *int
	}{
		{taskExpire, s.expire, &res.Expired},
		{taskFlagSLA, s.flagSLA, &res.Flagged},
		{taskOrphans, s.removeOrphans, &res.Orphans},
		{taskLanes, s.reassignLanes, &res.Reassigned},
		{taskWorkers, s.removeStaleWorkers, &res.Workers},
	}
	for _, step := range steps {
		n, err := s.locked(ctx, step.task, step.run)
		if err != nil {
			return res, fmt.Errorf("sweeper task %s: %w", taskNames[step.task], err)
		}
		*step.dst = n
	}
	return res, nil
}

// locked runs fn in a transaction that holds the task's advisory lock, or
// does nothing if another transaction holds it.
func (s *Sweeper) locked(ctx context.Context, t task, fn func(context.Context, pgx.Tx) (int, error)) (int, error) {
	var n int
	err := store.WithTx(ctx, s.db, func(tx pgx.Tx) error {
		var acquired bool
		if err := tx.QueryRow(ctx, `SELECT pg_try_advisory_xact_lock($1)`, int64(t)).Scan(&acquired); err != nil {
			return err
		}
		if !acquired {
			return nil
		}
		var err error
		n, err = fn(ctx, tx)
		return err
	})
	return n, err
}
