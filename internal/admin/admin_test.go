package admin_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mmrzaf/sms-gatway/internal/admin"
	"github.com/mmrzaf/sms-gatway/internal/config"
	"github.com/mmrzaf/sms-gatway/internal/fakeprovider"
	"github.com/mmrzaf/sms-gatway/internal/httpx"
	"github.com/mmrzaf/sms-gatway/internal/invariant"
	"github.com/mmrzaf/sms-gatway/internal/message"
	"github.com/mmrzaf/sms-gatway/internal/testutil"
)

const token = "t0ken"

var discard = slog.New(slog.DiscardHandler)

type env struct {
	srv      *httptest.Server
	pool     *pgxpool.Pool
	messages *message.Service
	provider *fakeprovider.Provider
}

func newEnv(t *testing.T) env {
	t.Helper()
	pool := testutil.DB(t)
	messages := message.NewService(pool, message.Config{Prices: message.Prices{Normal: 1, Express: 3},
		MaxSegments: 10, NormalLanes: 2, NormalTTL: time.Hour, ExpressTTL: time.Minute})

	fp := fakeprovider.New(fakeprovider.Config{Name: "A", GatewayDLRURL: "http://127.0.0.1:1", Secret: "s",
		Settings: fakeprovider.DefaultSettings}, discard)
	t.Cleanup(fp.Close)
	providerSrv := httptest.NewServer(fp.Handler())
	t.Cleanup(providerSrv.Close)

	s, err := admin.New(pool, messages, admin.Config{
		Token:               token,
		Providers:           []config.Provider{{Name: "A", URL: providerSrv.URL}, {Name: "B", URL: "http://127.0.0.1:1"}},
		Lanes:               []string{message.ExpressLane, message.NormalLane(0), message.NormalLane(1)},
		DefaultRateLimitRPS: 100,
		Invariants:          invariant.Config{LeaseDuration: 30 * time.Second, SweepInterval: 10 * time.Second},
	}, discard)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	s.Register(mux)
	srv := httptest.NewServer(httpx.Chain(mux, httpx.WithRequestID(discard), httpx.WithRecovery()))
	t.Cleanup(srv.Close)
	return env{srv: srv, pool: pool, messages: messages, provider: fp}
}

func (e env) do(t *testing.T, method, path string, body any) (int, map[string]any) {
	t.Helper()
	var r io.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		r = bytes.NewReader(raw)
	}
	req, _ := http.NewRequest(method, e.srv.URL+path, r)
	req.SetBasicAuth(admin.User, token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func TestRequiresCredentials(t *testing.T) {
	e := newEnv(t)
	for _, path := range []string{"/admin/api/customers", "/dashboard/customers", "/dashboard/static/app.css"} {
		resp, err := http.Get(e.srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized || resp.Header.Get("WWW-Authenticate") == "" {
			t.Errorf("%s without credentials: %d", path, resp.StatusCode)
		}
	}
}

func TestCustomerLifecycle(t *testing.T) {
	e := newEnv(t)
	status, created := e.do(t, "POST", "/admin/api/customers", map[string]any{"name": "acme", "initial_credits": 500, "rate_limit_rps": 50})
	if status != http.StatusCreated || created["balance"] != 500.0 || !strings.HasPrefix(created["api_key"].(string), "sk_") {
		t.Fatalf("create: %d %v", status, created)
	}
	id := created["id"].(string)

	if status, body := e.do(t, "POST", "/admin/api/customers", map[string]any{"rate_limit_rps": 0}); status != 422 ||
		len(body["error"].(map[string]any)["details"].([]any)) != 2 {
		t.Errorf("invalid create: %d %v", status, body)
	}

	status, got := e.do(t, "GET", "/admin/api/customers/"+id, nil)
	if status != 200 || got["stats_24h"] == nil || got["api_key"] != nil {
		t.Errorf("get: %d %v", status, got)
	}
	status, updated := e.do(t, "PATCH", "/admin/api/customers/"+id, map[string]any{"rate_limit_rps": 75})
	if status != 200 || updated["rate_limit_rps"] != 75.0 || updated["name"] != "acme" {
		t.Errorf("update: %d %v", status, updated)
	}
	status, credited := e.do(t, "POST", "/admin/api/customers/"+id+"/credits", map[string]any{"amount": 25})
	if status != http.StatusCreated || credited["balance"] != 525.0 {
		t.Errorf("credits: %d %v", status, credited)
	}
	status, rotated := e.do(t, "POST", "/admin/api/customers/"+id+"/rotate-key", nil)
	if status != 200 || rotated["api_key"] == created["api_key"] {
		t.Errorf("rotate: %d %v", status, rotated)
	}
	if status, list := e.do(t, "GET", "/admin/api/customers", nil); status != 200 || len(list["data"].([]any)) != 1 {
		t.Errorf("list: %d %v", status, list)
	}
	if status, _ := e.do(t, "DELETE", "/admin/api/customers/"+id, nil); status != http.StatusNoContent {
		t.Errorf("delete: %d", status)
	}
	if status, _ := e.do(t, "GET", "/admin/api/customers/"+id, nil); status != http.StatusNotFound {
		t.Errorf("get after delete: %d", status)
	}
	testutil.AssertInvariants(t, e.pool)
}

func TestMessagesSystemAndInvariants(t *testing.T) {
	e := newEnv(t)
	c, _ := testutil.Customer(t, e.pool, 100)
	m, _, err := e.messages.Accept(context.Background(), c.ID, message.Request{To: "+989121234567", Text: "hi", Type: message.Express})
	if err != nil {
		t.Fatal(err)
	}

	status, list := e.do(t, "GET", "/admin/api/messages?customer_id="+c.ID.String(), nil)
	if status != 200 || len(list["data"].([]any)) != 1 {
		t.Fatalf("list: %d %v", status, list)
	}
	status, got := e.do(t, "GET", "/admin/api/messages/"+m.ID.String(), nil)
	queue, _ := got["queue"].(map[string]any)
	if status != 200 || got["customer_name"] != c.Name || queue["lane"] != message.ExpressLane {
		t.Errorf("get: %d %v", status, got)
	}
	if status, _ := e.do(t, "GET", "/admin/api/messages?status=queued", nil); status != 422 {
		t.Errorf("bad filter: %d", status)
	}

	status, sys := e.do(t, "GET", "/admin/api/system", nil)
	lanes, _ := sys["queue"].([]any)
	if status != 200 || len(lanes) != 3 || lanes[0].(map[string]any)["ready"] != 1.0 {
		t.Errorf("system: %d %v", status, sys)
	}
	status, inv := e.do(t, "GET", "/admin/api/invariants", nil)
	if status != 200 || inv["ok"] != true {
		t.Errorf("invariants: %d %v", status, inv)
	}
}

func TestProviderProxy(t *testing.T) {
	e := newEnv(t)
	status, cfg := e.do(t, "PUT", "/admin/api/providers/A/config", map[string]any{"outage": true})
	if status != 200 || cfg["outage"] != true || !e.provider.Settings().Outage {
		t.Fatalf("update provider: %d %v", status, cfg)
	}
	if status, stats := e.do(t, "GET", "/admin/api/providers/A/stats", nil); status != 200 || stats["received"] == nil {
		t.Errorf("stats: %d %v", status, stats)
	}
	if status, _ := e.do(t, "GET", "/admin/api/providers/B/config", nil); status != http.StatusBadGateway {
		t.Errorf("unreachable provider: %d", status)
	}
	if status, _ := e.do(t, "GET", "/admin/api/providers/Z/config", nil); status != http.StatusNotFound {
		t.Errorf("unknown provider: %d", status)
	}
}

func (e env) page(t *testing.T, method, path string, form url.Values) (int, string, string) {
	t.Helper()
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req, _ := http.NewRequest(method, e.srv.URL+path, body)
	req.SetBasicAuth(admin.User, token)
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, resp.Header.Get("Location"), string(raw)
}

func TestDashboardPages(t *testing.T) {
	e := newEnv(t)
	c, _ := testutil.Customer(t, e.pool, 100)
	m, _, err := e.messages.Accept(context.Background(), c.ID, message.Request{To: "+989121234567", Text: "hello there"})
	if err != nil {
		t.Fatal(err)
	}

	for path, want := range map[string]string{
		"/dashboard/customers":                  c.Name,
		"/dashboard/customers/" + c.ID.String(): "Recent transactions",
		"/dashboard/messages":                   c.Name,
		"/dashboard/messages/" + m.ID.String():  "hello there",
		"/dashboard/system":                     "Invariants",
		"/dashboard/system?check=1":             "All checks passed",
		"/dashboard/system/panel":               "normal-1",
		"/dashboard/providers/A":                "Simulation",
		"/dashboard/providers/A/panel":          "Received messages",
		"/dashboard/providers/B":                "unreachable",
		"/dashboard/static/app.js":              "data-poll",
	} {
		status, _, body := e.page(t, "GET", path, nil)
		if status != 200 || !strings.Contains(body, want) {
			t.Errorf("%s: %d, missing %q", path, status, want)
		}
	}
	if status, location, _ := e.page(t, "GET", "/dashboard/", nil); status != http.StatusSeeOther || location != "/dashboard/customers" {
		t.Errorf("index: %d %s", status, location)
	}
	if status, _, body := e.page(t, "GET", "/dashboard/customers/not-an-id", nil); status != 404 || !strings.Contains(body, "does not exist") {
		t.Errorf("missing customer: %d", status)
	}
}

func TestDashboardForms(t *testing.T) {
	e := newEnv(t)
	status, _, body := e.page(t, "POST", "/dashboard/customers", url.Values{"name": {"bulkco"}, "initial_credits": {"1000"}, "rate_limit_rps": {"2000"}})
	if status != 200 || !strings.Contains(body, "shown only now") || !strings.Contains(body, "sk_") {
		t.Fatalf("create: %d", status)
	}
	var id string
	if err := e.pool.QueryRow(context.Background(), `SELECT id FROM customers WHERE name = 'bulkco'`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	base := "/dashboard/customers/" + id

	if status, location, _ := e.page(t, "POST", base+"/credits", url.Values{"amount": {"50"}}); status != 303 || location != base {
		t.Errorf("credits: %d %s", status, location)
	}
	if status, _, _ := e.page(t, "POST", base+"/update", url.Values{"rate_limit_rps": {"0"}}); status != 422 {
		t.Errorf("invalid update: %d", status)
	}
	if status, _, body := e.page(t, "POST", base+"/rotate-key", url.Values{}); status != 200 || !strings.Contains(body, "shown only now") {
		t.Errorf("rotate: %d", status)
	}
	if status, _, _ := e.page(t, "POST", "/dashboard/providers/A/config", url.Values{"failure_rate": {"0.25"}, "outage": {"on"}}); status != 303 {
		t.Errorf("provider config: %d", status)
	}
	if s := e.provider.Settings(); s.FailureRate != 0.25 || !s.Outage {
		t.Errorf("provider settings: %+v", s)
	}
	if status, location, _ := e.page(t, "POST", base+"/delete", url.Values{}); status != 303 || location != "/dashboard/customers" {
		t.Errorf("delete: %d %s", status, location)
	}

	var balance int64
	err := e.pool.QueryRow(context.Background(), `SELECT count(*) FROM customers`).Scan(&balance)
	if err != nil || balance != 0 {
		t.Errorf("customers left: %d", balance)
	}
}
