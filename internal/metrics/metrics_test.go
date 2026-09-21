package metrics

import (
	"io"
	"net/http/httptest"
	"strings"
	"testing"
)

func scrape(t *testing.T, r *Registry) string {
	t.Helper()
	rec := httptest.NewRecorder()
	r.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))
	body, _ := io.ReadAll(rec.Body)
	return string(body)
}

func TestExposition(t *testing.T) {
	r := NewRegistry()
	c := r.NewCounter("requests_total", "Requests.", "route", "code")
	g := r.NewGauge("depth", "Depth.")
	h := r.NewHistogram("latency_seconds", "Latency.", []float64{0.1, 1}, "route")

	c.With("GET /v1/messages", "200").Add(2)
	c.With("GET /v1/messages", "200").Inc()
	c.With(`we"ird`, "500").Inc()
	g.With().Set(7)
	g.With().Add(-2)
	for _, v := range []float64{0.05, 0.5, 5} {
		h.With("a").Observe(v)
	}

	out := scrape(t, r)
	for _, want := range []string{
		"# TYPE requests_total counter",
		`requests_total{route="GET /v1/messages",code="200"} 3`,
		`requests_total{route="we\"ird",code="500"} 1`,
		"# TYPE depth gauge",
		"depth 5",
		"# TYPE latency_seconds histogram",
		`latency_seconds_bucket{route="a",le="0.1"} 1`,
		`latency_seconds_bucket{route="a",le="1"} 2`,
		`latency_seconds_bucket{route="a",le="+Inf"} 3`,
		`latency_seconds_sum{route="a"} 5.55`,
		`latency_seconds_count{route="a"} 3`,
		"go_goroutines ",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

func TestDuplicateRegistrationPanics(t *testing.T) {
	r := NewRegistry()
	r.NewCounter("x", "X.")
	defer func() {
		if recover() == nil {
			t.Error("expected a panic")
		}
	}()
	r.NewGauge("x", "X.")
}

func TestWrongLabelCountPanics(t *testing.T) {
	r := NewRegistry()
	c := r.NewCounter("x", "X.", "a")
	defer func() {
		if recover() == nil {
			t.Error("expected a panic")
		}
	}()
	c.With("1", "2")
}
