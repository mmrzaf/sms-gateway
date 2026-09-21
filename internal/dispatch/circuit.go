package dispatch

import (
	"sync"
	"time"
)

// CircuitState is the state of a circuit breaker.
type CircuitState int

// Circuit states. The numeric values are exported as the sms_circuit_state metric.
const (
	CircuitClosed CircuitState = iota
	CircuitHalfOpen
	CircuitOpen
)

func (s CircuitState) String() string {
	switch s {
	case CircuitHalfOpen:
		return "half_open"
	case CircuitOpen:
		return "open"
	default:
		return "closed"
	}
}

// breaker stops traffic to a provider after consecutive failures and lets a
// single probe through after a pause.
//
//	closed    requests flow; a success resets the failure count
//	open      no requests until openFor has elapsed
//	half-open exactly one probe; success closes, failure reopens
type breaker struct {
	threshold int
	openFor   time.Duration
	now       func() time.Time

	mu       sync.Mutex
	state    CircuitState
	failures int
	openedAt time.Time
	probing  bool
}

func newBreaker(threshold int, openFor time.Duration) *breaker {
	return &breaker{threshold: threshold, openFor: openFor, now: time.Now}
}

// allow reports whether a request may be sent now. In half-open state it
// grants the single probe to the first caller.
func (b *breaker) allow() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	switch b.currentLocked() {
	case CircuitClosed:
		return true
	case CircuitHalfOpen:
		if b.probing {
			return false
		}
		b.probing = true
		return true
	default:
		return false
	}
}

// usable reports whether allow could succeed now, without taking the probe.
func (b *breaker) usable() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	switch b.currentLocked() {
	case CircuitClosed:
		return true
	case CircuitHalfOpen:
		return !b.probing
	default:
		return false
	}
}

// nextProbe returns when an open circuit will allow a probe.
func (b *breaker) nextProbe() time.Time {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.openedAt.Add(b.openFor)
}

// record reports the result of an allowed request. counted is false for
// failures that are not the provider's fault; they neither trip nor reset
// the circuit, but they do end a probe.
func (b *breaker) record(success, counted bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	wasProbe := b.probing
	b.probing = false
	switch {
	case success:
		b.state, b.failures = CircuitClosed, 0
	case wasProbe && counted:
		b.state, b.openedAt = CircuitOpen, b.now()
	case counted:
		b.failures++
		if b.failures >= b.threshold {
			b.state, b.openedAt, b.failures = CircuitOpen, b.now(), 0
		}
	}
}

// State returns the current state.
func (b *breaker) State() CircuitState {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.currentLocked()
}

func (b *breaker) currentLocked() CircuitState {
	if b.state == CircuitOpen && !b.now().Before(b.openedAt.Add(b.openFor)) {
		b.state = CircuitHalfOpen
	}
	return b.state
}
