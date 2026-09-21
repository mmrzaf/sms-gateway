package fakeprovider

import (
	"sync"
	"time"
)

// dedupCapacity bounds the deduplication store; the oldest entries are
// evicted first.
const dedupCapacity = 1_000_000

// acceptance is what the provider remembers about an accepted message, so a
// repeated send returns the original result.
type acceptance struct {
	ref        string
	acceptedAt time.Time
	record     *record
}

// dedupStore maps message IDs to their acceptance, evicting in FIFO order.
type dedupStore struct {
	mu       sync.Mutex
	capacity int
	entries  map[string]acceptance
	order    []string // ring of IDs in insertion order
	next     int
}

func newDedupStore(capacity int) *dedupStore {
	return &dedupStore{capacity: capacity, entries: make(map[string]acceptance)}
}

func (d *dedupStore) get(id string) (acceptance, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	a, ok := d.entries[id]
	return a, ok
}

// add records an acceptance unless id is already present. It returns the
// stored acceptance and whether it was newly added, so two concurrent sends
// of the same ID agree on one result.
func (d *dedupStore) add(id string, a acceptance) (acceptance, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if existing, ok := d.entries[id]; ok {
		return existing, false
	}
	if len(d.order) < d.capacity {
		d.order = append(d.order, id)
	} else {
		delete(d.entries, d.order[d.next])
		d.order[d.next] = id
		d.next = (d.next + 1) % d.capacity
	}
	d.entries[id] = a
	return a, true
}
