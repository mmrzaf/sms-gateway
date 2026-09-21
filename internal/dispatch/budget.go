package dispatch

import (
	"context"
	"math"
	"time"

	"golang.org/x/time/rate"
)

// budget is a worker's send rate toward one provider, split into a shared
// bucket and a bucket reserved for Express. Express takes a reserved token
// when one is available and otherwise whichever bucket grants one sooner;
// normal traffic uses only the shared bucket. Under full normal load Express
// therefore keeps at least the reserved share.
type budget struct {
	shared   *rate.Limiter
	reserved *rate.Limiter // nil when nothing is reserved
}

func newBudget(perSecond int, reservedRatio float64) *budget {
	reservedRate := float64(perSecond) * reservedRatio
	sharedRate := float64(perSecond) - reservedRate
	b := &budget{shared: rate.NewLimiter(rate.Limit(sharedRate), max(int(math.Ceil(sharedRate)), 1))}
	if reservedRate > 0 {
		b.reserved = rate.NewLimiter(rate.Limit(reservedRate), max(int(math.Ceil(reservedRate)), 1))
	}
	return b
}

// wait blocks until a token is available for the class or ctx is done.
func (b *budget) wait(ctx context.Context, express bool) error {
	if !express || b.reserved == nil {
		return b.shared.Wait(ctx)
	}
	if b.reserved.Allow() {
		return nil
	}
	now := time.Now()
	r, s := b.reserved.ReserveN(now, 1), b.shared.ReserveN(now, 1)
	chosen, other := r, s
	if s.DelayFrom(now) < r.DelayFrom(now) {
		chosen, other = s, r
	}
	other.CancelAt(now)
	if !sleep(ctx, chosen.DelayFrom(now)) {
		chosen.Cancel()
		return ctx.Err()
	}
	return nil
}

// available estimates how many messages of the class can start within the
// next second. Pools size their claims with it, so claimed messages start
// well within their lease.
func (b *budget) available(express bool) int {
	now := time.Now()
	n := tokensWithin(b.shared, now)
	if express && b.reserved != nil {
		n += tokensWithin(b.reserved, now)
	}
	return n
}

func tokensWithin(l *rate.Limiter, now time.Time) int {
	t := l.TokensAt(now) + float64(l.Limit())
	return max(int(math.Min(t, float64(l.Burst()))), 0)
}

// sleep waits for d or until ctx is done, and reports whether d elapsed.
func sleep(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return ctx.Err() == nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-ctx.Done():
		return false
	}
}
