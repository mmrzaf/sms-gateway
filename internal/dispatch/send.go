package dispatch

import (
	"context"
	"errors"
	"time"

	"github.com/mmrzaf/sms-gatway/internal/message"
	"github.com/mmrzaf/sms-gatway/internal/metrics"
)

type outcomeKind int

const (
	outcomeSent outcomeKind = iota
	outcomeRetry
	outcomeDeferred
	outcomeFailed
	outcomeExpired
)

// outcome is the result of dispatching one job, committed by the completer.
type outcome struct {
	kind        outcomeKind
	job         job
	provider    string
	providerRef string
	err         string
	delay       time.Duration
	reason      message.FailureReason
	// attempted is true when a provider request was made and counts as an attempt.
	attempted bool
}

// dispatch sends one job and decides its outcome. It returns false when the
// send was cut off by shutdown; the job's lease then expires and another
// worker claims it.
func (w *Worker) dispatch(ctx context.Context, pol Policy, j job) (outcome, bool) {
	o := outcome{job: j}
	if !j.ExpiresAt.After(j.dbNow()) {
		o.kind, o.reason = outcomeExpired, message.ReasonExpired
		return o, true
	}

	p := w.pick(pol, j.Attempts)
	if p == nil {
		o.kind, o.delay = outcomeDeferred, w.cfg.CircuitOpenDuration
		return o, true
	}
	if err := p.budget.wait(ctx, pol.Class == message.Express); err != nil {
		p.record(false, false)
		return outcome{}, false
	}

	start := time.Now()
	ref, err := p.client.send(ctx, pol.Timeout, j.ID, j.Recipient, j.Body)
	metrics.ProviderRequestDuration.With(p.name).Observe(time.Since(start).Seconds())
	o.provider, o.attempted = p.name, true
	if err == nil {
		p.record(true, false)
		metrics.DispatchAttempts.With(p.name, string(pol.Class), "sent").Inc()
		o.kind, o.providerRef = outcomeSent, ref
		return o, true
	}
	if ctx.Err() != nil {
		p.record(false, false)
		return outcome{}, false
	}

	se := asSendError(err)
	p.record(false, se.ProviderFault)
	metrics.DispatchAttempts.With(p.name, string(pol.Class), attemptLabel(se)).Inc()
	o.err = "provider " + p.name + ": " + se.Error()
	attempts := j.Attempts + 1

	switch {
	case se.Permanent:
		o.kind, o.reason = outcomeFailed, message.ReasonRejected
	case attempts >= pol.MaxAttempts:
		o.kind, o.reason = outcomeFailed, message.ReasonAttemptsExhausted
	default:
		o.delay = pol.Backoff(attempts, w.random)
		if !j.dbNow().Add(o.delay).Before(j.ExpiresAt) {
			// The next attempt could not happen before the TTL.
			o.kind, o.reason = outcomeExpired, message.ReasonExpired
		} else {
			o.kind = outcomeRetry
		}
	}
	return o, true
}

// attemptLabel names a failed attempt for metrics.
func attemptLabel(se *SendError) string {
	switch {
	case se.Permanent:
		return "rejected"
	case errors.Is(se.Err, context.DeadlineExceeded):
		return "timeout"
	default:
		return "retry"
	}
}

// record reports a request result to the provider's breaker and publishes
// the resulting circuit state.
func (p *provider) record(success, counted bool) {
	p.breaker.record(success, counted)
	metrics.CircuitState.With(p.name).Set(float64(p.breaker.State()))
}

// pick chooses the provider for an attempt. Normal traffic uses the first
// usable provider in priority order, so it moves off the primary only while
// the primary's circuit is open. Express starts at a provider that advances
// with every attempt, so a retry always tries a different provider first.
func (w *Worker) pick(pol Policy, attempts int) *provider {
	n := len(w.providers)
	start := 0
	if pol.Rotate {
		start = attempts % n
	}
	for i := range n {
		if p := w.providers[(start+i)%n]; p.breaker.allow() {
			return p
		}
	}
	return nil
}

// anyUsable reports whether any provider can take a request now.
func (w *Worker) anyUsable() bool {
	for _, p := range w.providers {
		if p.breaker.usable() {
			return true
		}
	}
	return false
}

// earliestProbe returns the first time an open circuit allows a probe.
func (w *Worker) earliestProbe() time.Time {
	var t time.Time
	for _, p := range w.providers {
		if next := p.breaker.nextProbe(); t.IsZero() || next.Before(t) {
			t = next
		}
	}
	return t
}

// budgetAvailable estimates how many messages of the class can start in the
// next second: the first usable provider's budget for normal traffic, which
// uses one provider at a time, and all usable providers' budgets for Express.
func (w *Worker) budgetAvailable(pol Policy) int {
	express := pol.Class == message.Express
	total := 0
	for _, p := range w.providers {
		if !p.breaker.usable() {
			continue
		}
		total += p.budget.available(express)
		if !express {
			break
		}
	}
	return total
}
