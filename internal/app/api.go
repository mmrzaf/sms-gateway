package app

import (
	"context"
	"net/http"

	"golang.org/x/sync/errgroup"

	"github.com/mmrzaf/sms-gatway/internal/admin"
	"github.com/mmrzaf/sms-gatway/internal/api"
	"github.com/mmrzaf/sms-gatway/internal/auth"
	"github.com/mmrzaf/sms-gatway/internal/dlr"
	"github.com/mmrzaf/sms-gatway/internal/httpx"
	"github.com/mmrzaf/sms-gatway/internal/invariant"
	"github.com/mmrzaf/sms-gatway/internal/message"
	"github.com/mmrzaf/sms-gatway/internal/metrics"
	"github.com/mmrzaf/sms-gatway/internal/ratelimit"
)

// startAPI runs the public server (customer API) and the admin server
// (dashboard, admin API, DLR intake, metrics).
func startAPI(ctx context.Context, g *errgroup.Group, d deps) {
	batcher := dlr.NewBatcher(d.pool, d.cfg.DLR.BatchSize, d.cfg.DLR.FlushInterval, d.logger)
	g.Go(func() error {
		batcher.Run(ctx)
		return nil
	})
	serve(ctx, g, d, "public", d.cfg.HTTPAddr, publicHandler(d))
	admin, err := newAdminServer(d)
	if err != nil {
		g.Go(func() error { return err })
		return
	}
	serve(ctx, g, d, "admin", d.cfg.AdminAddr, adminHandler(d, batcher, admin))
}

func newAdminServer(d deps) (*admin.Server, error) {
	return admin.New(d.pool, newMessageService(d), admin.Config{
		Token:               d.cfg.AdminToken,
		Providers:           d.cfg.Providers.List,
		Lanes:               lanes(d),
		DefaultRateLimitRPS: d.cfg.API.DefaultRateLimitRPS,
		Invariants: invariant.Config{
			LeaseDuration: d.cfg.Dispatch.LeaseDuration,
			SweepInterval: d.cfg.Dispatch.SweepInterval,
		},
	}, d.logger)
}

// lanes lists every queue lane: Express first, then the normal lanes.
func lanes(d deps) []string {
	out := []string{message.ExpressLane}
	for i := range d.cfg.Dispatch.NormalLanes {
		out = append(out, message.NormalLane(i))
	}
	return out
}

// newMessageService builds the message service from configuration.
func newMessageService(d deps) *message.Service {
	return message.NewService(d.pool, message.Config{
		Prices:      message.Prices{Normal: d.cfg.API.PriceNormal, Express: d.cfg.API.PriceExpress},
		MaxSegments: d.cfg.API.MaxSegments,
		NormalLanes: d.cfg.Dispatch.NormalLanes,
		NormalTTL:   d.cfg.Normal.TTL,
		ExpressTTL:  d.cfg.Express.TTL,
	})
}

func publicHandler(d deps) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", httpx.Healthz)
	mux.Handle("GET /readyz", httpx.Readyz(d.ready))

	server := api.New(d.pool,
		newMessageService(d),
		auth.NewAuthenticator(d.pool, d.cfg.API.KeyCacheTTL),
		ratelimit.New(d.cfg.API.Instances))
	server.Register(mux)

	mux.HandleFunc("/", httpx.NotFound)
	return mux
}

func adminHandler(d deps, batcher *dlr.Batcher, adminServer *admin.Server) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", httpx.Healthz)
	mux.Handle("GET /readyz", httpx.Readyz(d.ready))

	names := make([]string, len(d.cfg.Providers.List))
	for i, p := range d.cfg.Providers.List {
		names[i] = p.Name
	}
	mux.Handle("POST /internal/dlr", dlr.NewHandler(batcher, d.cfg.Providers.Secret, names))
	mux.Handle("GET /metrics", metrics.Default.Handler())
	adminServer.Register(mux)

	mux.HandleFunc("/", httpx.NotFound)
	return mux
}
