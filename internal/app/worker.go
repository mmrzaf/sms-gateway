package app

import (
	"context"
	"net/http"

	"golang.org/x/sync/errgroup"

	"github.com/mmrzaf/sms-gatway/internal/httpx"
)

// startWorker runs the worker's components and its metrics and health server.
func startWorker(ctx context.Context, g *errgroup.Group, d deps) {
	serve(ctx, g, d, "worker", d.cfg.WorkerAddr, workerHandler(d))
}

func workerHandler(d deps) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", httpx.Healthz)
	mux.Handle("GET /readyz", httpx.Readyz(d.ready))
	mux.HandleFunc("/", httpx.NotFound)
	return mux
}
