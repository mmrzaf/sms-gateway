package fakeprovider

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mmrzaf/sms-gatway/internal/httpx"
)

// gateway is a stand-in for the gateway's DLR endpoint.
type gateway struct {
	mu      sync.Mutex
	reports []report
	secrets []string
	calls   atomic.Int64
	status  func(call int64) int
}

func newGateway(t *testing.T) (*gateway, *httptest.Server) {
	g := &gateway{status: func(int64) int { return http.StatusOK }}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := g.calls.Add(1)
		var rep report
		_ = json.NewDecoder(r.Body).Decode(&rep)
		g.mu.Lock()
		g.reports = append(g.reports, rep)
		g.secrets = append(g.secrets, r.Header.Get(SecretHeader))
		g.mu.Unlock()
		w.WriteHeader(g.status(n))
	}))
	t.Cleanup(srv.Close)
	return g, srv
}

func (g *gateway) received() []report {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]report(nil), g.reports...)
}

// newTestProvider returns a provider with no latency and immediate reports,
// whose random draws always return draw.
func newTestProvider(t *testing.T, dlrURL string, s Settings, draw float64) (*Provider, *httptest.Server) {
	t.Helper()
	p := New(Config{Name: "A", GatewayDLRURL: dlrURL, Secret: "s3cret", Settings: s}, slog.New(slog.DiscardHandler))
	p.random = func() float64 { return draw }
	p.retryInitial = time.Millisecond
	t.Cleanup(p.Close)
	srv := httptest.NewServer(httpx.Chain(p.Handler(), httpx.WithRecovery()))
	t.Cleanup(srv.Close)
	return p, srv
}

var instant = Settings{DeliveryRatio: 1}

func send(t *testing.T, srv *httptest.Server, id, to string) (int, map[string]any) {
	t.Helper()
	body := fmt.Sprintf(`{"id":%q,"to":%q,"text":"hello"}`, id, to)
	resp, err := http.Post(srv.URL+"/send", "application/json", bytes.NewBufferString(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestAcceptAndReportDelivery(t *testing.T) {
	g, gw := newGateway(t)
	p, srv := newTestProvider(t, gw.URL, instant, 0.5)

	status, body := send(t, srv, "m-1", "+989121234567")
	if status != http.StatusOK || body["provider_ref"] == "" || body["accepted_at"] == nil {
		t.Fatalf("send: %d %v", status, body)
	}
	eventually(t, "the delivery report", func() bool { return len(g.received()) == 1 })

	rep := g.received()[0]
	if rep.MessageID != "m-1" || rep.Provider != "A" || rep.Status != dlrDelivered || rep.ProviderRef != body["provider_ref"] {
		t.Errorf("report: %+v", rep)
	}
	if g.secrets[0] != "s3cret" {
		t.Errorf("secret header = %q", g.secrets[0])
	}
	eventually(t, "the counters", func() bool { return p.Stats().DLRSent == 1 && p.Stats().DLRPending == 0 })
	if st := p.Stats(); st.Received != 1 || st.Accepted != 1 {
		t.Errorf("stats: %+v", st)
	}
}

func TestOutage(t *testing.T) {
	_, gw := newGateway(t)
	p, srv := newTestProvider(t, gw.URL, Settings{Outage: true}, 0.5)
	if status, body := send(t, srv, "m-1", "+989121234567"); status != http.StatusServiceUnavailable || body["error"] != "outage" {
		t.Fatalf("got %d %v", status, body)
	}
	if st := p.Stats(); st.Outage != 1 || st.Accepted != 0 {
		t.Errorf("stats: %+v", st)
	}
	if recs := p.recent.latest(10); len(recs) != 1 || recs[0].Result != resultOutage {
		t.Errorf("records: %+v", recs)
	}
}

func TestDuplicateSendReturnsOriginalResult(t *testing.T) {
	g, gw := newGateway(t)
	p, srv := newTestProvider(t, gw.URL, instant, 0.5)

	_, first := send(t, srv, "m-1", "+989121234567")
	_, second := send(t, srv, "m-1", "+989121234567")
	if first["provider_ref"] != second["provider_ref"] {
		t.Fatalf("refs differ: %v, %v", first, second)
	}
	eventually(t, "the delivery report", func() bool { return p.Stats().DLRSent == 1 })
	time.Sleep(20 * time.Millisecond)
	if n := len(g.received()); n != 1 {
		t.Errorf("%d reports for one message", n)
	}
	st := p.Stats()
	if st.Accepted != 1 || st.Duplicates != 1 {
		t.Errorf("stats: %+v", st)
	}
	if recs := p.recent.latest(10); len(recs) != 1 || recs[0].Duplicates != 1 {
		t.Errorf("records: %+v", recs)
	}
}

func TestRejectionsAndFailures(t *testing.T) {
	_, gw := newGateway(t)
	tests := []struct {
		name     string
		settings Settings
		to       string
		status   int
		errCode  string
	}{
		{"deterministic rejection", instant, "+999123456789", 400, "invalid_recipient"},
		{"random rejection", Settings{RejectRate: 1}, "+989121234567", 400, "invalid_recipient"},
		{"random failure", Settings{FailureRate: 1}, "+989121234567", 500, "internal_error"},
		{"rejection before failure", Settings{RejectRate: 1, FailureRate: 1}, "+989121234567", 400, "invalid_recipient"},
		{"draw above rate", Settings{FailureRate: 0.4, DeliveryRatio: 1}, "+989121234567", 200, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, srv := newTestProvider(t, gw.URL, tc.settings, 0.5)
			status, body := send(t, srv, "m-1", tc.to)
			if status != tc.status || (tc.errCode != "" && body["error"] != tc.errCode) {
				t.Errorf("got %d %v", status, body)
			}
		})
	}
}

func TestUndeliveredPrefix(t *testing.T) {
	g, gw := newGateway(t)
	_, srv := newTestProvider(t, gw.URL, instant, 0.5)
	send(t, srv, "m-1", "+998123456789")
	eventually(t, "the delivery report", func() bool { return len(g.received()) == 1 })
	if st := g.received()[0].Status; st != dlrUndelivered {
		t.Errorf("status = %s", st)
	}
}

func TestTimeoutAcceptsThenHoldsResponse(t *testing.T) {
	_, gw := newGateway(t)
	p, srv := newTestProvider(t, gw.URL, Settings{TimeoutRate: 1, DeliveryRatio: 1}, 0.5)
	p.holdFor = 2 * time.Second

	client := &http.Client{Timeout: 100 * time.Millisecond}
	_, err := client.Post(srv.URL+"/send", "application/json",
		bytes.NewBufferString(`{"id":"m-1","to":"+989121234567","text":"hi"}`))
	if err == nil {
		t.Fatal("expected the request to time out")
	}

	status, body := send(t, srv, "m-1", "+989121234567")
	if status != http.StatusOK || body["provider_ref"] == nil {
		t.Fatalf("retry after timeout: %d %v", status, body)
	}
	if st := p.Stats(); st.Accepted != 1 || st.TimedOut != 1 || st.Duplicates != 1 {
		t.Errorf("stats: %+v", st)
	}
}

func TestReportRetriedUntilAcknowledged(t *testing.T) {
	g, gw := newGateway(t)
	g.status = func(call int64) int {
		if call < 3 {
			return http.StatusServiceUnavailable
		}
		return http.StatusOK
	}
	p, srv := newTestProvider(t, gw.URL, instant, 0.5)
	send(t, srv, "m-1", "+989121234567")
	eventually(t, "the acknowledged report", func() bool { return p.Stats().DLRSent == 1 })
	if n := g.calls.Load(); n != 3 {
		t.Errorf("%d delivery attempts, want 3", n)
	}
}

func TestReportRefusedIsNotRetried(t *testing.T) {
	g, gw := newGateway(t)
	g.status = func(int64) int { return http.StatusUnauthorized }
	p, srv := newTestProvider(t, gw.URL, instant, 0.5)
	send(t, srv, "m-1", "+989121234567")
	eventually(t, "the failed report", func() bool { return p.Stats().DLRFailed == 1 })
	if n := g.calls.Load(); n != 1 {
		t.Errorf("%d delivery attempts, want 1", n)
	}
}

func TestInvalidSendRequest(t *testing.T) {
	_, gw := newGateway(t)
	_, srv := newTestProvider(t, gw.URL, instant, 0.5)
	if status, _ := send(t, srv, "", "+989121234567"); status != http.StatusBadRequest {
		t.Errorf("missing id: %d", status)
	}
}

func TestAdminConfig(t *testing.T) {
	_, gw := newGateway(t)
	p, srv := newTestProvider(t, gw.URL, DefaultSettings, 0.5)

	put := func(body string) (int, map[string]any) {
		req, _ := http.NewRequest(http.MethodPut, srv.URL+"/admin/config", bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var out map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&out)
		return resp.StatusCode, out
	}

	status, body := put(`{"outage": true, "failure_rate": 0.3}`)
	if status != 200 || body["outage"] != true || body["failure_rate"] != 0.3 || body["latency_ms"] != 50.0 {
		t.Fatalf("update: %d %v", status, body)
	}
	if s := p.Settings(); !s.Outage || s.DeliveryRatio != 0.95 {
		t.Errorf("settings: %+v", s)
	}

	status, body = put(`{"failure_rate": 1.5, "dlr_delay_ms": -1}`)
	details, _ := body["error"].(map[string]any)["details"].([]any)
	if status != 422 || len(details) != 2 {
		t.Fatalf("invalid update: %d %v", status, body)
	}
	if p.Settings().FailureRate != 0.3 {
		t.Error("an invalid update changed the settings")
	}

	resp, err := http.Get(srv.URL + "/admin/config")
	if err != nil {
		t.Fatal(err)
	}
	var got Settings
	_ = json.NewDecoder(resp.Body).Decode(&got)
	resp.Body.Close()
	if got != p.Settings() {
		t.Errorf("GET /admin/config = %+v", got)
	}
}

func TestAdminMessagesAndStats(t *testing.T) {
	_, gw := newGateway(t)
	_, srv := newTestProvider(t, gw.URL, instant, 0.5)
	for i := range 3 {
		send(t, srv, fmt.Sprintf("m-%d", i), "+989121234567")
	}

	resp, err := http.Get(srv.URL + "/admin/messages?limit=2")
	if err != nil {
		t.Fatal(err)
	}
	var page struct{ Data []recordJSON }
	_ = json.NewDecoder(resp.Body).Decode(&page)
	resp.Body.Close()
	if len(page.Data) != 2 || page.Data[0].ID != "m-2" || page.Data[0].Result != resultAccepted {
		t.Errorf("messages: %+v", page.Data)
	}

	resp, _ = http.Get(srv.URL + "/admin/messages?limit=5000")
	resp.Body.Close()
	if resp.StatusCode != 422 {
		t.Errorf("limit 5000: %d", resp.StatusCode)
	}

	resp, _ = http.Get(srv.URL + "/admin/stats")
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	var st Stats
	_ = json.Unmarshal(body, &st)
	if st.Received != 3 || st.Accepted != 3 {
		t.Errorf("stats: %s", body)
	}
}

func TestDedupStoreEvictsOldest(t *testing.T) {
	d := newDedupStore(2)
	for _, id := range []string{"a", "b", "c"} {
		d.add(id, acceptance{ref: id})
	}
	if _, ok := d.get("a"); ok {
		t.Error("oldest entry was not evicted")
	}
	for _, id := range []string{"b", "c"} {
		if _, ok := d.get(id); !ok {
			t.Errorf("%s was evicted", id)
		}
	}
	if a, added := d.add("b", acceptance{ref: "other"}); added || a.ref != "b" {
		t.Errorf("re-adding b: %+v, %v", a, added)
	}
}

func TestRecentLogWraps(t *testing.T) {
	l := newRecentLog(3)
	for i := range 5 {
		l.add(&record{id: fmt.Sprint(i)})
	}
	got := l.latest(10)
	if len(got) != 3 || got[0].ID != "4" || got[1].ID != "3" || got[2].ID != "2" {
		t.Errorf("latest = %+v", got)
	}
}
