package dlr_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mmrzaf/sms-gatway/internal/dlr"
	"github.com/mmrzaf/sms-gatway/internal/message"
	"github.com/mmrzaf/sms-gatway/internal/testutil"
)

func newMessage(t *testing.T, pool *pgxpool.Pool, status message.Status) uuid.UUID {
	t.Helper()
	c, _ := testutil.Customer(t, pool, 10)
	svc := message.NewService(pool, message.Config{Prices: message.Prices{Normal: 1, Express: 3},
		MaxSegments: 10, NormalLanes: 4, NormalTTL: time.Hour, ExpressTTL: time.Minute})
	m, _, err := svc.Accept(context.Background(), c.ID, message.Request{To: "+989121234567", Text: "hi"})
	if err != nil {
		t.Fatal(err)
	}
	if status != message.StatusAccepted {
		var reason *string
		if status == message.StatusFailed {
			r := "rejected"
			reason = &r
		}
		if _, err := pool.Exec(context.Background(),
			`UPDATE messages SET status = $2, failure_reason = $3 WHERE id = $1`, m.ID, string(status), reason); err != nil {
			t.Fatal(err)
		}
	}
	return m.ID
}

func statusOf(t *testing.T, pool *pgxpool.Pool, id uuid.UUID) string {
	t.Helper()
	var s string
	if err := pool.QueryRow(context.Background(), `SELECT status FROM messages WHERE id = $1`, id).Scan(&s); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestApplyOutcomes(t *testing.T) {
	pool := testutil.DB(t)
	accepted := newMessage(t, pool, message.StatusAccepted)
	sent := newMessage(t, pool, message.StatusSent)
	failed := newMessage(t, pool, message.StatusFailed)
	unknown := uuid.New()

	reports := []dlr.Report{
		{MessageID: accepted, Provider: "A", ProviderRef: "r1", Status: "delivered"},
		{MessageID: sent, Provider: "A", ProviderRef: "r2", Status: "undelivered"},
		{MessageID: sent, Provider: "A", ProviderRef: "r2", Status: "delivered"},
		{MessageID: failed, Provider: "A", ProviderRef: "r3", Status: "delivered"},
		{MessageID: unknown, Provider: "A", ProviderRef: "r4", Status: "delivered"},
	}
	got, err := dlr.Apply(context.Background(), pool, reports)
	if err != nil {
		t.Fatal(err)
	}
	want := []dlr.Outcome{dlr.Applied, dlr.Applied, dlr.Duplicate, dlr.IgnoredTerminal, dlr.Unknown}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("report %d: %s, want %s", i, got[i], want[i])
		}
	}
	for id, status := range map[uuid.UUID]string{accepted: "delivered", sent: "undelivered", failed: "failed"} {
		if s := statusOf(t, pool, id); s != status {
			t.Errorf("message %s is %s, want %s", id, s, status)
		}
	}

	again, err := dlr.Apply(context.Background(), pool, reports[:1])
	if err != nil || again[0] != dlr.Duplicate {
		t.Errorf("repeated report: %v, %v", again, err)
	}
	testutil.AssertLedgerConsistent(t, pool)
}

func TestHandler(t *testing.T) {
	pool := testutil.DB(t)
	ctx, cancel := context.WithCancel(context.Background())
	b := dlr.NewBatcher(pool, 500, 5*time.Millisecond, slog.New(slog.DiscardHandler))
	done := make(chan struct{})
	go func() { defer close(done); b.Run(ctx) }()
	t.Cleanup(func() { cancel(); <-done })
	srv := httptest.NewServer(dlr.NewHandler(b, "s3cret", []string{"A", "B"}))
	t.Cleanup(srv.Close)

	post := func(secret string, body map[string]any) (int, map[string]any) {
		raw, _ := json.Marshal(body)
		req, _ := http.NewRequest(http.MethodPost, srv.URL, bytes.NewReader(raw))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set(dlr.SecretHeader, secret)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var out map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&out)
		return resp.StatusCode, out
	}

	id := newMessage(t, pool, message.StatusAccepted)
	valid := map[string]any{"message_id": id.String(), "provider": "A", "provider_ref": "A-1",
		"status": "delivered", "reported_at": "2026-09-21T10:15:31.480Z"}

	if status, _ := post("wrong", valid); status != http.StatusUnauthorized {
		t.Errorf("wrong secret: %d", status)
	}
	status, body := post("s3cret", map[string]any{"message_id": "x", "provider": "C", "status": "lost", "reported_at": "now"})
	if status != http.StatusBadRequest || len(body["error"].(map[string]any)["details"].([]any)) != 5 {
		t.Errorf("invalid report: %d %v", status, body)
	}
	if status, body := post("s3cret", valid); status != http.StatusOK || body["outcome"] != "applied" {
		t.Errorf("valid report: %d %v", status, body)
	}
	if status, body := post("s3cret", valid); status != http.StatusOK || body["outcome"] != "duplicate" {
		t.Errorf("repeated report: %d %v", status, body)
	}
	if s := statusOf(t, pool, id); s != "delivered" {
		t.Errorf("status %s", s)
	}
}

func TestBatcherCommitsConcurrentReports(t *testing.T) {
	pool := testutil.DB(t)
	ctx, cancel := context.WithCancel(context.Background())
	b := dlr.NewBatcher(pool, 50, 20*time.Millisecond, slog.New(slog.DiscardHandler))
	done := make(chan struct{})
	go func() { defer close(done); b.Run(ctx) }()

	ids := make([]uuid.UUID, 120)
	for i := range ids {
		ids[i] = newMessage(t, pool, message.StatusSent)
	}
	var wg sync.WaitGroup
	for _, id := range ids {
		wg.Add(1)
		go func() {
			defer wg.Done()
			out, err := b.Submit(context.Background(), dlr.Report{MessageID: id, Provider: "A", ProviderRef: "r", Status: "delivered"})
			if err != nil || out != dlr.Applied {
				t.Errorf("submit: %v, %v", out, err)
			}
		}()
	}
	wg.Wait()
	cancel()
	<-done
	if _, err := b.Submit(context.Background(), dlr.Report{MessageID: ids[0]}); err != dlr.ErrStopped {
		t.Errorf("submit after stop: %v", err)
	}
	for _, id := range ids {
		if s := statusOf(t, pool, id); s != "delivered" {
			t.Fatalf("message %s is %s", id, s)
		}
	}
}
