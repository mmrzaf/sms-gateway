// Package ratelimit enforces per-customer message rate limits with token
// buckets held in process memory.
//
// With several API instances, each instance enforces an equal share of every
// customer's limit, so the shares add up to the configured rate when load
// balancing is even.
package ratelimit

import (
	"math"
	"sync"
	"time"

	"github.com/google/uuid"
	"golang.org/x/time/rate"
)

// MinBurst is the smallest bucket capacity. It admits a maximum-size batch
// from a full bucket regardless of the customer's rate.
const MinBurst = 500

// idleAfter is how long an unused bucket is kept before it is dropped.
const idleAfter = 10 * time.Minute

// Decision is the outcome of a rate-limit check.
type Decision struct {
	Allowed bool
	// Limit is the customer's configured rate in messages per second.
	Limit int
	// Remaining is the number of messages the bucket admits right now.
	Remaining int
	// RetryAfter is how long to wait before the request would be admitted.
	RetryAfter time.Duration
}

// Limiter holds one token bucket per customer.
type Limiter struct {
	instances int
	now       func() time.Time

	mu        sync.Mutex
	buckets   map[uuid.UUID]*bucket
	lastSweep time.Time
}

type bucket struct {
	limiter  *rate.Limiter
	rps      int
	lastUsed time.Time
}

// New returns a Limiter for a deployment with the given number of API instances.
func New(instances int) *Limiter {
	return &Limiter{
		instances: max(instances, 1),
		now:       time.Now,
		buckets:   make(map[uuid.UUID]*bucket),
	}
}

// Allow takes n tokens from the customer's bucket if it holds them. rps is
// the customer's current limit; a changed limit takes effect immediately.
func (l *Limiter) Allow(customerID uuid.UUID, rps, n int) Decision {
	now := l.now()

	l.mu.Lock()
	defer l.mu.Unlock()
	l.sweepLocked(now)

	b := l.bucketLocked(customerID, rps, now)
	b.lastUsed = now

	d := Decision{Limit: rps}
	if b.limiter.AllowN(now, n) {
		d.Allowed = true
	} else {
		r := b.limiter.ReserveN(now, n)
		if r.OK() {
			d.RetryAfter = r.DelayFrom(now)
			r.CancelAt(now)
		} else {
			// n exceeds the bucket capacity and can never be admitted at once.
			d.RetryAfter = time.Duration(math.MaxInt64)
		}
	}
	d.Remaining = max(int(b.limiter.TokensAt(now)), 0)
	return d
}

func (l *Limiter) bucketLocked(customerID uuid.UUID, rps int, now time.Time) *bucket {
	perInstance := float64(rps) / float64(l.instances)
	burst := max(int(math.Ceil(2*perInstance)), MinBurst)

	b, ok := l.buckets[customerID]
	if !ok {
		b = &bucket{limiter: rate.NewLimiter(rate.Limit(perInstance), burst), rps: rps}
		l.buckets[customerID] = b
		return b
	}
	if b.rps != rps {
		b.limiter.SetLimitAt(now, rate.Limit(perInstance))
		b.limiter.SetBurstAt(now, burst)
		b.rps = rps
	}
	return b
}

// sweepLocked drops buckets that have not been used recently. An idle bucket
// has refilled completely, so dropping it loses no state.
func (l *Limiter) sweepLocked(now time.Time) {
	if now.Sub(l.lastSweep) < idleAfter {
		return
	}
	for id, b := range l.buckets {
		if now.Sub(b.lastUsed) > idleAfter {
			delete(l.buckets, id)
		}
	}
	l.lastSweep = now
}
