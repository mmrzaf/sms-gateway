package fakeprovider

import (
	"net/http"
	"strings"
	"time"

	"github.com/mmrzaf/sms-gatway/internal/httpx"
)

// Deterministic recipient prefixes for tests.
const (
	alwaysRejectPrefix      = "+999"
	alwaysUndeliveredPrefix = "+998"
)

type sendRequest struct {
	ID   string `json:"id"`
	To   string `json:"to"`
	Text string `json:"text"`
}

type sendResponse struct {
	ProviderRef string     `json:"provider_ref"`
	AcceptedAt  httpx.Time `json:"accepted_at"`
}

type sendError struct {
	Error string `json:"error"`
}

// handleSend processes a send request in the documented order: outage,
// deduplication, deterministic rejection, random rejection, random failure,
// acceptance, and finally a simulated timeout or a normal response.
func (p *Provider) handleSend(w http.ResponseWriter, r *http.Request) {
	var req sendRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil || req.ID == "" || req.To == "" {
		httpx.WriteJSON(w, http.StatusBadRequest, sendError{"invalid_request"})
		return
	}
	s := p.Settings()
	p.stats.received.Add(1)
	rec := &record{id: req.ID, to: req.To, text: req.Text, receivedAt: time.Now()}

	if s.Outage {
		p.stats.outage.Add(1)
		rec.result = resultOutage
		p.recent.add(rec)
		httpx.WriteJSON(w, http.StatusServiceUnavailable, sendError{"outage"})
		return
	}

	if !sleep(r.Context(), p.jittered(s.LatencyMS, s.JitterMS)) {
		return // the client gave up
	}

	if a, ok := p.dedup.get(req.ID); ok {
		p.replay(w, a)
		return
	}

	switch {
	case strings.HasPrefix(req.To, alwaysRejectPrefix), p.chance(s.RejectRate):
		p.stats.rejected.Add(1)
		rec.result = resultRejected
		p.recent.add(rec)
		httpx.WriteJSON(w, http.StatusBadRequest, sendError{"invalid_recipient"})
		return
	case p.chance(s.FailureRate):
		p.stats.failed.Add(1)
		rec.result = resultFailed
		p.recent.add(rec)
		httpx.WriteJSON(w, http.StatusInternalServerError, sendError{"internal_error"})
		return
	}

	// Accept. The deduplication store settles concurrent sends of one ID.
	rec.providerRef = p.newRef()
	rec.dlrStatus = dlrPending
	a, added := p.dedup.add(req.ID, acceptance{ref: rec.providerRef, acceptedAt: time.Now(), record: rec})
	if !added {
		p.replay(w, a)
		return
	}
	p.stats.accepted.Add(1)
	p.scheduleDLR(req.ID, rec, s)

	if p.chance(s.TimeoutRate) {
		// The message is accepted, but the caller does not learn it: the
		// response is held past any client timeout. A retry is answered from
		// the deduplication store.
		p.stats.timedOut.Add(1)
		rec.result = resultTimedOut
		p.recent.add(rec)
		if sleep(r.Context(), p.holdFor) {
			// A caller that waited this long gets a dropped connection
			// rather than a response.
			panic(http.ErrAbortHandler)
		}
		return
	}
	rec.result = resultAccepted
	p.recent.add(rec)
	httpx.WriteJSON(w, http.StatusOK, sendResponse{ProviderRef: a.ref, AcceptedAt: httpx.Time(a.acceptedAt)})
}

// replay answers a repeated send with the original result.
func (p *Provider) replay(w http.ResponseWriter, a acceptance) {
	p.stats.duplicates.Add(1)
	p.recent.update(a.record, func(r *record) { r.duplicates++ })
	httpx.WriteJSON(w, http.StatusOK, sendResponse{ProviderRef: a.ref, AcceptedAt: httpx.Time(a.acceptedAt)})
}
