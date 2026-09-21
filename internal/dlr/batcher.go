package dlr

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mmrzaf/sms-gatway/internal/metrics"
)

// ErrStopped is returned for reports submitted after the batcher stopped.
var ErrStopped = errors.New("delivery report batcher stopped")

// Batcher is an in-process group commit: concurrent callbacks share one
// transaction, and each caller is released only after its report is durable.
type Batcher struct {
	db       *pgxpool.Pool
	size     int
	interval time.Duration
	logger   *slog.Logger

	in      chan pending
	stopped chan struct{} // closed when shutdown begins
	done    chan struct{} // closed when Run has returned
}

type pending struct {
	report Report
	result chan result
}

type result struct {
	outcome Outcome
	err     error
}

// NewBatcher returns a batcher that commits up to size reports at a time, at
// least every interval.
func NewBatcher(db *pgxpool.Pool, size int, interval time.Duration, logger *slog.Logger) *Batcher {
	return &Batcher{
		db:       db,
		size:     size,
		interval: interval,
		logger:   logger,
		in:       make(chan pending, size),
		stopped:  make(chan struct{}),
		done:     make(chan struct{}),
	}
}

// Submit queues a report and waits until its batch commits or ctx is done.
func (b *Batcher) Submit(ctx context.Context, r Report) (Outcome, error) {
	select {
	case <-b.stopped:
		return "", ErrStopped
	default:
	}
	p := pending{report: r, result: make(chan result, 1)}
	select {
	case b.in <- p:
	case <-b.stopped:
		return "", ErrStopped
	case <-ctx.Done():
		return "", ctx.Err()
	}
	select {
	case res := <-p.result:
		return res.outcome, res.err
	case <-b.done:
		// The report may have been in the final batch.
		select {
		case res := <-p.result:
			return res.outcome, res.err
		default:
			return "", ErrStopped
		}
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

// Run commits batches until ctx is cancelled, then commits what is pending.
func (b *Batcher) Run(ctx context.Context) {
	defer close(b.done)
	batch := make([]pending, 0, b.size)
	timer := time.NewTimer(time.Hour)
	timer.Stop()
	flush := func() {
		timer.Stop()
		if len(batch) > 0 {
			b.commit(batch)
			batch = batch[:0]
		}
	}
	for {
		select {
		case p := <-b.in:
			if len(batch) == 0 {
				timer.Reset(b.interval)
			}
			batch = append(batch, p)
			if len(batch) >= b.size {
				flush()
			}
		case <-timer.C:
			flush()
		case <-ctx.Done():
			close(b.stopped)
			for {
				select {
				case p := <-b.in:
					batch = append(batch, p)
				default:
					flush()
					return
				}
			}
		}
	}
}

func (b *Batcher) commit(batch []pending) {
	reports := make([]Report, len(batch))
	for i, p := range batch {
		reports[i] = p.report
	}
	outcomes, err := Apply(context.Background(), b.db, reports)
	if err == nil {
		metrics.DLRBatchSize.Observe(float64(len(batch)))
	} else {
		b.logger.Warn("delivery report batch failed", "reports", len(batch), "error", err)
	}
	for i, p := range batch {
		if err != nil {
			p.result <- result{err: err}
		} else {
			p.result <- result{outcome: outcomes[i]}
		}
	}
}
