package store

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestNewIDIsVersion7(t *testing.T) {
	id := NewID()
	if id.Version() != 7 {
		t.Fatalf("version = %d, want 7", id.Version())
	}
	if id.Variant() != uuid.RFC4122 {
		t.Fatalf("variant = %v, want RFC4122", id.Variant())
	}
}

func TestLowerBoundOrdersAgainstGeneratedIDs(t *testing.T) {
	before := LowerBound(time.Now().Add(-time.Millisecond))
	id := NewID()
	after := LowerBound(time.Now().Add(2 * time.Millisecond))

	if !(before.String() <= id.String()) {
		t.Errorf("lower bound %s should not exceed id %s", before, id)
	}
	if !(id.String() < after.String()) {
		t.Errorf("id %s should be below bound %s", id, after)
	}
}

func TestLowerBoundEncodesMilliseconds(t *testing.T) {
	ts := time.UnixMilli(1_758_000_000_123)
	id := LowerBound(ts)
	sec, nsec := id.Time().UnixTime()
	got := time.Unix(sec, nsec)
	if !got.Equal(ts) {
		t.Errorf("decoded time %v, want %v", got, ts)
	}
	if id.Version() != 7 || id.Variant() != uuid.RFC4122 {
		t.Errorf("bound %s is not a valid UUIDv7", id)
	}
}

func TestCursorRoundTrip(t *testing.T) {
	id := NewID()
	got, err := DecodeCursor(EncodeCursor(id))
	if err != nil || got != id {
		t.Fatalf("round trip: got %s, %v; want %s", got, err, id)
	}
	if got, err := DecodeCursor(""); err != nil || got != uuid.Nil {
		t.Errorf("empty cursor: got %s, %v", got, err)
	}
	for _, bad := range []string{"%%%", "c2hvcnQ"} {
		if _, err := DecodeCursor(bad); err != ErrInvalidCursor {
			t.Errorf("DecodeCursor(%q) error = %v, want ErrInvalidCursor", bad, err)
		}
	}
}
