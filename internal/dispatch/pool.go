package dispatch

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mmrzaf/sms-gatway/internal/metrics"
)

// claimErrorBackoff is the pause after a failed claim, so an unavailable
// database is not queried at poll speed.
const claimErrorBackoff = time.Second

// pool dispatches the messages of one service class with a fixed concurrency.
type pool struct {
	name        string
	policy      Policy
	lanes       []string
	concurrency int

	slots    chan struct{} // one token per send in progress
	wake     chan struct{} // signalled when new work may be ready
	active   atomic.Int64
	inFlight sync.WaitGroup
	next     int // next lane in the rotation
}

func newPool(name string, policy Policy, lanes []string, concurrency int) *pool {
	return &pool{
		name:        name,
		policy:      policy,
		lanes:       lanes,
		concurrency: concurrency,
		slots:       make(chan struct{}, concurrency),
		wake:        make(chan struct{}, 1),
	}
}

// signal wakes an idle pool without blocking.
func (p *pool) signal() {
	select {
	case p.wake <- struct{}{}:
	default:
	}
}

// runPool claims and dispatches until ctx is cancelled. Sends run under
// sendCtx, which outlives ctx so in-flight sends can finish during shutdown.
//
// Lanes are served round-robin: each pass claims from the next lane, so a
// backlog in one lane cannot delay the others by more than one rotation.
// Claims are sized by free concurrency and by the provider budget available
// in the next second, so claimed messages start well within their lease.
func (w *Worker) runPool(ctx, sendCtx context.Context, p *pool) {
	emptyLanes := 0
	for ctx.Err() == nil {
		if !w.anyUsable() {
			// Every provider's circuit is open: waiting costs nothing, while
			// claiming would only defer messages.
			sleep(ctx, time.Until(w.earliestProbe()))
			continue
		}

		// Wait for one free slot, then take as many more as are free.
		select {
		case p.slots <- struct{}{}:
		case <-ctx.Done():
			return
		}
		want := min(w.cfg.ClaimBatchSize, w.budgetAvailable(p.policy))
		if want < 1 {
			<-p.slots
			sleep(ctx, 10*time.Millisecond)
			continue
		}
		taken := 1
	take:
		for taken < want {
			select {
			case p.slots <- struct{}{}:
				taken++
			default:
				break take
			}
		}

		lane := p.lanes[p.next]
		p.next = (p.next + 1) % len(p.lanes)
		jobs, err := w.claim(ctx, lane, taken)
		for range taken - len(jobs) {
			<-p.slots
		}
		if err != nil {
			if ctx.Err() == nil {
				w.logger.Error("claim failed", "pool", p.name, "lane", lane, "error", err)
				sleep(ctx, max(w.cfg.PollInterval, claimErrorBackoff))
			}
			continue
		}

		if len(jobs) == 0 {
			emptyLanes++
			if emptyLanes >= len(p.lanes) {
				emptyLanes = 0
				p.idle(ctx, w.cfg.PollInterval)
			}
			continue
		}
		emptyLanes = 0
		metrics.ClaimSize.With(p.name).Observe(float64(len(jobs)))

		for _, j := range jobs {
			p.inFlight.Add(1)
			metrics.PoolInFlight.With(p.name).Set(float64(p.active.Add(1)))
			go func() {
				defer func() {
					metrics.PoolInFlight.With(p.name).Set(float64(p.active.Add(-1)))
					<-p.slots
					p.inFlight.Done()
				}()
				if o, ok := w.dispatch(sendCtx, p.policy, j); ok {
					w.completer.submit(o)
				}
			}()
		}
	}
}

// idle waits for the poll interval, a wake-up, or shutdown.
func (p *pool) idle(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
	case <-p.wake:
	case <-ctx.Done():
	}
}
