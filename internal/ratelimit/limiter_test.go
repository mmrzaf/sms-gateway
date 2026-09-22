package ratelimit

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/mmrzaf/sms-gatway/internal/message"
)

type clock struct{ t time.Time }

func (c *clock) now() time.Time          { return c.t }
func (c *clock) advance(d time.Duration) { c.t = c.t.Add(d) }

func newTestLimiter(instances int) (*Limiter, *clock) {
	c := &clock{t: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	l := New(instances)
	l.now = c.now
	return l, c
}

func TestAllowWithinBurst(t *testing.T) {
	l, _ := newTestLimiter(1)
	id := uuid.New()
	// rps 100 gives a burst of max(200, MaxBatchSize).
	d := l.Allow(id, 100, message.MaxBatchSize)
	if !d.Allowed || d.Remaining != 0 || d.Limit != 100 {
		t.Fatalf("full batch from a full bucket: %+v", d)
	}
	d = l.Allow(id, 100, 1)
	if d.Allowed {
		t.Fatalf("empty bucket admitted a message: %+v", d)
	}
	if d.RetryAfter != 10*time.Millisecond {
		t.Errorf("RetryAfter = %v, want 10ms at 100 msg/s", d.RetryAfter)
	}
}

func TestBucketRefills(t *testing.T) {
	l, c := newTestLimiter(1)
	id := uuid.New()
	l.Allow(id, 100, message.MaxBatchSize)
	c.advance(time.Second)
	if d := l.Allow(id, 100, 100); !d.Allowed {
		t.Fatalf("100 tokens should refill in one second: %+v", d)
	}
	if d := l.Allow(id, 100, 1); d.Allowed {
		t.Fatalf("bucket should be empty again: %+v", d)
	}
}

func TestDeniedRequestTakesNoTokens(t *testing.T) {
	l, _ := newTestLimiter(1)
	id := uuid.New()
	l.Allow(id, 100, 450)
	if d := l.Allow(id, 100, 100); d.Allowed {
		t.Fatal("100 messages admitted with 50 tokens left")
	}
	if d := l.Allow(id, 100, 50); !d.Allowed {
		t.Fatal("the denied request consumed tokens")
	}
}

func TestLimitIsSharedAcrossInstances(t *testing.T) {
	l, c := newTestLimiter(4)
	id := uuid.New()
	l.Allow(id, 4000, 2000) // burst is 2 × 1000 = 2000 per instance
	c.advance(time.Second)
	if d := l.Allow(id, 4000, 1000); !d.Allowed {
		t.Fatalf("instance share of 1000 msg/s not admitted: %+v", d)
	}
	if d := l.Allow(id, 4000, 1); d.Allowed {
		t.Fatalf("admitted more than the instance share: %+v", d)
	}
}

func TestChangedLimitTakesEffect(t *testing.T) {
	l, c := newTestLimiter(1)
	id := uuid.New()
	l.Allow(id, 10, message.MaxBatchSize)
	c.advance(time.Second)
	l.Allow(id, 1000, 0) // raise the limit
	c.advance(time.Second)
	if d := l.Allow(id, 1000, 1000); !d.Allowed {
		t.Fatalf("raised limit not applied: %+v", d)
	}
}

func TestCustomersAreIndependent(t *testing.T) {
	l, _ := newTestLimiter(1)
	a, b := uuid.New(), uuid.New()
	l.Allow(a, 100, message.MaxBatchSize)
	if d := l.Allow(b, 100, message.MaxBatchSize); !d.Allowed {
		t.Fatal("one customer's usage affected another")
	}
}

func TestIdleBucketsAreDropped(t *testing.T) {
	l, c := newTestLimiter(1)
	l.Allow(uuid.New(), 100, 1)
	c.advance(2 * idleAfter)
	l.Allow(uuid.New(), 100, 1)
	if len(l.buckets) != 1 {
		t.Errorf("%d buckets kept, want 1", len(l.buckets))
	}
}
