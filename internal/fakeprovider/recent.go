package fakeprovider

import (
	"sync"
	"time"

	"github.com/mmrzaf/sms-gatway/internal/httpx"
)

// recentCapacity is how many received messages the admin view keeps.
const recentCapacity = 10_000

// Results of a send request, as shown in the admin view.
const (
	resultAccepted = "accepted"
	resultRejected = "rejected"
	resultFailed   = "failed"
	resultTimedOut = "timed_out"
	resultOutage   = "outage"
)

// DLR states of an accepted message.
const (
	dlrPending     = "pending"
	dlrDelivered   = "delivered"
	dlrUndelivered = "undelivered"
)

// record is one received send request.
type record struct {
	id          string
	to          string
	text        string
	providerRef string
	receivedAt  time.Time
	result      string
	dlrStatus   string
	dlrSentAt   time.Time
	duplicates  int
}

// recentLog keeps the latest records in a ring buffer. Records are updated in
// place when their delivery report is sent or a duplicate arrives.
type recentLog struct {
	mu      sync.Mutex
	records []*record
	next    int
}

func newRecentLog(capacity int) *recentLog {
	return &recentLog{records: make([]*record, 0, capacity)}
}

func (l *recentLog) add(r *record) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.records) < cap(l.records) {
		l.records = append(l.records, r)
		return
	}
	l.records[l.next] = r
	l.next = (l.next + 1) % len(l.records)
}

// update changes a record under the log's lock.
func (l *recentLog) update(r *record, fn func(*record)) {
	l.mu.Lock()
	defer l.mu.Unlock()
	fn(r)
}

// recordJSON is a record in the admin API.
type recordJSON struct {
	ID          string      `json:"id"`
	To          string      `json:"to"`
	Text        string      `json:"text"`
	ProviderRef *string     `json:"provider_ref"`
	ReceivedAt  httpx.Time  `json:"received_at"`
	Result      string      `json:"result"`
	DLRStatus   *string     `json:"dlr_status"`
	DLRSentAt   *httpx.Time `json:"dlr_sent_at"`
	Duplicates  int         `json:"duplicates"`
}

// latest returns up to limit records, newest first.
func (l *recentLog) latest(limit int) []recordJSON {
	l.mu.Lock()
	defer l.mu.Unlock()
	size := len(l.records)
	n := min(limit, size)
	out := make([]recordJSON, 0, n)
	newest := size - 1
	if size == cap(l.records) {
		newest = (l.next - 1 + size) % size // the ring has wrapped
	}
	for i := range n {
		out = append(out, l.records[(newest-i+size)%size].json())
	}
	return out
}

func (r *record) json() recordJSON {
	j := recordJSON{
		ID:         r.id,
		To:         r.to,
		Text:       r.text,
		ReceivedAt: httpx.Time(r.receivedAt),
		Result:     r.result,
		Duplicates: r.duplicates,
	}
	if r.providerRef != "" {
		ref := r.providerRef
		j.ProviderRef = &ref
	}
	if r.dlrStatus != "" {
		st := r.dlrStatus
		j.DLRStatus = &st
	}
	if !r.dlrSentAt.IsZero() {
		t := httpx.Time(r.dlrSentAt)
		j.DLRSentAt = &t
	}
	return j
}
