package dispatch

import (
	"context"
	"time"

	"github.com/mmrzaf/sms-gatway/internal/message"
)

// listen wakes the pool whenever the accept transaction signals that Express
// messages were enqueued, so Express does not wait for the poll interval.
// LISTEN needs a session, so it holds one connection for its lifetime.
func (w *Worker) listen(ctx context.Context, p *pool) {
	for ctx.Err() == nil {
		if err := w.listenOnce(ctx, p); err != nil && ctx.Err() == nil {
			w.logger.Warn("notification listener failed; polling continues", "error", err)
			sleep(ctx, time.Second)
		}
	}
}

func (w *Worker) listenOnce(ctx context.Context, p *pool) error {
	conn, err := w.db.Acquire(ctx)
	if err != nil {
		return err
	}
	// A listening session must not be returned to the pool.
	c := conn.Hijack()
	defer c.Close(context.Background())

	if _, err := c.Exec(ctx, "LISTEN "+message.ExpressChannel); err != nil {
		return err
	}
	for {
		if _, err := c.WaitForNotification(ctx); err != nil {
			return err
		}
		p.signal()
	}
}
