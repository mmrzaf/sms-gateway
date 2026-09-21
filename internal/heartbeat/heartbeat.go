// Package heartbeat records that a worker process is alive, with a snapshot
// of its state, in the workers table.
package heartbeat

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Run upserts the worker's row every interval until ctx is cancelled, then
// deletes the row. snapshot supplies the stats document.
func Run(ctx context.Context, db *pgxpool.Pool, id, role string, interval time.Duration,
	snapshot func() map[string]any, logger *slog.Logger) {
	started := time.Now()
	beat := func() {
		stats, err := json.Marshal(snapshot())
		if err != nil {
			logger.Error("encode heartbeat", "error", err)
			return
		}
		if _, err := db.Exec(ctx, `
			INSERT INTO workers (id, role, started_at, last_seen, stats)
			VALUES ($1, $2, $3, now(), $4)
			ON CONFLICT (id) DO UPDATE SET last_seen = now(), stats = EXCLUDED.stats`,
			id, role, started, stats); err != nil && ctx.Err() == nil {
			logger.Warn("heartbeat failed", "error", err)
		}
	}

	beat()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			beat()
		case <-ctx.Done():
			cleanup, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			if _, err := db.Exec(cleanup, `DELETE FROM workers WHERE id = $1`, id); err != nil {
				logger.Warn("remove worker record", "error", err)
			}
			return
		}
	}
}
