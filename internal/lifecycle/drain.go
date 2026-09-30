// Package lifecycle bounds work that may finish after shutdown begins.
package lifecycle

import (
	"context"
	"time"
)

// DrainContext preserves parent values and allows work to finish for timeout
// after parent is cancelled. Cancel must be called when the work has stopped.
func DrainContext(parent context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(context.WithoutCancel(parent))
	stop := context.AfterFunc(parent, func() {
		timer := time.NewTimer(timeout)
		defer timer.Stop()
		select {
		case <-timer.C:
			cancel()
		case <-ctx.Done():
		}
	})
	return ctx, func() { stop(); cancel() }
}
