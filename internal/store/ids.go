package store

import (
	"encoding/binary"
	"time"

	"github.com/google/uuid"
)

// NewID returns a new UUIDv7. Its leading 48 bits are the creation time in
// Unix milliseconds, so IDs sort by creation time.
func NewID() uuid.UUID {
	return uuid.Must(uuid.NewV7())
}

// LowerBound returns the smallest UUIDv7 whose timestamp is t, truncated to
// the millisecond. Every ID generated at or after t compares greater than or
// equal to it, which lets time-range filters use primary-key indexes.
func LowerBound(t time.Time) uuid.UUID {
	var id uuid.UUID
	ms := uint64(t.UnixMilli())
	var ts [8]byte
	binary.BigEndian.PutUint64(ts[:], ms)
	copy(id[0:6], ts[2:8])
	id[6] = 0x70 // version 7, zero random bits
	id[8] = 0x80 // RFC 4122 variant, zero random bits
	return id
}
