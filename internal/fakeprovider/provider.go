// Package fakeprovider simulates an SMS operator. It implements the same
// contract a real operator adapter would: a send endpoint that deduplicates
// by message ID, and asynchronous delivery reports sent to the gateway. Its
// latency, failures, timeouts, rejections, outages, and delivery ratio are
// controllable at runtime so that retries, failover, and delivery tracking
// can be demonstrated and tested.
package fakeprovider

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	mrand "math/rand/v2"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

// Config configures a provider instance.
type Config struct {
	Name          string
	GatewayDLRURL string
	Secret        string
	Settings      Settings
}

// Provider is one simulated SMS operator.
type Provider struct {
	name   string
	dlrURL string
	secret string
	logger *slog.Logger
	client *http.Client

	settingsMu sync.RWMutex
	settings   Settings

	dedup  *dedupStore
	recent *recentLog
	stats  counters

	// Behavior that tests shorten or make deterministic.
	random       func() float64
	holdFor      time.Duration // how long a simulated timeout holds the response
	retryInitial time.Duration // first delay between DLR delivery attempts
	retryMax     time.Duration
	retryLimit   int

	ctx    context.Context // cancelled by Close; stops pending reports
	cancel context.CancelFunc
}

type counters struct {
	received, accepted, duplicates, rejected, failed, timedOut, outage atomic.Int64
	dlrSent, dlrPending, dlrFailed                                     atomic.Int64
}

// New returns a provider. Call Close to stop pending delivery reports.
func New(cfg Config, logger *slog.Logger) *Provider {
	ctx, cancel := context.WithCancel(context.Background())
	return &Provider{
		name:         cfg.Name,
		dlrURL:       cfg.GatewayDLRURL,
		secret:       cfg.Secret,
		logger:       logger,
		client:       &http.Client{Timeout: 5 * time.Second},
		settings:     cfg.Settings,
		dedup:        newDedupStore(dedupCapacity),
		recent:       newRecentLog(recentCapacity),
		random:       mrand.Float64,
		holdFor:      30 * time.Second,
		retryInitial: time.Second,
		retryMax:     time.Minute,
		retryLimit:   10,
		ctx:          ctx,
		cancel:       cancel,
	}
}

// Close stops scheduling and sending delivery reports. Reports not yet sent
// are dropped, as they would be when a real provider restarts.
func (p *Provider) Close() {
	p.cancel()
}

// Handler returns the provider's HTTP routes.
func (p *Provider) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /send", p.handleSend)
	mux.HandleFunc("GET /admin/config", p.handleGetConfig)
	mux.HandleFunc("PUT /admin/config", p.handlePutConfig)
	mux.HandleFunc("GET /admin/messages", p.handleMessages)
	mux.HandleFunc("GET /admin/stats", p.handleStats)
	return mux
}

// Settings returns the current simulation settings.
func (p *Provider) Settings() Settings {
	p.settingsMu.RLock()
	defer p.settingsMu.RUnlock()
	return p.settings
}

func (p *Provider) newRef() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return p.name + "-" + hex.EncodeToString(b)
}

// chance returns true with probability rate.
func (p *Provider) chance(rate float64) bool {
	return rate > 0 && p.random() < rate
}

// jittered returns base plus a uniform random value in [0, jitter].
func (p *Provider) jittered(baseMS, jitterMS int) time.Duration {
	d := time.Duration(baseMS) * time.Millisecond
	if jitterMS > 0 {
		d += time.Duration(p.random() * float64(jitterMS) * float64(time.Millisecond))
	}
	return d
}

// sleep waits for d or until ctx is done, and reports whether d elapsed.
func sleep(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return true
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-ctx.Done():
		return false
	}
}
