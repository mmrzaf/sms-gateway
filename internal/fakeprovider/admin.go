package fakeprovider

import (
	"net/http"
	"strconv"

	"github.com/mmrzaf/sms-gatway/internal/httpx"
)

func (p *Provider) handleGetConfig(w http.ResponseWriter, _ *http.Request) {
	httpx.WriteJSON(w, http.StatusOK, p.Settings())
}

func (p *Provider) handlePutConfig(w http.ResponseWriter, r *http.Request) {
	var u SettingsUpdate
	if err := httpx.DecodeJSON(w, r, &u); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	p.settingsMu.Lock()
	s, err := u.Apply(p.settings)
	if err == nil {
		p.settings = s
	}
	p.settingsMu.Unlock()
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	p.logger.Info("settings changed", "settings", s)
	httpx.WriteJSON(w, http.StatusOK, s)
}

func (p *Provider) handleMessages(w http.ResponseWriter, r *http.Request) {
	limit := 100
	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 1000 {
			httpx.WriteError(w, r, httpx.ValidationError(httpx.FieldError{
				Field: "limit", Code: "out_of_range", Message: "must be an integer from 1 to 1000"}))
			return
		}
		limit = n
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"data": p.recent.latest(limit)})
}

// Stats are the provider's cumulative counters.
type Stats struct {
	Received   int64 `json:"received"`
	Accepted   int64 `json:"accepted"`
	Duplicates int64 `json:"duplicates"`
	Rejected   int64 `json:"rejected"`
	Failed     int64 `json:"failed"`
	TimedOut   int64 `json:"timed_out"`
	Outage     int64 `json:"outage"`
	DLRSent    int64 `json:"dlr_sent"`
	DLRPending int64 `json:"dlr_pending"`
	DLRFailed  int64 `json:"dlr_failed"`
}

// Stats returns a snapshot of the counters.
func (p *Provider) Stats() Stats {
	c := &p.stats
	return Stats{
		Received:   c.received.Load(),
		Accepted:   c.accepted.Load(),
		Duplicates: c.duplicates.Load(),
		Rejected:   c.rejected.Load(),
		Failed:     c.failed.Load(),
		TimedOut:   c.timedOut.Load(),
		Outage:     c.outage.Load(),
		DLRSent:    c.dlrSent.Load(),
		DLRPending: c.dlrPending.Load(),
		DLRFailed:  c.dlrFailed.Load(),
	}
}

func (p *Provider) handleStats(w http.ResponseWriter, _ *http.Request) {
	httpx.WriteJSON(w, http.StatusOK, p.Stats())
}
