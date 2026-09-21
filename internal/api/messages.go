package api

import (
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"

	"github.com/mmrzaf/sms-gatway/internal/auth"
	"github.com/mmrzaf/sms-gatway/internal/httpx"
	"github.com/mmrzaf/sms-gatway/internal/message"
	"github.com/mmrzaf/sms-gatway/internal/metrics"
)

// replayedHeader marks a response that repeats an earlier result.
const replayedHeader = "Idempotent-Replayed"

type sendRequest struct {
	To        string  `json:"to"`
	Text      string  `json:"text"`
	Type      string  `json:"type"`
	ClientRef *string `json:"client_ref"`
}

func (r sendRequest) toMessage() message.Request {
	return message.Request{To: r.To, Text: r.Text, Type: message.Type(r.Type), ClientRef: r.ClientRef}
}

type batchRequest struct {
	Messages []sendRequest `json:"messages"`
}

type messageResponse struct {
	ID            string      `json:"id"`
	Type          string      `json:"type"`
	To            string      `json:"to"`
	Text          string      `json:"text"`
	Encoding      string      `json:"encoding"`
	Segments      int         `json:"segments"`
	Cost          int64       `json:"cost"`
	Status        string      `json:"status"`
	Attempts      int         `json:"attempts"`
	FailureReason *string     `json:"failure_reason"`
	SLABreached   bool        `json:"sla_breached"`
	ClientRef     *string     `json:"client_ref"`
	AcceptedAt    httpx.Time  `json:"accepted_at"`
	SentAt        *httpx.Time `json:"sent_at"`
	CompletedAt   *httpx.Time `json:"completed_at"`
}

func toMessageResponse(m message.Message) messageResponse {
	var reason *string
	if m.FailureReason != nil {
		r := string(*m.FailureReason)
		reason = &r
	}
	return messageResponse{
		ID:            m.ID.String(),
		Type:          string(m.Type),
		To:            m.Recipient,
		Text:          m.Body,
		Encoding:      string(m.Encoding),
		Segments:      m.Segments,
		Cost:          m.Cost,
		Status:        string(m.Status),
		Attempts:      m.Attempts,
		FailureReason: reason,
		SLABreached:   m.SLABreached,
		ClientRef:     m.ClientRef,
		AcceptedAt:    httpx.Time(m.AcceptedAt),
		SentAt:        httpx.TimePtr(m.SentAt),
		CompletedAt:   httpx.TimePtr(m.CompletedAt),
	}
}

func (s *Server) sendMessage(w http.ResponseWriter, r *http.Request, c auth.Customer) error {
	var req sendRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	if err := s.rateLimit(w, c, 1); err != nil {
		return err
	}
	m, replayed, err := s.messages.Accept(r.Context(), c.ID, req.toMessage())
	if err != nil {
		return err
	}
	if replayed {
		w.Header().Set(replayedHeader, "true")
	} else {
		recordAccepted(m)
	}
	httpx.WriteJSON(w, http.StatusAccepted, toMessageResponse(m))
	return nil
}

type batchResponse struct {
	Messages  []messageResponse `json:"messages"`
	TotalCost int64             `json:"total_cost"`
}

func (s *Server) sendBatch(w http.ResponseWriter, r *http.Request, c auth.Customer) error {
	var req batchRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	if len(req.Messages) > message.MaxBatchSize {
		// Rejected before rate limiting so an oversized batch costs no tokens.
		return invalid("messages", message.CodeTooManyItems, "must contain at most 500 messages")
	}
	if err := s.rateLimit(w, c, max(len(req.Messages), 1)); err != nil {
		return err
	}
	reqs := make([]message.Request, len(req.Messages))
	for i, m := range req.Messages {
		reqs[i] = m.toMessage()
	}
	res, err := s.messages.AcceptBatch(r.Context(), c.ID, reqs)
	if err != nil {
		return err
	}
	if res.Replayed {
		w.Header().Set(replayedHeader, "true")
	} else {
		for _, m := range res.Messages {
			recordAccepted(m)
		}
	}
	out := batchResponse{Messages: make([]messageResponse, len(res.Messages)), TotalCost: res.TotalCost}
	for i, m := range res.Messages {
		out.Messages[i] = toMessageResponse(m)
	}
	httpx.WriteJSON(w, http.StatusAccepted, out)
	return nil
}

func (s *Server) getMessage(w http.ResponseWriter, r *http.Request, c auth.Customer) error {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		return message.ErrNotFound
	}
	m, err := s.messages.Get(r.Context(), c.ID, id)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, toMessageResponse(m))
	return nil
}

func (s *Server) listMessages(w http.ResponseWriter, r *http.Request, c auth.Customer) error {
	q := httpx.NewQuery(r.URL.Query())
	f := message.Filter{
		Recipient: q.String("recipient"),
		Since:     q.Time("since"),
		Until:     q.Time("until"),
		After:     q.Cursor(),
		Limit:     q.Limit(defaultLimit, maxLimit),
	}
	if v := q.String("status"); v != "" {
		st, ok := message.ParseStatus(v)
		if !ok {
			q.Fail("status", httpx.FieldInvalidFormat, "must be a message status")
		}
		f.Status = st
	}
	if v := q.String("type"); v != "" {
		t, ok := message.ParseType(v)
		if !ok {
			q.Fail("type", httpx.FieldInvalidFormat, "must be normal or express")
		}
		f.Type = t
	}
	if f.Recipient != "" && !message.ValidRecipient(f.Recipient) {
		q.Fail("recipient", httpx.FieldInvalidFormat, "must be an E.164 phone number")
	}
	q.TimeRange(f.Since, f.Until)
	if err := q.Err(); err != nil {
		return err
	}

	list, more, err := s.messages.List(r.Context(), c.ID, f)
	if err != nil {
		return err
	}
	data := make([]messageResponse, len(list))
	for i, m := range list {
		data[i] = toMessageResponse(m)
	}
	httpx.WriteJSON(w, http.StatusOK, httpx.NewPage(data, more, func() uuid.UUID { return list[len(list)-1].ID }))
	return nil
}

// rateLimit takes n tokens from the customer's bucket and sets the rate-limit
// headers. It returns a 429 error when the bucket does not hold n tokens.
func (s *Server) rateLimit(w http.ResponseWriter, c auth.Customer, n int) error {
	d := s.limiter.Allow(c.ID, c.RateLimitRPS, n)
	w.Header().Set("X-RateLimit-Limit", strconv.Itoa(d.Limit))
	w.Header().Set("X-RateLimit-Remaining", strconv.Itoa(d.Remaining))
	if d.Allowed {
		return nil
	}
	metrics.RateLimited.Add(float64(n))
	w.Header().Set("Retry-After", strconv.Itoa(retryAfterSeconds(d.RetryAfter)))
	return httpx.NewError(http.StatusTooManyRequests, httpx.CodeRateLimited,
		"The message rate limit was exceeded. Retry after the time in the Retry-After header.")
}

func recordAccepted(m message.Message) {
	metrics.MessagesAccepted.With(string(m.Type)).Inc()
	metrics.CreditsDebited.Add(float64(m.Cost))
}

// retryAfterSeconds rounds a wait up to whole seconds, between 1 and 60.
func retryAfterSeconds(d time.Duration) int {
	secs := math.Ceil(d.Seconds())
	return int(min(max(secs, 1), 60))
}
