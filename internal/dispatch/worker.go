package dispatch

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	mrand "math/rand/v2"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mmrzaf/sms-gatway/internal/config"
	"github.com/mmrzaf/sms-gatway/internal/message"
)

// Config configures a worker.
type Config struct {
	// Providers in priority order.
	Providers []config.Provider

	NormalLanes        int
	NormalConcurrency  int
	ExpressConcurrency int
	ClaimBatchSize     int
	LeaseDuration      time.Duration
	PollInterval       time.Duration

	CompleterBatchSize     int
	CompleterFlushInterval time.Duration

	ProviderRateLimit       int
	ExpressReservedRatio    float64
	CircuitFailureThreshold int
	CircuitOpenDuration     time.Duration

	Normal     Policy
	Express    Policy
	ExpressSLA time.Duration

	// ShutdownTimeout bounds how long in-flight sends may finish on shutdown.
	ShutdownTimeout time.Duration
}

// FromGateway builds a worker configuration from the gateway configuration.
func FromGateway(c config.Gateway) Config {
	return Config{
		Providers:               c.Providers.List,
		NormalLanes:             c.Dispatch.NormalLanes,
		NormalConcurrency:       c.Dispatch.NormalConcurrency,
		ExpressConcurrency:      c.Dispatch.ExpressConcurrency,
		ClaimBatchSize:          c.Dispatch.ClaimBatchSize,
		LeaseDuration:           c.Dispatch.LeaseDuration,
		PollInterval:            c.Dispatch.PollInterval,
		CompleterBatchSize:      c.Dispatch.CompleterBatchSize,
		CompleterFlushInterval:  c.Dispatch.CompleterFlushInterval,
		ProviderRateLimit:       c.Providers.RateLimit,
		ExpressReservedRatio:    c.Providers.ExpressReservedRatio,
		CircuitFailureThreshold: c.Providers.CircuitFailureThreshold,
		CircuitOpenDuration:     c.Providers.CircuitOpenDuration,
		Normal: Policy{
			Class:       message.Normal,
			MaxAttempts: c.Normal.MaxAttempts,
			BackoffBase: c.Normal.BackoffBase,
			BackoffMax:  c.Normal.BackoffMax,
			Timeout:     c.Normal.Timeout,
		},
		Express: Policy{
			Class:       message.Express,
			MaxAttempts: c.Express.MaxAttempts,
			BackoffBase: c.Express.BackoffBase,
			BackoffMax:  c.Express.BackoffMax,
			Timeout:     c.Express.Timeout,
			Rotate:      true,
		},
		ExpressSLA:      c.Express.SLA,
		ShutdownTimeout: c.ShutdownTimeout,
	}
}

// provider is a worker's view of one provider.
type provider struct {
	name    string
	client  *client
	breaker *breaker
	budget  *budget
}

// Worker dispatches queued messages.
type Worker struct {
	id     string
	cfg    Config
	db     *pgxpool.Pool
	logger *slog.Logger

	providers []*provider
	express   *pool
	normal    *pool
	completer *completer
	random    func() float64

	counters Counters
}

// Counters are a worker's cumulative outcome counts.
type Counters struct {
	Sent, Retried, Deferred, Failed, Expired atomic.Int64
}

// New returns a worker with a unique ID.
func New(cfg Config, db *pgxpool.Pool, logger *slog.Logger) *Worker {
	w := &Worker{
		id:     newWorkerID(),
		cfg:    cfg,
		db:     db,
		random: mrand.Float64,
	}
	w.logger = logger.With("worker_id", w.id)
	conns := cfg.NormalConcurrency + cfg.ExpressConcurrency
	for _, p := range cfg.Providers {
		w.providers = append(w.providers, &provider{
			name:    p.Name,
			client:  newClient(p.Name, p.URL, conns),
			breaker: newBreaker(cfg.CircuitFailureThreshold, cfg.CircuitOpenDuration),
			budget:  newBudget(cfg.ProviderRateLimit, cfg.ExpressReservedRatio),
		})
	}
	w.completer = newCompleter(w)

	normalLanes := make([]string, cfg.NormalLanes)
	for i := range normalLanes {
		normalLanes[i] = message.NormalLane(i)
	}
	w.express = newPool("express", cfg.Express, []string{message.ExpressLane}, cfg.ExpressConcurrency)
	w.normal = newPool("normal", cfg.Normal, normalLanes, cfg.NormalConcurrency)
	return w
}

// ID returns the worker's identifier, used as the lease owner.
func (w *Worker) ID() string { return w.id }

// Run dispatches until ctx is cancelled. On shutdown it stops claiming,
// waits up to ShutdownTimeout for in-flight sends, and commits their
// outcomes. Sends that do not finish keep their lease until it expires and
// are then claimed by another worker.
func (w *Worker) Run(ctx context.Context) error {
	sendCtx, cancelSends := context.WithCancel(context.WithoutCancel(ctx))
	defer cancelSends()

	completerDone := make(chan struct{})
	go func() {
		defer close(completerDone)
		w.completer.run()
	}()

	var pools sync.WaitGroup
	for _, p := range []*pool{w.express, w.normal} {
		pools.Add(1)
		go func() {
			defer pools.Done()
			w.runPool(ctx, sendCtx, p)
		}()
	}
	listenerDone := make(chan struct{})
	go func() {
		defer close(listenerDone)
		w.listen(ctx, w.express)
	}()

	w.logger.Info("worker started", "providers", len(w.providers))
	<-ctx.Done()
	pools.Wait()
	<-listenerDone

	// Give in-flight sends a bounded time to finish, then cut them off.
	drained := make(chan struct{})
	go func() {
		w.express.inFlight.Wait()
		w.normal.inFlight.Wait()
		close(drained)
	}()
	select {
	case <-drained:
	case <-time.After(w.cfg.ShutdownTimeout):
		w.logger.Warn("in-flight sends did not finish before the shutdown timeout")
		cancelSends()
		<-drained
	}
	w.completer.stop()
	<-completerDone
	w.logger.Info("worker stopped")
	return nil
}

// Snapshot describes the worker for its heartbeat.
func (w *Worker) Snapshot() map[string]any {
	circuits := make(map[string]string, len(w.providers))
	for _, p := range w.providers {
		circuits[p.name] = p.breaker.State().String()
	}
	pool := func(p *pool) map[string]any {
		return map[string]any{"concurrency": p.concurrency, "in_flight": p.active.Load()}
	}
	return map[string]any{
		"pools": map[string]any{"express": pool(w.express), "normal": pool(w.normal)},
		"counters": map[string]int64{
			"sent":     w.counters.Sent.Load(),
			"retried":  w.counters.Retried.Load(),
			"deferred": w.counters.Deferred.Load(),
			"failed":   w.counters.Failed.Load(),
			"expired":  w.counters.Expired.Load(),
		},
		"circuits": circuits,
	}
}

func newWorkerID() string {
	host, err := os.Hostname()
	if err != nil {
		host = "worker"
	}
	b := make([]byte, 2)
	_, _ = rand.Read(b)
	return fmt.Sprintf("%s-%d-%s", host, os.Getpid(), hex.EncodeToString(b))
}
