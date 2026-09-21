package sweeper_test

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mmrzaf/sms-gatway/internal/config"
	"github.com/mmrzaf/sms-gatway/internal/dispatch"
	"github.com/mmrzaf/sms-gatway/internal/dlr"
	"github.com/mmrzaf/sms-gatway/internal/message"
	"github.com/mmrzaf/sms-gatway/internal/sweeper"
	"github.com/mmrzaf/sms-gatway/internal/testutil"
)

var discard = slog.New(slog.DiscardHandler)

func newSweeper(pool *pgxpool.Pool) *sweeper.Sweeper {
	return sweeper.New(sweeper.Config{Interval: time.Hour, ExpressSLA: 30 * time.Second, NormalLanes: 4}, pool, discard)
}

func newMessages(pool *pgxpool.Pool, ttl time.Duration) *message.Service {
	return message.NewService(pool, message.Config{Prices: message.Prices{Normal: 1, Express: 3},
		MaxSegments: 10, NormalLanes: 4, NormalTTL: ttl, ExpressTTL: ttl})
}

func accept(t *testing.T, svc *message.Service, customer uuid.UUID, typ message.Type) message.Message {
	t.Helper()
	m, _, err := svc.Accept(context.Background(), customer, message.Request{To: "+989121234567", Text: "hi", Type: typ})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func exec(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), sql, args...); err != nil {
		t.Fatal(err)
	}
}

func status(t *testing.T, pool *pgxpool.Pool, id uuid.UUID) (string, bool) {
	t.Helper()
	var s string
	var queued bool
	err := pool.QueryRow(context.Background(), `
		SELECT m.status, EXISTS (SELECT 1 FROM queue q WHERE q.message_id = m.id)
		FROM messages m WHERE m.id = $1`, id).Scan(&s, &queued)
	if err != nil {
		t.Fatal(err)
	}
	return s, queued
}

func expireNow(t *testing.T, pool *pgxpool.Pool, ids ...uuid.UUID) {
	t.Helper()
	exec(t, pool, `UPDATE messages SET expires_at = now() - interval '1 second' WHERE id = ANY($1::uuid[])`, ids)
	exec(t, pool, `UPDATE queue SET expires_at = now() - interval '1 second' WHERE message_id = ANY($1::uuid[])`, ids)
}

func TestExpireRefundsMessagesNoWorkerHolds(t *testing.T) {
	pool := testutil.DB(t)
	svc := newMessages(pool, time.Hour)
	c, _ := testutil.Customer(t, pool, 10)
	waiting := accept(t, svc, c.ID, message.Express)
	inFlight := accept(t, svc, c.ID, message.Normal)
	current := accept(t, svc, c.ID, message.Normal)
	expireNow(t, pool, waiting.ID, inFlight.ID)
	exec(t, pool, `UPDATE queue SET lease_owner = 'w', next_attempt_at = now() + interval '1 minute' WHERE message_id = $1`, inFlight.ID)

	res, err := newSweeper(pool).Sweep(context.Background())
	if err != nil || res.Expired != 1 {
		t.Fatalf("sweep: %+v, %v", res, err)
	}
	if s, queued := status(t, pool, waiting.ID); s != "expired" || queued {
		t.Errorf("waiting message: %s, queued %v", s, queued)
	}
	for _, m := range []message.Message{inFlight, current} {
		if s, queued := status(t, pool, m.ID); s != "accepted" || !queued {
			t.Errorf("message %s: %s, queued %v", m.ID, s, queued)
		}
	}
	got, _ := svc.Get(context.Background(), c.ID, waiting.ID)
	if !got.SLABreached || *got.FailureReason != message.ReasonExpired {
		t.Errorf("expired express message: %+v", got)
	}
	var balance int64
	_ = pool.QueryRow(context.Background(), `SELECT balance FROM customers WHERE id = $1`, c.ID).Scan(&balance)
	if balance != 10-1-1 {
		t.Errorf("balance %d, want 8 after refunding the 3-credit express message", balance)
	}
	testutil.AssertInvariants(t, pool)
}

func TestFlagExpressSLA(t *testing.T) {
	pool := testutil.DB(t)
	svc := newMessages(pool, time.Hour)
	c, _ := testutil.Customer(t, pool, 10)
	late := accept(t, svc, c.ID, message.Express)
	lateNormal := accept(t, svc, c.ID, message.Normal)
	fresh := accept(t, svc, c.ID, message.Express)
	exec(t, pool, `UPDATE messages SET accepted_at = now() - interval '1 minute' WHERE id = ANY($1::uuid[])`,
		[]uuid.UUID{late.ID, lateNormal.ID})

	res, err := newSweeper(pool).Sweep(context.Background())
	if err != nil || res.Flagged != 1 {
		t.Fatalf("sweep: %+v, %v", res, err)
	}
	for id, want := range map[uuid.UUID]bool{late.ID: true, lateNormal.ID: false, fresh.ID: false} {
		got, _ := svc.Get(context.Background(), c.ID, id)
		if got.SLABreached != want {
			t.Errorf("message %s: sla_breached %v, want %v", id, got.SLABreached, want)
		}
	}
}

func TestRemoveOrphanedQueueRows(t *testing.T) {
	pool := testutil.DB(t)
	svc := newMessages(pool, time.Hour)
	c, _ := testutil.Customer(t, pool, 10)
	waiting := accept(t, svc, c.ID, message.Normal)
	leased := accept(t, svc, c.ID, message.Normal)
	exec(t, pool, `UPDATE queue SET lease_owner = 'w', next_attempt_at = now() + interval '1 minute' WHERE message_id = $1`, leased.ID)

	var reports []dlr.Report
	for _, m := range []message.Message{waiting, leased} {
		reports = append(reports, dlr.Report{MessageID: m.ID, Provider: "A", ProviderRef: "r", Status: "delivered"})
	}
	if _, err := dlr.Apply(context.Background(), pool, reports); err != nil {
		t.Fatal(err)
	}

	res, err := newSweeper(pool).Sweep(context.Background())
	if err != nil || res.Orphans != 1 {
		t.Fatalf("sweep: %+v, %v", res, err)
	}
	if _, queued := status(t, pool, waiting.ID); queued {
		t.Error("orphan of a waiting message was kept")
	}
	if _, queued := status(t, pool, leased.ID); !queued {
		t.Error("a row held by a worker was removed")
	}
}

func TestReassignRowsFromRemovedLanes(t *testing.T) {
	pool := testutil.DB(t)
	svc := newMessages(pool, time.Hour)
	c, _ := testutil.Customer(t, pool, 10)
	m := accept(t, svc, c.ID, message.Normal)
	exec(t, pool, `UPDATE queue SET lane = 'normal-9' WHERE message_id = $1`, m.ID)

	res, err := newSweeper(pool).Sweep(context.Background())
	if err != nil || res.Reassigned != 1 {
		t.Fatalf("sweep: %+v, %v", res, err)
	}
	var lane string
	_ = pool.QueryRow(context.Background(), `SELECT lane FROM queue WHERE message_id = $1`, m.ID).Scan(&lane)
	if want := message.Lane(message.Normal, c.ID, 4); lane != want {
		t.Errorf("lane %s, want %s", lane, want)
	}
}

func TestRemoveStaleWorkers(t *testing.T) {
	pool := testutil.DB(t)
	exec(t, pool, `INSERT INTO workers (id, role, started_at, last_seen) VALUES
		('gone', 'worker', now() - interval '3 hours', now() - interval '2 hours'),
		('alive', 'worker', now(), now())`)
	res, err := newSweeper(pool).Sweep(context.Background())
	if err != nil || res.Workers != 1 {
		t.Fatalf("sweep: %+v, %v", res, err)
	}
}

// IT20: concurrent sweepers refund each message exactly once.
func TestConcurrentSweepsRefundOnce(t *testing.T) {
	pool := testutil.DB(t)
	svc := newMessages(pool, time.Hour)
	c, _ := testutil.Customer(t, pool, 100)
	var ids []uuid.UUID
	for range 50 {
		ids = append(ids, accept(t, svc, c.ID, message.Normal).ID)
	}
	expireNow(t, pool, ids...)

	var wg sync.WaitGroup
	var mu sync.Mutex
	total := 0
	for range 5 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, err := newSweeper(pool).Sweep(context.Background())
			if err != nil {
				t.Error(err)
			}
			mu.Lock()
			total += res.Expired
			mu.Unlock()
		}()
	}
	wg.Wait()
	for total < 50 {
		res, err := newSweeper(pool).Sweep(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		total += res.Expired
	}
	if total != 50 {
		t.Errorf("expired %d messages, want 50", total)
	}
	var refunds int
	_ = pool.QueryRow(context.Background(), `SELECT count(*) FROM transactions WHERE kind = 'refund'`).Scan(&refunds)
	if refunds != 50 {
		t.Errorf("%d refunds, want 50", refunds)
	}
	testutil.AssertInvariants(t, pool)
}

// IT13: a provider outage longer than the TTL ends in expiry and refunds,
// and waiting does not consume attempts.
func TestOutageLongerThanTTLExpiresAndRefunds(t *testing.T) {
	pool := testutil.DB(t)
	svc := newMessages(pool, 700*time.Millisecond)
	c, _ := testutil.Customer(t, pool, 100)
	down := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	t.Cleanup(down.Close)

	var ids []uuid.UUID
	for range 20 {
		ids = append(ids, accept(t, svc, c.ID, message.Normal).ID)
	}

	ctx, cancel := context.WithCancel(context.Background())
	w := dispatch.New(dispatch.Config{
		Providers:   []config.Provider{{Name: "A", URL: down.URL}},
		NormalLanes: 4, NormalConcurrency: 4, ExpressConcurrency: 2, ClaimBatchSize: 4,
		LeaseDuration: 2 * time.Second, PollInterval: 10 * time.Millisecond,
		CompleterBatchSize: 50, CompleterFlushInterval: 5 * time.Millisecond,
		ProviderRateLimit: 10_000, ExpressReservedRatio: 0.2,
		CircuitFailureThreshold: 2, CircuitOpenDuration: time.Minute,
		Normal:          dispatch.Policy{Class: message.Normal, MaxAttempts: 8, BackoffBase: 5 * time.Millisecond, BackoffMax: 10 * time.Millisecond, Timeout: 200 * time.Millisecond},
		Express:         dispatch.Policy{Class: message.Express, MaxAttempts: 5, BackoffBase: 5 * time.Millisecond, BackoffMax: 10 * time.Millisecond, Timeout: 200 * time.Millisecond, Rotate: true},
		ExpressSLA:      30 * time.Second,
		ShutdownTimeout: time.Second,
	}, pool, discard)
	workerDone := make(chan struct{})
	go func() { defer close(workerDone); _ = w.Run(ctx) }()
	sw := sweeper.New(sweeper.Config{Interval: 50 * time.Millisecond, ExpressSLA: 30 * time.Second, NormalLanes: 4}, pool, discard)
	sweeperDone := make(chan struct{})
	go func() { defer close(sweeperDone); sw.Run(ctx) }()
	defer func() { cancel(); <-workerDone; <-sweeperDone }()

	deadline := time.Now().Add(10 * time.Second)
	for {
		var left int
		_ = pool.QueryRow(context.Background(), `SELECT count(*) FROM messages WHERE status = 'accepted'`).Scan(&left)
		if left == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("%d messages still accepted", left)
		}
		time.Sleep(20 * time.Millisecond)
	}

	var expired, maxAttempts int
	_ = pool.QueryRow(context.Background(),
		`SELECT count(*) FILTER (WHERE status = 'expired'), max(attempts) FROM messages`).Scan(&expired, &maxAttempts)
	if expired != len(ids) || maxAttempts >= 8 {
		t.Errorf("%d of %d expired, max attempts %d", expired, len(ids), maxAttempts)
	}
	var balance int64
	_ = pool.QueryRow(context.Background(), `SELECT balance FROM customers WHERE id = $1`, c.ID).Scan(&balance)
	if balance != 100 {
		t.Errorf("balance %d, want 100 after refunds", balance)
	}
	testutil.AssertInvariants(t, pool)
}
