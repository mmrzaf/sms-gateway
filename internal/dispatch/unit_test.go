package dispatch

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/mmrzaf/sms-gatway/internal/message"
)

func TestBackoff(t *testing.T) {
	p := Policy{BackoffBase: 5 * time.Second, BackoffMax: 10 * time.Minute}
	for attempt, full := range map[int]time.Duration{
		1: 5 * time.Second, 2: 10 * time.Second, 3: 20 * time.Second, 7: 320 * time.Second,
		8: 10 * time.Minute, 60: 10 * time.Minute,
	} {
		lo, hi := p.Backoff(attempt, func() float64 { return 0 }), p.Backoff(attempt, func() float64 { return 1 })
		if lo != full/2 || hi != full {
			t.Errorf("attempt %d: range [%v, %v], want [%v, %v]", attempt, lo, hi, full/2, full)
		}
	}
}

func TestClassifyStatus(t *testing.T) {
	tests := []struct {
		status                   int
		permanent, providerFault bool
	}{
		{http.StatusBadRequest, true, false},
		{http.StatusNotFound, true, false},
		{http.StatusTooManyRequests, false, false},
		{http.StatusInternalServerError, false, true},
		{http.StatusServiceUnavailable, false, true},
	}
	for _, tc := range tests {
		e := classifyStatus(tc.status)
		if e.Permanent != tc.permanent || e.ProviderFault != tc.providerFault {
			t.Errorf("%d: %+v", tc.status, e)
		}
	}
	if e := asSendError(errors.New("connection refused")); e.Permanent || !e.ProviderFault {
		t.Errorf("transport error: %+v", e)
	}
}

func TestBreaker(t *testing.T) {
	now := time.Unix(0, 0)
	b := newBreaker(3, 10*time.Second)
	b.now = func() time.Time { return now }

	for range 2 {
		b.record(false, true)
	}
	b.record(false, false) // not the provider's fault: neither trips nor resets
	if b.State() != CircuitClosed {
		t.Fatal("opened before the threshold")
	}
	b.record(false, true)
	if b.State() != CircuitOpen || b.allow() || b.usable() {
		t.Fatal("should be open after 3 counted failures")
	}

	now = now.Add(10 * time.Second)
	if b.State() != CircuitHalfOpen || !b.allow() {
		t.Fatal("should grant a probe after the open duration")
	}
	if b.allow() || b.usable() {
		t.Fatal("only one probe may be in flight")
	}
	b.record(false, true)
	if b.State() != CircuitOpen {
		t.Fatal("a failed probe should reopen the circuit")
	}

	now = now.Add(10 * time.Second)
	b.allow()
	b.record(true, false)
	if b.State() != CircuitClosed || !b.allow() {
		t.Fatal("a successful probe should close the circuit")
	}
}

func TestPick(t *testing.T) {
	w := &Worker{}
	for _, name := range []string{"A", "B"} {
		w.providers = append(w.providers, &provider{name: name, breaker: newBreaker(1, time.Hour)})
	}
	normal, express := Policy{Class: message.Normal}, Policy{Class: message.Express, Rotate: true}

	for attempts, want := range map[int]string{0: "A", 1: "A", 2: "A"} {
		if p := w.pick(normal, attempts); p.name != want {
			t.Errorf("normal attempt %d: %s", attempts, p.name)
		}
	}
	for attempts, want := range map[int]string{0: "A", 1: "B", 2: "A", 3: "B"} {
		if p := w.pick(express, attempts); p.name != want {
			t.Errorf("express attempt %d: %s", attempts, p.name)
		}
	}

	w.providers[0].breaker.record(false, true) // open A
	if p := w.pick(normal, 0); p.name != "B" {
		t.Errorf("normal with A open: %s", p.name)
	}
	if p := w.pick(express, 0); p.name != "B" {
		t.Errorf("express with A open: %s", p.name)
	}
	w.providers[1].breaker.record(false, true) // open B
	if p := w.pick(normal, 0); p != nil || w.anyUsable() {
		t.Error("no provider should be usable")
	}
}

func TestBudgetReservesExpressCapacity(t *testing.T) {
	b := newBudget(100, 0.2) // shared 80/s, reserved 20/s
	for range 80 {
		if err := b.wait(context.Background(), false); err != nil {
			t.Fatal(err)
		}
	}
	// Normal traffic has used the shared bucket; Express still has its reservation.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()
	if err := b.wait(ctx, false); err == nil {
		t.Error("normal traffic exceeded the shared bucket")
	}
	for range 20 {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
		if err := b.wait(ctx, true); err != nil {
			t.Fatalf("express could not use its reservation: %v", err)
		}
		cancel()
	}
	if n := b.available(true); n < 90 {
		t.Errorf("express available within a second = %d", n)
	}
}
