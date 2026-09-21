package api

import (
	"net/http"
	"time"

	"github.com/mmrzaf/sms-gatway/internal/auth"
	"github.com/mmrzaf/sms-gatway/internal/httpx"
	"github.com/mmrzaf/sms-gatway/internal/message"
)

type summaryResponse struct {
	Since    httpx.Time                     `json:"since"`
	Until    httpx.Time                     `json:"until"`
	Totals   summaryTotals                  `json:"totals"`
	ByStatus map[string]int64               `json:"by_status"`
	ByType   map[string]summaryTypeResponse `json:"by_type"`
}

type summaryTotals struct {
	Messages        int64 `json:"messages"`
	Segments        int64 `json:"segments"`
	CreditsSpent    int64 `json:"credits_spent"`
	CreditsRefunded int64 `json:"credits_refunded"`
}

type summaryTypeResponse struct {
	Messages    int64  `json:"messages"`
	Credits     int64  `json:"credits"`
	SLABreached *int64 `json:"sla_breached,omitempty"`
}

func (s *Server) summary(w http.ResponseWriter, r *http.Request, c auth.Customer) error {
	q := httpx.NewQuery(r.URL.Query())
	since, until := q.Time("since"), q.Time("until")
	if err := q.Err(); err != nil {
		return err
	}
	if until.IsZero() {
		until = s.now()
	}
	if since.IsZero() {
		since = until.Add(-24 * time.Hour)
	}
	switch {
	case !since.Before(until):
		return invalid("since", message.CodeOutOfRange, "must be earlier than until")
	case until.Sub(since) > message.MaxReportRange:
		return invalid("since", message.CodeOutOfRange, "the range may not exceed 31 days")
	}

	sum, err := s.messages.Summarize(r.Context(), c.ID, since, until)
	if err != nil {
		return err
	}
	out := summaryResponse{
		Since: httpx.Time(sum.Since),
		Until: httpx.Time(sum.Until),
		Totals: summaryTotals{
			Messages:        sum.Messages,
			Segments:        sum.Segments,
			CreditsSpent:    sum.CreditsSpent,
			CreditsRefunded: sum.CreditsRefunded,
		},
		ByStatus: make(map[string]int64, len(sum.ByStatus)),
		ByType:   make(map[string]summaryTypeResponse, len(sum.ByType)),
	}
	for st, n := range sum.ByStatus {
		out.ByStatus[string(st)] = n
	}
	for t, ts := range sum.ByType {
		tr := summaryTypeResponse{Messages: ts.Messages, Credits: ts.Credits}
		if t == message.Express {
			breached := ts.SLABreached
			tr.SLABreached = &breached
		}
		out.ByType[string(t)] = tr
	}
	httpx.WriteJSON(w, http.StatusOK, out)
	return nil
}
