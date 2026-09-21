// Package app wires the gateway's components together for each process role
// and runs them until shutdown.
package app

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/sync/errgroup"

	"github.com/mmrzaf/sms-gatway/internal/config"
	"github.com/mmrzaf/sms-gatway/internal/httpx"
	"github.com/mmrzaf/sms-gatway/internal/store"
)

// Role selects which components a gateway process runs.
type Role string

// Process roles.
const (
	RoleAPI    Role = "api"
	RoleWorker Role = "worker"
	RoleAll    Role = "all"
)

// ParseRole validates a role name.
func ParseRole(s string) (Role, error) {
	switch r := Role(s); r {
	case RoleAPI, RoleWorker, RoleAll:
		return r, nil
	default:
		return "", fmt.Errorf("unknown role %q: must be api, worker, or all", s)
	}
}

func (r Role) runsAPI() bool    { return r == RoleAPI || r == RoleAll }
func (r Role) runsWorker() bool { return r == RoleWorker || r == RoleAll }

// deps are the shared dependencies handed to every component.
type deps struct {
	cfg    config.Gateway
	pool   *pgxpool.Pool
	logger *slog.Logger
}

// ready is the readiness check: the database must answer.
func (d deps) ready(ctx context.Context) error {
	return store.Ping(ctx, d.pool)
}

// Run starts the components of role and blocks until ctx is cancelled or a
// component fails. On return, every component has stopped.
func Run(ctx context.Context, cfg config.Gateway, role Role, logger *slog.Logger) error {
	pool, err := store.Open(ctx, cfg.Database.URL, cfg.Database.MaxConns)
	if err != nil {
		return err
	}
	defer pool.Close()

	d := deps{cfg: cfg, pool: pool, logger: logger}
	g, ctx := errgroup.WithContext(ctx)
	if role.runsAPI() {
		startAPI(ctx, g, d)
	}
	if role.runsWorker() {
		startWorker(ctx, g, d)
	}
	return g.Wait()
}

// serve runs an HTTP server as part of g.
func serve(ctx context.Context, g *errgroup.Group, d deps, name, addr string, h http.Handler) {
	g.Go(func() error {
		d.logger.Info("listening", "server", name, "addr", addr)
		return httpx.Serve(ctx, httpx.NewServer(addr, withMiddleware(h, d.logger)), d.cfg.ShutdownTimeout)
	})
}

// withMiddleware applies the middleware every server uses.
func withMiddleware(h http.Handler, logger *slog.Logger) http.Handler {
	return httpx.Chain(h,
		httpx.WithRequestID(logger),
		httpx.WithAccessLog(),
		httpx.WithRecovery(),
	)
}
