package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mmrzaf/sms-gatway/internal/api"
	"github.com/mmrzaf/sms-gatway/internal/auth"
	"github.com/mmrzaf/sms-gatway/internal/customer"
	"github.com/mmrzaf/sms-gatway/internal/httpx"
	"github.com/mmrzaf/sms-gatway/internal/message"
	"github.com/mmrzaf/sms-gatway/internal/ratelimit"
	"github.com/mmrzaf/sms-gatway/internal/testutil"
)

func newServer(t *testing.T) (*httptest.Server, *pgxpool.Pool) {
	t.Helper()
	pool := testutil.DB(t)
	svc := message.NewService(pool, message.Config{
		Prices: message.Prices{Normal: 1, Express: 3}, MaxSegments: 10, NormalLanes: 16,
		NormalTTL: 24 * time.Hour, ExpressTTL: 5 * time.Minute,
	})
	mux := http.NewServeMux()
	api.New(pool, svc, auth.NewAuthenticator(pool, time.Minute), ratelimit.New(1)).Register(mux)
	mux.HandleFunc("/", httpx.NotFound)
	srv := httptest.NewServer(httpx.Chain(mux, httpx.WithRequestID(slog.New(slog.DiscardHandler)), httpx.WithRecovery()))
	t.Cleanup(srv.Close)
	return srv, pool
}

type response struct {
	status int
	header http.Header
	body   map[string]any
}

func call(t *testing.T, srv *httptest.Server, method, path, key string, body any) response {
	t.Helper()
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, srv.URL+path, r)
	if err != nil {
		t.Fatal(err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	out := response{status: resp.StatusCode, header: resp.Header}
	if err := json.NewDecoder(resp.Body).Decode(&out.body); err != nil && err != io.EOF {
		t.Fatalf("%s %s: decode body: %v", method, path, err)
	}
	return out
}

func errorCode(r response) string {
	e, _ := r.body["error"].(map[string]any)
	code, _ := e["code"].(string)
	return code
}

func TestSendAndGetMessage(t *testing.T) {
	srv, pool := newServer(t)
	_, key := testutil.Customer(t, pool, 10)

	send := map[string]any{"to": "+989121234567", "text": "Your code is 482913", "client_ref": "otp-1"}
	r := call(t, srv, "POST", "/v1/messages", key, send)
	if r.status != http.StatusAccepted {
		t.Fatalf("send: %d %v", r.status, r.body)
	}
	if r.body["status"] != "accepted" || r.body["cost"] != 1.0 || r.body["encoding"] != "gsm7" ||
		r.body["type"] != "normal" || r.body["sent_at"] != nil || r.body["failure_reason"] != nil {
		t.Errorf("unexpected message: %v", r.body)
	}
	if r.header.Get("X-RateLimit-Limit") == "" || r.header.Get("X-RateLimit-Remaining") == "" {
		t.Error("rate-limit headers missing")
	}
	id := r.body["id"].(string)

	again := call(t, srv, "POST", "/v1/messages", key, send)
	if again.status != http.StatusAccepted || again.header.Get("Idempotent-Replayed") != "true" || again.body["id"] != id {
		t.Errorf("replay: %d %v", again.status, again.header)
	}

	got := call(t, srv, "GET", "/v1/messages/"+id, key, nil)
	if got.status != http.StatusOK || got.body["id"] != id {
		t.Errorf("get: %d %v", got.status, got.body)
	}

	_, otherKey := testutil.Customer(t, pool, 0)
	if r := call(t, srv, "GET", "/v1/messages/"+id, otherKey, nil); r.status != http.StatusNotFound {
		t.Errorf("another customer's message: %d", r.status)
	}
	if r := call(t, srv, "GET", "/v1/messages/not-a-uuid", key, nil); r.status != http.StatusNotFound {
		t.Errorf("malformed id: %d", r.status)
	}
}

func TestErrorResponses(t *testing.T) {
	srv, pool := newServer(t)
	_, key := testutil.Customer(t, pool, 0)

	tests := []struct {
		name   string
		key    string
		body   any
		status int
		code   string
	}{
		{"no key", "", map[string]any{"to": "+989121234567", "text": "hi"}, 401, httpx.CodeUnauthorized},
		{"unknown key", auth.GenerateKey(), map[string]any{"to": "+989121234567", "text": "hi"}, 401, httpx.CodeUnauthorized},
		{"no credits", key, map[string]any{"to": "+989121234567", "text": "hi"}, 402, httpx.CodeInsufficientCredits},
		{"invalid fields", key, map[string]any{"to": "0912", "text": ""}, 422, httpx.CodeValidationFailed},
		{"unknown field", key, map[string]any{"to": "+989121234567", "txt": "hi"}, 400, httpx.CodeInvalidJSON},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := call(t, srv, "POST", "/v1/messages", tc.key, tc.body)
			if r.status != tc.status || errorCode(r) != tc.code {
				t.Errorf("got %d %s, want %d %s: %v", r.status, errorCode(r), tc.status, tc.code, r.body)
			}
		})
	}

	r := call(t, srv, "POST", "/v1/messages", key, map[string]any{"to": "0912", "text": ""})
	details := r.body["error"].(map[string]any)["details"].([]any)
	if len(details) != 2 {
		t.Errorf("want 2 field errors, got %v", details)
	}
	if r.body["error"].(map[string]any)["request_id"] == "" {
		t.Error("request_id missing from the error envelope")
	}
}

func TestRateLimit(t *testing.T) {
	srv, pool := newServer(t)
	_, key, err := customer.Create(context.Background(), pool, customer.New{Name: "slow", InitialCredits: 1000, RateLimitRPS: 1})
	if err != nil {
		t.Fatal(err)
	}
	msgs := make([]map[string]any, 500)
	for i := range msgs {
		msgs[i] = map[string]any{"to": "+989121234567", "text": "hi"}
	}
	if r := call(t, srv, "POST", "/v1/messages/batch", key, map[string]any{"messages": msgs}); r.status != http.StatusAccepted {
		t.Fatalf("batch within burst: %d %v", r.status, r.body)
	}
	r := call(t, srv, "POST", "/v1/messages", key, map[string]any{"to": "+989121234567", "text": "hi"})
	if r.status != http.StatusTooManyRequests || errorCode(r) != httpx.CodeRateLimited || r.header.Get("Retry-After") != "1" {
		t.Errorf("over the limit: %d %v %v", r.status, r.header, r.body)
	}
}

func TestBatchEndpoint(t *testing.T) {
	srv, pool := newServer(t)
	_, key := testutil.Customer(t, pool, 100)

	r := call(t, srv, "POST", "/v1/messages/batch", key, map[string]any{"messages": []map[string]any{
		{"to": "+989121234567", "text": "a"},
		{"to": "+989121234568", "text": "b", "type": "express"},
	}})
	if r.status != http.StatusAccepted || r.body["total_cost"] != 4.0 || len(r.body["messages"].([]any)) != 2 {
		t.Fatalf("batch: %d %v", r.status, r.body)
	}

	r = call(t, srv, "POST", "/v1/messages/batch", key, map[string]any{"messages": []map[string]any{
		{"to": "+989121234567", "text": "a"}, {"to": "bad", "text": "b"},
	}})
	details := r.body["error"].(map[string]any)["details"].([]any)
	if r.status != 422 || details[0].(map[string]any)["field"] != "messages[1].to" {
		t.Errorf("invalid item: %d %v", r.status, r.body)
	}

	tooMany := make([]map[string]any, 501)
	if r := call(t, srv, "POST", "/v1/messages/batch", key, map[string]any{"messages": tooMany}); r.status != 422 {
		t.Errorf("501 items: %d", r.status)
	}
}

func TestBalanceAndCharges(t *testing.T) {
	srv, pool := newServer(t)
	_, key := testutil.Customer(t, pool, 10)

	if r := call(t, srv, "GET", "/v1/balance", key, nil); r.status != 200 || r.body["balance"] != 10.0 {
		t.Fatalf("balance: %d %v", r.status, r.body)
	}
	charge := map[string]any{"amount": 5, "client_ref": "topup-1"}
	r := call(t, srv, "POST", "/v1/balance/charges", key, charge)
	if r.status != http.StatusCreated || r.body["balance"] != 15.0 {
		t.Fatalf("charge: %d %v", r.status, r.body)
	}
	r = call(t, srv, "POST", "/v1/balance/charges", key, charge)
	if r.status != http.StatusCreated || r.header.Get("Idempotent-Replayed") != "true" || r.body["balance"] != 15.0 {
		t.Errorf("replay: %d %v", r.status, r.body)
	}
	r = call(t, srv, "POST", "/v1/balance/charges", key, map[string]any{"amount": 6, "client_ref": "topup-1"})
	if r.status != http.StatusConflict || errorCode(r) != httpx.CodeIdempotencyConflict {
		t.Errorf("conflict: %d %v", r.status, r.body)
	}
	r = call(t, srv, "POST", "/v1/balance/charges", key, map[string]any{"amount": 0})
	if r.status != 422 || len(r.body["error"].(map[string]any)["details"].([]any)) != 2 {
		t.Errorf("invalid charge: %d %v", r.status, r.body)
	}

	r = call(t, srv, "GET", "/v1/balance/transactions?kind=charge&limit=1", key, nil)
	if r.status != 200 || len(r.body["data"].([]any)) != 1 || r.body["next_cursor"] == nil {
		t.Fatalf("transactions page 1: %d %v", r.status, r.body)
	}
	next := r.body["next_cursor"].(string)
	r = call(t, srv, "GET", "/v1/balance/transactions?kind=charge&limit=1&cursor="+next, key, nil)
	if r.status != 200 || len(r.body["data"].([]any)) != 1 || r.body["next_cursor"] != nil {
		t.Errorf("transactions page 2: %d %v", r.status, r.body)
	}
}

func TestListMessagesAndSummary(t *testing.T) {
	srv, pool := newServer(t)
	_, key := testutil.Customer(t, pool, 100)
	for i := range 3 {
		typ := "normal"
		if i == 2 {
			typ = "express"
		}
		call(t, srv, "POST", "/v1/messages", key, map[string]any{"to": "+989121234567", "text": fmt.Sprint(i), "type": typ})
	}

	r := call(t, srv, "GET", "/v1/messages?limit=2", key, nil)
	if r.status != 200 || len(r.body["data"].([]any)) != 2 || r.body["next_cursor"] == nil {
		t.Fatalf("list: %d %v", r.status, r.body)
	}
	r = call(t, srv, "GET", "/v1/messages?type=express&status=accepted", key, nil)
	if r.status != 200 || len(r.body["data"].([]any)) != 1 {
		t.Errorf("filtered list: %d %v", r.status, r.body)
	}
	r = call(t, srv, "GET", "/v1/messages?status=queued&limit=0&since=yesterday&cursor=%25", key, nil)
	if r.status != 422 || len(r.body["error"].(map[string]any)["details"].([]any)) != 4 {
		t.Errorf("bad parameters: %d %v", r.status, r.body)
	}

	r = call(t, srv, "GET", "/v1/reports/summary", key, nil)
	totals, _ := r.body["totals"].(map[string]any)
	if r.status != 200 || totals["messages"] != 3.0 || totals["credits_spent"] != 5.0 {
		t.Fatalf("summary: %d %v", r.status, r.body)
	}
	byType := r.body["by_type"].(map[string]any)
	if _, ok := byType["normal"].(map[string]any)["sla_breached"]; ok {
		t.Error("normal summary should not include sla_breached")
	}
	since := time.Now().Add(-40 * 24 * time.Hour).UTC().Format(time.RFC3339)
	if r := call(t, srv, "GET", "/v1/reports/summary?since="+since, key, nil); r.status != 422 {
		t.Errorf("range over 31 days: %d", r.status)
	}
}

func TestAPIReference(t *testing.T) {
	srv, _ := newServer(t)
	resp, err := http.Get(srv.URL + "/openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.HasPrefix(string(body), "openapi: 3.1.0") {
		t.Errorf("spec: %d", resp.StatusCode)
	}
	resp, err = http.Get(srv.URL + "/docs")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/html") {
		t.Errorf("docs: %d", resp.StatusCode)
	}
}
