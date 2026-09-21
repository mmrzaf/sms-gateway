// Package dispatch moves accepted messages from the queue to SMS providers.
//
// A worker runs two pools, one for Express and one for normal messages. Each
// pool claims batches of ready queue rows under a lease, sends every message
// on its own goroutine, and hands the outcome to the completer, which commits
// outcomes in batches. Provider failures are contained by per-provider
// circuit breakers, and provider capacity is shared through rate budgets that
// reserve a portion for Express.
package dispatch

import (
	"time"

	"github.com/mmrzaf/sms-gatway/internal/message"
)

// Policy holds the dispatch settings of one service class.
type Policy struct {
	Class       message.Type
	MaxAttempts int
	BackoffBase time.Duration
	BackoffMax  time.Duration
	Timeout     time.Duration
	// Rotate moves to the next provider on every retry (Express). Without it,
	// retries stay on the first usable provider in priority order (normal).
	Rotate bool
}

// Backoff returns the delay after the attempt-th failed attempt:
// d = min(base × 2^(attempt-1), max), and the delay is uniform in [d/2, d].
// The jitter spreads retries of messages that failed together.
func (p Policy) Backoff(attempt int, random func() float64) time.Duration {
	d := p.BackoffMax
	if attempt-1 < 32 {
		if exp := p.BackoffBase << (attempt - 1); exp > 0 && exp < d {
			d = exp
		}
	}
	return d/2 + time.Duration(random()*float64(d/2))
}
