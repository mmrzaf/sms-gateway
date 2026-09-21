package app

import (
	"context"
	"net/http"

	"golang.org/x/sync/errgroup"

	"github.com/mmrzaf/sms-gatway/internal/dispatch"
	"github.com/mmrzaf/sms-gatway/internal/heartbeat"
	"github.com/mmrzaf/sms-gatway/internal/httpx"
)

// startWorker runs the dispatcher, its heartbeat, and the worker's metrics
// and health server.
func startWorker(ctx context.Context, g *errgroup.Group, d deps) {
	w := dispatch.New(dispatch.FromGateway(d.cfg), d.pool, d.logger)
	g.Go(func() error {
		return w.Run(ctx)
	})
	g.Go(func() error {
		heartbeat.Run(ctx, d.pool, w.ID(), string(RoleWorker), d.cfg.Dispatch.HeartbeatInterval, w.Snapshot, d.logger)
		return nil
	})
	serve(ctx, g, d, "worker", d.cfg.WorkerAddr, workerHandler(d))
}

func workerHandler(d deps) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", httpx.Healthz)
	mux.Handle("GET /readyz", httpx.Readyz(d.ready))
	mux.HandleFunc("/", httpx.NotFound)
	return mux
}
