package api

import (
	"fmt"
	"net/http"

	"github.com/google/uuid"

	"github.com/mmrzaf/sms-gatway/internal/auth"
	"github.com/mmrzaf/sms-gatway/internal/billing"
	"github.com/mmrzaf/sms-gatway/internal/httpx"
	"github.com/mmrzaf/sms-gatway/internal/message"
	"github.com/mmrzaf/sms-gatway/internal/metrics"
)

type balanceResponse struct {
	Balance   int64      `json:"balance"`
	UpdatedAt httpx.Time `json:"updated_at"`
}

func (s *Server) getBalance(w http.ResponseWriter, r *http.Request, c auth.Customer) error {
	b, err := billing.GetBalance(r.Context(), s.db, c.ID)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, balanceResponse{Balance: b.Amount, UpdatedAt: httpx.Time(b.UpdatedAt)})
	return nil
}

type chargeRequest struct {
	Amount    *int64  `json:"amount"`
	ClientRef *string `json:"client_ref"`
}

type transactionResponse struct {
	ID        string     `json:"id"`
	Kind      string     `json:"kind"`
	Amount    int64      `json:"amount"`
	MessageID *string    `json:"message_id"`
	ClientRef *string    `json:"client_ref"`
	CreatedAt httpx.Time `json:"created_at"`
}

func toTransactionResponse(t billing.Transaction) transactionResponse {
	var messageID *string
	if t.MessageID != nil {
		id := t.MessageID.String()
		messageID = &id
	}
	return transactionResponse{
		ID:        t.ID.String(),
		Kind:      string(t.Kind),
		Amount:    t.Amount,
		MessageID: messageID,
		ClientRef: t.ClientRef,
		CreatedAt: httpx.Time(t.CreatedAt),
	}
}

type chargeResponse struct {
	Transaction transactionResponse `json:"transaction"`
	Balance     int64               `json:"balance"`
}

func (s *Server) charge(w http.ResponseWriter, r *http.Request, c auth.Customer) error {
	var req chargeRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	var problems []message.FieldError
	switch {
	case req.Amount == nil:
		problems = append(problems, message.FieldError{Field: "amount", Code: message.CodeRequired, Message: "is required"})
	case *req.Amount < 1 || *req.Amount > billing.MaxChargeAmount:
		problems = append(problems, message.FieldError{Field: "amount", Code: message.CodeOutOfRange,
			Message: fmt.Sprintf("must be an integer from 1 to %d", billing.MaxChargeAmount)})
	}
	switch {
	case req.ClientRef == nil:
		problems = append(problems, message.FieldError{Field: "client_ref", Code: message.CodeRequired, Message: "is required"})
	case !message.ValidClientRef(*req.ClientRef):
		problems = append(problems, message.FieldError{Field: "client_ref", Code: message.CodeInvalidFormat,
			Message: "must be 1-64 characters from A-Z a-z 0-9 . _ : -"})
	}
	if len(problems) > 0 {
		return &message.ValidationError{Fields: problems}
	}

	res, err := billing.ChargeCustomer(r.Context(), s.db, c.ID, *req.Amount, *req.ClientRef)
	if err != nil {
		return err
	}
	if res.Replayed {
		w.Header().Set(replayedHeader, "true")
	} else {
		metrics.CreditsCharged.Add(float64(*req.Amount))
	}
	httpx.WriteJSON(w, http.StatusCreated, chargeResponse{
		Transaction: toTransactionResponse(res.Transaction),
		Balance:     res.Balance,
	})
	return nil
}

func (s *Server) listTransactions(w http.ResponseWriter, r *http.Request, c auth.Customer) error {
	q := httpx.NewQuery(r.URL.Query())
	f := billing.TransactionFilter{
		Since: q.Time("since"),
		Until: q.Time("until"),
		After: q.Cursor(),
		Limit: q.Limit(defaultLimit, maxLimit),
	}
	if v := q.String("kind"); v != "" {
		k, ok := billing.ParseKind(v)
		if !ok {
			q.Fail("kind", httpx.FieldInvalidFormat, "must be charge, debit, or refund")
		}
		f.Kind = k
	}
	q.TimeRange(f.Since, f.Until)
	if err := q.Err(); err != nil {
		return err
	}

	list, more, err := billing.ListTransactions(r.Context(), s.db, c.ID, f)
	if err != nil {
		return err
	}
	data := make([]transactionResponse, len(list))
	for i, t := range list {
		data[i] = toTransactionResponse(t)
	}
	httpx.WriteJSON(w, http.StatusOK, httpx.NewPage(data, more, func() uuid.UUID { return list[len(list)-1].ID }))
	return nil
}
