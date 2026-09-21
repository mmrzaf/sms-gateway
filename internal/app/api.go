package app

import (
	"context"
	"net/http"

	"golang.org/x/sync/errgroup"

	"github.com/mmrzaf/sms-gatway/internal/httpx"
)

// startAPI runs the public server (customer API) and the admin server
// (dashboard, admin API, DLR intake, metrics).
func startAPI(ctx context.Context, g *errgroup.Group, d deps) {
	serve(ctx, g, d, "public", d.cfg.HTTPAddr, publicHandler(d))
	serve(ctx, g, d, "admin", d.cfg.AdminAddr, adminHandler(d))
}

func publicHandler(d deps) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", httpx.Healthz)
	mux.Handle("GET /readyz", httpx.Readyz(d.ready))
	mux.HandleFunc("/", httpx.NotFound)
	return mux
}

func adminHandler(d deps) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", httpx.Healthz)
	mux.Handle("GET /readyz", httpx.Readyz(d.ready))
	mux.HandleFunc("/", httpx.NotFound)
	return mux
}
