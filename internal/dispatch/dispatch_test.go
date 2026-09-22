package dispatch

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mmrzaf/sms-gatway/internal/config"
	"github.com/mmrzaf/sms-gatway/internal/dlr"
	"github.com/mmrzaf/sms-gatway/internal/fakeprovider"
	"github.com/mmrzaf/sms-gatway/internal/message"
	"github.com/mmrzaf/sms-gatway/internal/store"
	"github.com/mmrzaf/sms-gatway/internal/testutil"
)

var discard = slog.New(slog.DiscardHandler)

// scripted is a provider whose answer to the n-th request (1-based) is
// decided by the test.
type scripted struct {
	srv    *httptest.Server
	mu     sync.Mutex
	calls  []sendBody
	answer func(n int, req sendBody) (status int, delay time.Duration)
}

func newScripted(t testing.TB, answer func(n int, req sendBody) (int, time.Duration)) *scripted {
	s := &scripted{answer: answer}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req sendBody
		_ = json.NewDecoder(r.Body).Decode(&req)
		s.mu.Lock()
		s.calls = append(s.calls, req)
		n := len(s.calls)
		s.mu.Unlock()
		status, delay := s.answer(n, req)
		if !sleep(r.Context(), delay) {
			return
		}
		w.WriteHeader(status)
		if status == http.StatusOK {
			_, _ = io.WriteString(w, `{"provider_ref":"ref-`+req.ID[:8]+`"}`)
		}
	}))
	t.Cleanup(s.srv.Close)
	return s
}

func ok(int, sendBody) (int, time.Duration) { return http.StatusOK, 0 }

func (s *scripted) received() []sendBody {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]sendBody(nil), s.calls...)
}

func testConfig(urls ...string) Config {
	var providers []config.Provider
	for i, u := range urls {
		providers = append(providers, config.Provider{Name: string(rune('A' + i)), URL: u})
	}
	return Config{
		Providers:               providers,
		NormalLanes:             4,
		NormalConcurrency:       16,
		ExpressConcurrency:      8,
		ClaimBatchSize:          20,
		LeaseDuration:           2 * time.Second,
		PollInterval:            10 * time.Millisecond,
		CompleterBatchSize:      50,
		CompleterFlushInterval:  5 * time.Millisecond,
		ProviderRateLimit:       100_000,
		ExpressReservedRatio:    0.2,
		CircuitFailureThreshold: 5,
		CircuitOpenDuration:     200 * time.Millisecond,
		Normal:                  Policy{Class: message.Normal, MaxAttempts: 8, BackoffBase: 5 * time.Millisecond, BackoffMax: 50 * time.Millisecond, Timeout: 500 * time.Millisecond},
		Express:                 Policy{Class: message.Express, MaxAttempts: 5, BackoffBase: 5 * time.Millisecond, BackoffMax: 20 * time.Millisecond, Timeout: 200 * time.Millisecond, Rotate: true},
		ExpressSLA:              30 * time.Second,
		ShutdownTimeout:         2 * time.Second,
	}
}

func newMessages(pool *pgxpool.Pool, lanes int) *message.Service {
	return message.NewService(pool, message.Config{
		Prices: message.Prices{Normal: 1, Express: 3}, MaxSegments: 10, NormalLanes: lanes,
		NormalTTL: time.Hour, ExpressTTL: 5 * time.Minute,
	})
}

func startWorker(t testing.TB, pool *pgxpool.Pool, cfg Config) *Worker {
	t.Helper()
	w := New(cfg, pool, discard)
	w.random = func() float64 { return 0.5 }
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = w.Run(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})
	return w
}

func accept(t *testing.T, svc *message.Service, customer uuid.UUID, typ message.Type, to string) message.Message {
	t.Helper()
	m, _, err := svc.Accept(context.Background(), customer, message.Request{To: to, Text: "hello", Type: typ})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func reload(t *testing.T, svc *message.Service, m message.Message) message.Message {
	t.Helper()
	got, err := svc.Get(context.Background(), m.CustomerID, m.ID)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func waitForStatus(t *testing.T, svc *message.Service, m message.Message, want message.Status) message.Message {
	t.Helper()
	var got message.Message
	waitFor(t, fmt.Sprintf("message %s to become %s", m.ID, want), func() bool {
		got = reload(t, svc, m)
		return got.Status == want
	})
	return got
}

func queueRows(t testing.TB, pool *pgxpool.Pool) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM queue`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestSendsNormalAndExpressMessages(t *testing.T) {
	pool := testutil.DB(t)
	svc := newMessages(pool, 4)
	c, _ := testutil.Customer(t, pool, 100)
	a := newScripted(t, ok)

	normal := accept(t, svc, c.ID, message.Normal, "+989121234567")
	express := accept(t, svc, c.ID, message.Express, "+989121234568")
	startWorker(t, pool, testConfig(a.srv.URL))

	for _, m := range []message.Message{normal, express} {
		got := waitForStatus(t, svc, m, message.StatusSent)
		if got.Attempts != 1 || *got.Provider != "A" || got.ProviderRef == nil || got.SentAt == nil || got.SLABreached {
			t.Errorf("sent message: %+v", got)
		}
	}
	waitFor(t, "the queue to empty", func() bool { return queueRows(t, pool) == 0 })
}

// IT11: permanent rejection.
func TestPermanentRejectionFailsAndRefunds(t *testing.T) {
	pool := testutil.DB(t)
	svc := newMessages(pool, 4)
	c, _ := testutil.Customer(t, pool, 10)
	a := newScripted(t, func(int, sendBody) (int, time.Duration) { return http.StatusBadRequest, 0 })

	m := accept(t, svc, c.ID, message.Normal, "+989121234567")
	startWorker(t, pool, testConfig(a.srv.URL))

	got := waitForStatus(t, svc, m, message.StatusFailed)
	if *got.FailureReason != message.ReasonRejected || got.Attempts != 1 || got.CompletedAt == nil ||
		!strings.Contains(*got.LastError, "400") {
		t.Errorf("failed message: %+v", got)
	}
	assertRefunded(t, pool, c.ID, 10, 1)
}

// IT12: attempts exhausted.
func TestAttemptsExhaustedFailsAndRefunds(t *testing.T) {
	pool := testutil.DB(t)
	svc := newMessages(pool, 4)
	c, _ := testutil.Customer(t, pool, 10)
	a := newScripted(t, func(int, sendBody) (int, time.Duration) { return http.StatusInternalServerError, 0 })

	cfg := testConfig(a.srv.URL)
	cfg.Normal.MaxAttempts = 3
	m := accept(t, svc, c.ID, message.Normal, "+989121234567")
	startWorker(t, pool, cfg)

	got := waitForStatus(t, svc, m, message.StatusFailed)
	if *got.FailureReason != message.ReasonAttemptsExhausted || got.Attempts != 3 {
		t.Errorf("failed message: %+v", got)
	}
	if n := len(a.received()); n != 3 {
		t.Errorf("provider received %d attempts, want 3", n)
	}
	assertRefunded(t, pool, c.ID, 10, 1)
}

func assertRefunded(t *testing.T, pool *pgxpool.Pool, customer uuid.UUID, wantBalance int64, wantRefunds int) {
	t.Helper()
	ctx := context.Background()
	var balance int64
	var refunds int
	if err := pool.QueryRow(ctx, `SELECT balance FROM customers WHERE id = $1`, customer).Scan(&balance); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM transactions WHERE customer_id = $1 AND kind = 'refund'`, customer).Scan(&refunds); err != nil {
		t.Fatal(err)
	}
	if balance != wantBalance || refunds != wantRefunds {
		t.Errorf("balance %d with %d refunds, want %d with %d", balance, refunds, wantBalance, wantRefunds)
	}
	testutil.AssertInvariants(t, pool)
}

// IT15: circuit breaker.
func TestOpenCircuitMovesNormalTrafficToNextProvider(t *testing.T) {
	pool := testutil.DB(t)
	svc := newMessages(pool, 4)
	c, _ := testutil.Customer(t, pool, 100)
	a := newScripted(t, func(int, sendBody) (int, time.Duration) { return http.StatusServiceUnavailable, 0 })
	b := newScripted(t, ok)

	var msgs []message.Message
	for i := range 30 {
		msgs = append(msgs, accept(t, svc, c.ID, message.Normal, fmt.Sprintf("+98912123%04d", i)))
	}
	cfg := testConfig(a.srv.URL, b.srv.URL)
	cfg.CircuitFailureThreshold = 3
	cfg.CircuitOpenDuration = time.Minute
	w := startWorker(t, pool, cfg)

	for _, m := range msgs {
		if got := waitForStatus(t, svc, m, message.StatusSent); *got.Provider != "B" {
			t.Errorf("message sent via %s", *got.Provider)
		}
	}
	if st := w.providers[0].breaker.State(); st != CircuitOpen {
		t.Errorf("circuit A is %s", st)
	}
	if n := len(a.received()); n > 3+cfg.NormalConcurrency {
		t.Errorf("provider A received %d requests after failing", n)
	}
}

// IT16: Express rotation.
func TestExpressRetryMovesToNextProvider(t *testing.T) {
	pool := testutil.DB(t)
	svc := newMessages(pool, 4)
	c, _ := testutil.Customer(t, pool, 100)
	a := newScripted(t, func(int, sendBody) (int, time.Duration) { return http.StatusOK, time.Second })
	b := newScripted(t, ok)

	m := accept(t, svc, c.ID, message.Express, "+989121234567")
	startWorker(t, pool, testConfig(a.srv.URL, b.srv.URL))

	got := waitForStatus(t, svc, m, message.StatusSent)
	if *got.Provider != "B" || got.Attempts != 2 {
		t.Errorf("express message: provider %s, attempts %d", *got.Provider, got.Attempts)
	}
}

// IT17: normal messages retry the same provider after a timeout.
func TestNormalRetryStaysOnProviderAfterTimeout(t *testing.T) {
	pool := testutil.DB(t)
	svc := newMessages(pool, 4)
	c, _ := testutil.Customer(t, pool, 100)
	a := newScripted(t, func(n int, _ sendBody) (int, time.Duration) {
		if n == 1 {
			return http.StatusOK, time.Second
		}
		return http.StatusOK, 0
	})
	b := newScripted(t, ok)

	cfg := testConfig(a.srv.URL, b.srv.URL)
	cfg.Normal.Timeout = 200 * time.Millisecond
	m := accept(t, svc, c.ID, message.Normal, "+989121234567")
	startWorker(t, pool, cfg)

	got := waitForStatus(t, svc, m, message.StatusSent)
	if *got.Provider != "A" || got.Attempts != 2 || len(b.received()) != 0 {
		t.Errorf("provider %s, attempts %d, B received %d", *got.Provider, got.Attempts, len(b.received()))
	}
}

// IT08: lease recovery.
func TestExpiredLeaseIsReclaimed(t *testing.T) {
	pool := testutil.DB(t)
	svc := newMessages(pool, 4)
	c, _ := testutil.Customer(t, pool, 100)
	a := newScripted(t, ok)

	m := accept(t, svc, c.ID, message.Normal, "+989121234567")
	// A worker that crashed after claiming: its lease ends 300 ms from now.
	if _, err := pool.Exec(context.Background(), `
		UPDATE queue SET lease_owner = 'crashed-worker', next_attempt_at = now() + interval '300 milliseconds'
		WHERE message_id = $1`, m.ID); err != nil {
		t.Fatal(err)
	}
	startWorker(t, pool, testConfig(a.srv.URL))

	time.Sleep(100 * time.Millisecond)
	if len(a.received()) != 0 {
		t.Fatal("a leased message was claimed before its lease expired")
	}
	waitForStatus(t, svc, m, message.StatusSent)
	if n := len(a.received()); n != 1 {
		t.Errorf("provider received %d requests, want 1", n)
	}
}

// IT09: the provider accepted a message but the worker's outcome was lost.
func TestResendAfterLostOutcomeIsDeduplicated(t *testing.T) {
	pool := testutil.DB(t)
	svc := newMessages(pool, 4)
	c, _ := testutil.Customer(t, pool, 100)
	gateway := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	t.Cleanup(gateway.Close)
	fp := fakeprovider.New(fakeprovider.Config{Name: "A", GatewayDLRURL: gateway.URL, Secret: "s",
		Settings: fakeprovider.Settings{DeliveryRatio: 1}}, discard)
	t.Cleanup(fp.Close)
	provider := httptest.NewServer(fp.Handler())
	t.Cleanup(provider.Close)

	m := accept(t, svc, c.ID, message.Normal, "+989121234567")
	// The crashed worker's send reached the provider...
	cl := newClient("A", provider.URL, 1)
	firstRef, err := cl.send(context.Background(), time.Second, m.ID, m.Recipient, m.Body)
	if err != nil {
		t.Fatal(err)
	}
	// ...but its outcome was never committed, and its lease has expired.
	if _, err := pool.Exec(context.Background(),
		`UPDATE queue SET lease_owner = 'crashed-worker', next_attempt_at = now() WHERE message_id = $1`, m.ID); err != nil {
		t.Fatal(err)
	}
	startWorker(t, pool, testConfig(provider.URL))

	got := waitForStatus(t, svc, m, message.StatusSent)
	if *got.ProviderRef != firstRef {
		t.Errorf("provider ref %s, want the original %s", *got.ProviderRef, firstRef)
	}
	if st := fp.Stats(); st.Accepted != 1 || st.Duplicates != 1 {
		t.Errorf("provider accepted %d and deduplicated %d; want 1 and 1", st.Accepted, st.Duplicates)
	}
}

// IT10: a delivery report arrives before the worker records the acceptance.
func TestEarlyDeliveryReportKeepsTerminalStatus(t *testing.T) {
	pool := testutil.DB(t)
	svc := newMessages(pool, 4)
	c, _ := testutil.Customer(t, pool, 100)
	ctx := context.Background()

	m := accept(t, svc, c.ID, message.Normal, "+989121234567")
	w := New(testConfig("http://unused"), pool, discard)
	jobs, err := w.claim(ctx, message.Lane(message.Normal, c.ID, 4), 1)
	if err != nil || len(jobs) != 1 {
		t.Fatalf("claim: %v, %d jobs", err, len(jobs))
	}

	outcomes, err := dlr.Apply(ctx, pool, []dlr.Report{{MessageID: m.ID, Provider: "A", ProviderRef: "ref-1", Status: "delivered"}})
	if err != nil || outcomes[0] != dlr.Applied {
		t.Fatalf("apply report: %v, %v", outcomes, err)
	}
	err = store.WithTx(ctx, pool, func(tx pgx.Tx) error {
		return w.completer.commit(ctx, tx, []outcome{{kind: outcomeSent, job: jobs[0], provider: "A", providerRef: "ref-1", attempted: true}})
	})
	if err != nil {
		t.Fatal(err)
	}

	got := reload(t, svc, m)
	if got.Status != message.StatusDelivered || got.SentAt == nil || *got.ProviderRef != "ref-1" || got.Attempts != 1 {
		t.Errorf("message: %+v", got)
	}
	if n := queueRows(t, pool); n != 0 {
		t.Errorf("%d queue rows left", n)
	}
}

// IT18: a backlog in one lane does not delay other lanes.
func TestLaneRotationServesSmallCustomerDuringBacklog(t *testing.T) {
	pool := testutil.DB(t)
	svc := newMessages(pool, 4)
	heavy, _ := testutil.Customer(t, pool, 1000)
	light, _ := testutil.Customer(t, pool, 100)
	for message.Lane(message.Normal, light.ID, 4) == message.Lane(message.Normal, heavy.ID, 4) {
		light, _ = testutil.Customer(t, pool, 100)
	}

	backlog := make([]message.Request, 400)
	for i := range backlog {
		backlog[i] = message.Request{To: "+989100000000", Text: "bulk"}
	}
	if _, err := svc.AcceptBatch(context.Background(), heavy.ID, backlog); err != nil {
		t.Fatal(err)
	}
	for range 5 {
		accept(t, svc, light.ID, message.Normal, "+989111111111")
	}

	a := newScripted(t, func(int, sendBody) (int, time.Duration) { return http.StatusOK, 2 * time.Millisecond })
	cfg := testConfig(a.srv.URL)
	cfg.NormalConcurrency, cfg.ClaimBatchSize = 4, 4
	startWorker(t, pool, cfg)

	waitFor(t, "the small customer's messages", func() bool {
		n := 0
		for _, r := range a.received() {
			if r.To == "+989111111111" {
				n++
			}
		}
		return n == 5
	})
	last := 0
	for i, r := range a.received() {
		if r.To == "+989111111111" {
			last = i
		}
	}
	if last > 40 {
		t.Errorf("the small customer's last message was request %d of a 400-message backlog", last)
	}
}

// IT19: normal backlog does not delay Express.
func TestExpressIsNotDelayedByNormalBacklog(t *testing.T) {
	pool := testutil.DB(t)
	svc := newMessages(pool, 4)
	c, _ := testutil.Customer(t, pool, 1000)

	backlog := make([]message.Request, 300)
	for i := range backlog {
		backlog[i] = message.Request{To: "+989100000000", Text: "bulk"}
	}
	if _, err := svc.AcceptBatch(context.Background(), c.ID, backlog); err != nil {
		t.Fatal(err)
	}
	express := accept(t, svc, c.ID, message.Express, "+989122222222")

	a := newScripted(t, func(int, sendBody) (int, time.Duration) { return http.StatusOK, 5 * time.Millisecond })
	cfg := testConfig(a.srv.URL)
	cfg.NormalConcurrency, cfg.ClaimBatchSize = 4, 4
	startWorker(t, pool, cfg)

	waitForStatus(t, svc, express, message.StatusSent)
	for i, r := range a.received() {
		if r.To == "+989122222222" && i > 10 {
			t.Errorf("the express message was request %d behind a normal backlog", i)
		}
	}
}

func TestNotificationWakesIdleExpressPool(t *testing.T) {
	pool := testutil.DB(t)
	svc := newMessages(pool, 4)
	c, _ := testutil.Customer(t, pool, 100)
	a := newScripted(t, ok)

	cfg := testConfig(a.srv.URL)
	cfg.PollInterval = time.Minute
	startWorker(t, pool, cfg)
	time.Sleep(300 * time.Millisecond) // let both pools go idle

	start := time.Now()
	m := accept(t, svc, c.ID, message.Express, "+989121234567")
	waitForStatus(t, svc, m, message.StatusSent)
	if d := time.Since(start); d > 5*time.Second {
		t.Errorf("express message waited %v despite the notification", d)
	}
}

func TestEndToEndDeliveryReport(t *testing.T) {
	pool := testutil.DB(t)
	svc := newMessages(pool, 4)
	c, _ := testutil.Customer(t, pool, 100)

	ctx, cancel := context.WithCancel(context.Background())
	batcher := dlr.NewBatcher(pool, 100, 5*time.Millisecond, discard)
	batcherDone := make(chan struct{})
	go func() { defer close(batcherDone); batcher.Run(ctx) }()
	t.Cleanup(func() { cancel(); <-batcherDone })
	intake := httptest.NewServer(dlr.NewHandler(batcher, "s", []string{"A"}))
	t.Cleanup(intake.Close)

	fp := fakeprovider.New(fakeprovider.Config{Name: "A", GatewayDLRURL: intake.URL, Secret: "s",
		Settings: fakeprovider.Settings{DeliveryRatio: 1}}, discard)
	t.Cleanup(fp.Close)
	provider := httptest.NewServer(fp.Handler())
	t.Cleanup(provider.Close)

	delivered := accept(t, svc, c.ID, message.Normal, "+989121234567")
	undelivered := accept(t, svc, c.ID, message.Express, "+998121234567")
	startWorker(t, pool, testConfig(provider.URL))

	waitForStatus(t, svc, delivered, message.StatusDelivered)
	// The report may overtake the worker's record of the acceptance; the
	// provider details and sent time follow when the completer commits.
	var got message.Message
	waitFor(t, "the acceptance to be recorded", func() bool {
		got = reload(t, svc, delivered)
		return got.SentAt != nil
	})
	if got.Status != message.StatusDelivered || got.CompletedAt == nil || got.ProviderRef == nil || got.Attempts != 1 {
		t.Errorf("delivered message: %+v", got)
	}
	waitForStatus(t, svc, undelivered, message.StatusUndelivered)
	testutil.AssertInvariants(t, pool)
}

// IT21: a customer is deleted while its messages are in flight.
func TestCustomerDeletedInFlight(t *testing.T) {
	pool := testutil.DB(t)
	svc := newMessages(pool, 4)
	c, _ := testutil.Customer(t, pool, 100)
	ctx := context.Background()

	m := accept(t, svc, c.ID, message.Normal, "+989121234567")
	w := New(testConfig("http://unused"), pool, discard)
	jobs, err := w.claim(ctx, message.Lane(message.Normal, c.ID, 4), 1)
	if err != nil || len(jobs) != 1 {
		t.Fatalf("claim: %v, %d jobs", err, len(jobs))
	}
	if _, err := pool.Exec(ctx, `DELETE FROM customers WHERE id = $1`, c.ID); err != nil {
		t.Fatal(err)
	}

	for _, o := range []outcome{
		{kind: outcomeSent, job: jobs[0], provider: "A", providerRef: "r", attempted: true},
		{kind: outcomeFailed, job: jobs[0], provider: "A", reason: message.ReasonRejected, attempted: true},
	} {
		if err := store.WithTx(ctx, pool, func(tx pgx.Tx) error {
			return w.completer.commit(ctx, tx, []outcome{o})
		}); err != nil {
			t.Errorf("completing a deleted customer's message: %v", err)
		}
	}
	outcomes, err := dlr.Apply(ctx, pool, []dlr.Report{{MessageID: m.ID, Provider: "A", ProviderRef: "r", Status: "delivered"}})
	if err != nil || outcomes[0] != dlr.Unknown {
		t.Errorf("report for a deleted message: %v, %v", outcomes, err)
	}
	testutil.AssertInvariants(t, pool)
}
