package admin

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/mmrzaf/sms-gatway/internal/billing"
	"github.com/mmrzaf/sms-gatway/internal/customer"
	"github.com/mmrzaf/sms-gatway/internal/httpx"
	"github.com/mmrzaf/sms-gatway/internal/invariant"
	"github.com/mmrzaf/sms-gatway/internal/message"
	"github.com/mmrzaf/sms-gatway/internal/metrics"
	"github.com/mmrzaf/sms-gatway/internal/monitor"
)

type customerJSON struct {
	ID           string       `json:"id"`
	Name         string       `json:"name"`
	APIKeyPrefix string       `json:"api_key_prefix"`
	Balance      int64        `json:"balance"`
	RateLimitRPS int          `json:"rate_limit_rps"`
	CreatedAt    httpx.Time   `json:"created_at"`
	UpdatedAt    httpx.Time   `json:"updated_at"`
	Stats24h     *customerDay `json:"stats_24h,omitempty"`
	APIKey       string       `json:"api_key,omitempty"`
}

type customerDay struct {
	Messages     int64            `json:"messages"`
	ByStatus     map[string]int64 `json:"by_status"`
	CreditsSpent int64            `json:"credits_spent"`
}

func toCustomerJSON(c customer.Customer) customerJSON {
	return customerJSON{
		ID:           c.ID.String(),
		Name:         c.Name,
		APIKeyPrefix: c.APIKeyPrefix,
		Balance:      c.Balance,
		RateLimitRPS: c.RateLimitRPS,
		CreatedAt:    httpx.Time(c.CreatedAt),
		UpdatedAt:    httpx.Time(c.UpdatedAt),
	}
}

func pathID(r *http.Request, notFound error) (uuid.UUID, error) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		return uuid.Nil, notFound
	}
	return id, nil
}

// customerRequest holds the fields of a create or update request.
type customerRequest struct {
	Name           *string `json:"name"`
	InitialCredits *int64  `json:"initial_credits"`
	RateLimitRPS   *int    `json:"rate_limit_rps"`
}

// validate checks the fields present in the request. creating requires a name.
func (req customerRequest) validate(creating bool) error {
	var problems []message.FieldError
	fail := func(field, code, msg string) {
		problems = append(problems, message.FieldError{Field: field, Code: code, Message: msg})
	}
	switch {
	case req.Name == nil && creating:
		fail("name", message.CodeRequired, "is required")
	case req.Name != nil && (len(*req.Name) < 1 || len(*req.Name) > 100):
		fail("name", message.CodeOutOfRange, "must be 1-100 characters")
	}
	if req.InitialCredits != nil && (*req.InitialCredits < 0 || *req.InitialCredits > billing.MaxChargeAmount) {
		fail("initial_credits", message.CodeOutOfRange, fmt.Sprintf("must be from 0 to %d", billing.MaxChargeAmount))
	}
	if req.RateLimitRPS != nil && (*req.RateLimitRPS < 1 || *req.RateLimitRPS > 100_000) {
		fail("rate_limit_rps", message.CodeOutOfRange, "must be from 1 to 100000")
	}
	if len(problems) > 0 {
		return &message.ValidationError{Fields: problems}
	}
	return nil
}

func (s *Server) newCustomer(ctx context.Context, req customerRequest) (customer.Customer, string, error) {
	n := customer.New{Name: *req.Name, RateLimitRPS: s.cfg.DefaultRateLimitRPS}
	if req.InitialCredits != nil {
		n.InitialCredits = *req.InitialCredits
	}
	if req.RateLimitRPS != nil {
		n.RateLimitRPS = *req.RateLimitRPS
	}
	c, key, err := customer.Create(ctx, s.db, n)
	if err == nil && n.InitialCredits > 0 {
		metrics.CreditsCharged.Add(float64(n.InitialCredits))
	}
	return c, key, err
}

func (s *Server) listCustomers(w http.ResponseWriter, r *http.Request) error {
	q := httpx.NewQuery(r.URL.Query())
	after, limit := q.Cursor(), q.Limit(50, 200)
	if err := q.Err(); err != nil {
		return err
	}
	list, more, err := customer.List(r.Context(), s.db, after, limit)
	if err != nil {
		return err
	}
	data := make([]customerJSON, len(list))
	for i, c := range list {
		data[i] = toCustomerJSON(c)
	}
	httpx.WriteJSON(w, http.StatusOK, httpx.NewPage(data, more, func() uuid.UUID { return list[len(list)-1].ID }))
	return nil
}

func (s *Server) createCustomer(w http.ResponseWriter, r *http.Request) error {
	var req customerRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	if err := req.validate(true); err != nil {
		return err
	}
	c, key, err := s.newCustomer(r.Context(), req)
	if err != nil {
		return err
	}
	out := toCustomerJSON(c)
	out.APIKey = key
	httpx.WriteJSON(w, http.StatusCreated, out)
	return nil
}

// customerDetail loads a customer with its statistics for the last 24 hours.
func (s *Server) customerDetail(ctx context.Context, id uuid.UUID) (customerJSON, error) {
	c, err := customer.Get(ctx, s.db, id)
	if err != nil {
		return customerJSON{}, err
	}
	now := s.now()
	sum, err := s.messages.Summarize(ctx, id, now.Add(-24*time.Hour), now.Add(time.Second))
	if err != nil {
		return customerJSON{}, err
	}
	out := toCustomerJSON(c)
	out.Stats24h = &customerDay{Messages: sum.Messages, ByStatus: map[string]int64{}, CreditsSpent: sum.CreditsSpent}
	for st, n := range sum.ByStatus {
		out.Stats24h.ByStatus[string(st)] = n
	}
	return out, nil
}

func (s *Server) getCustomer(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, customer.ErrNotFound)
	if err != nil {
		return err
	}
	out, err := s.customerDetail(r.Context(), id)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, out)
	return nil
}

func (s *Server) updateCustomer(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, customer.ErrNotFound)
	if err != nil {
		return err
	}
	var req struct {
		Name         *string `json:"name"`
		RateLimitRPS *int    `json:"rate_limit_rps"`
	}
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	if err := (customerRequest{Name: req.Name, RateLimitRPS: req.RateLimitRPS}).validate(false); err != nil {
		return err
	}
	c, err := customer.Update(r.Context(), s.db, id, req.Name, req.RateLimitRPS)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, toCustomerJSON(c))
	return nil
}

func (s *Server) deleteCustomer(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, customer.ErrNotFound)
	if err != nil {
		return err
	}
	if err := customer.Delete(r.Context(), s.db, id); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *Server) rotateKey(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, customer.ErrNotFound)
	if err != nil {
		return err
	}
	c, key, err := customer.RotateKey(r.Context(), s.db, id)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"api_key": key, "api_key_prefix": c.APIKeyPrefix})
	return nil
}

type transactionJSON struct {
	ID        string     `json:"id"`
	Kind      string     `json:"kind"`
	Amount    int64      `json:"amount"`
	MessageID *string    `json:"message_id"`
	ClientRef *string    `json:"client_ref"`
	CreatedAt httpx.Time `json:"created_at"`
}

func toTransactionJSON(t billing.Transaction) transactionJSON {
	var messageID *string
	if t.MessageID != nil {
		id := t.MessageID.String()
		messageID = &id
	}
	return transactionJSON{ID: t.ID.String(), Kind: string(t.Kind), Amount: t.Amount,
		MessageID: messageID, ClientRef: t.ClientRef, CreatedAt: httpx.Time(t.CreatedAt)}
}

// addCredit charges credits to a customer as an operator.
func (s *Server) addCredit(ctx context.Context, id uuid.UUID, amount int64) (billing.ChargeResult, error) {
	if amount < 1 || amount > billing.MaxChargeAmount {
		return billing.ChargeResult{}, &message.ValidationError{Fields: []message.FieldError{{Field: "amount",
			Code: message.CodeOutOfRange, Message: fmt.Sprintf("must be from 1 to %d", billing.MaxChargeAmount)}}}
	}
	res, err := billing.ChargeCustomer(ctx, s.db, id, amount, customer.AdminChargeRef())
	if err == nil {
		metrics.CreditsCharged.Add(float64(amount))
	}
	return res, err
}

func (s *Server) addCredits(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, customer.ErrNotFound)
	if err != nil {
		return err
	}
	var req struct {
		Amount int64 `json:"amount"`
	}
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	res, err := s.addCredit(r.Context(), id, req.Amount)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusCreated, map[string]any{"transaction": toTransactionJSON(res.Transaction), "balance": res.Balance})
	return nil
}

type messageJSON struct {
	ID            string      `json:"id"`
	CustomerID    string      `json:"customer_id"`
	CustomerName  string      `json:"customer_name"`
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
	Provider      *string     `json:"provider"`
	ProviderRef   *string     `json:"provider_ref"`
	LastError     *string     `json:"last_error"`
	AcceptedAt    httpx.Time  `json:"accepted_at"`
	ExpiresAt     httpx.Time  `json:"expires_at"`
	SentAt        *httpx.Time `json:"sent_at"`
	CompletedAt   *httpx.Time `json:"completed_at"`
	Queue         *queueJSON  `json:"queue,omitempty"`
}

type queueJSON struct {
	Lane          string     `json:"lane"`
	NextAttemptAt httpx.Time `json:"next_attempt_at"`
	LeaseOwner    *string    `json:"lease_owner"`
}

func toMessageJSON(in message.Inspected) messageJSON {
	m := in.Message
	var reason *string
	if m.FailureReason != nil {
		r := string(*m.FailureReason)
		reason = &r
	}
	return messageJSON{
		ID: m.ID.String(), CustomerID: m.CustomerID.String(), CustomerName: in.CustomerName,
		Type: string(m.Type), To: m.Recipient, Text: m.Body, Encoding: string(m.Encoding),
		Segments: m.Segments, Cost: m.Cost, Status: string(m.Status), Attempts: m.Attempts,
		FailureReason: reason, SLABreached: m.SLABreached, ClientRef: m.ClientRef,
		Provider: m.Provider, ProviderRef: m.ProviderRef, LastError: m.LastError,
		AcceptedAt: httpx.Time(m.AcceptedAt), ExpiresAt: httpx.Time(m.ExpiresAt),
		SentAt: httpx.TimePtr(m.SentAt), CompletedAt: httpx.TimePtr(m.CompletedAt),
	}
}

// messageFilter parses the message filters shared by the API and dashboard.
func messageFilter(q *httpx.Query) message.AdminFilter {
	f := message.AdminFilter{CustomerID: q.UUID("customer_id")}
	f.Since, f.Until, f.After, f.Limit = q.Time("since"), q.Time("until"), q.Cursor(), q.Limit(50, 200)
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
	q.TimeRange(f.Since, f.Until)
	return f
}

func (s *Server) listMessages(w http.ResponseWriter, r *http.Request) error {
	q := httpx.NewQuery(r.URL.Query())
	f := messageFilter(q)
	if err := q.Err(); err != nil {
		return err
	}
	list, more, err := s.messages.ListAll(r.Context(), f, s.now())
	if err != nil {
		return err
	}
	data := make([]messageJSON, len(list))
	for i, m := range list {
		data[i] = toMessageJSON(m)
	}
	httpx.WriteJSON(w, http.StatusOK, httpx.NewPage(data, more, func() uuid.UUID { return list[len(list)-1].ID }))
	return nil
}

func (s *Server) getMessage(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, message.ErrNotFound)
	if err != nil {
		return err
	}
	in, err := s.messages.Inspect(r.Context(), id)
	if err != nil {
		return err
	}
	out := toMessageJSON(in)
	if in.Queue != nil {
		out.Queue = &queueJSON{Lane: in.Queue.Lane, NextAttemptAt: httpx.Time(in.Queue.NextAttemptAt), LeaseOwner: in.Queue.LeaseOwner}
	}
	httpx.WriteJSON(w, http.StatusOK, out)
	return nil
}

// Overview is the system summary shown on the dashboard.
type Overview struct {
	GeneratedAt    httpx.Time             `json:"generated_at"`
	Queue          []monitor.LaneStats    `json:"queue"`
	Workers        []monitor.Worker       `json:"workers"`
	Throughput     map[string]float64     `json:"throughput_per_s"`
	ExpressLatency monitor.ExpressLatency `json:"express_latency_s"`
}

func (s *Server) overview(ctx context.Context) (Overview, error) {
	now := s.now()
	queue, err := monitor.QueueStats(ctx, s.db, s.cfg.Lanes)
	if err != nil {
		return Overview{}, err
	}
	workers, err := monitor.Workers(ctx, s.db)
	if err != nil {
		return Overview{}, err
	}
	accepted, err := monitor.AcceptedPerSecond(ctx, s.db, now)
	if err != nil {
		return Overview{}, err
	}
	express, err := monitor.Express(ctx, s.db, now, 5*time.Minute)
	if err != nil {
		return Overview{}, err
	}
	if workers == nil {
		workers = []monitor.Worker{}
	}
	return Overview{
		GeneratedAt:    httpx.Time(now),
		Queue:          queue,
		Workers:        workers,
		Throughput:     map[string]float64{"accepted": accepted},
		ExpressLatency: express,
	}, nil
}

func (s *Server) system(w http.ResponseWriter, r *http.Request) error {
	o, err := s.overview(r.Context())
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, o)
	return nil
}

func (s *Server) invariants(w http.ResponseWriter, r *http.Request) error {
	report, err := invariant.Run(r.Context(), s.db, s.cfg.Invariants)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, report)
	return nil
}
