package dlr

import (
	"context"
	"crypto/subtle"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/mmrzaf/sms-gatway/internal/httpx"
)

// SecretHeader carries the shared provider secret.
const SecretHeader = "X-Provider-Secret"

// submitTimeout bounds how long a callback waits for its batch.
const submitTimeout = 5 * time.Second

// Handler serves POST /internal/dlr.
type Handler struct {
	batcher   *Batcher
	secret    []byte
	providers map[string]bool
}

// NewHandler returns a handler that accepts reports signed with secret from
// the named providers.
func NewHandler(b *Batcher, secret string, providers []string) *Handler {
	known := make(map[string]bool, len(providers))
	for _, p := range providers {
		known[p] = true
	}
	return &Handler{batcher: b, secret: []byte(secret), providers: known}
}

type reportBody struct {
	MessageID   string `json:"message_id"`
	Provider    string `json:"provider"`
	ProviderRef string `json:"provider_ref"`
	Status      string `json:"status"`
	ReportedAt  string `json:"reported_at"`
}

type reportResponse struct {
	Outcome Outcome `json:"outcome"`
}

// ServeHTTP answers 200 once the report is durable (or known to need no
// change), 400 for unusable reports, 401 for a wrong secret, and 503 when the
// report could not be stored, which the provider retries.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if subtle.ConstantTimeCompare([]byte(r.Header.Get(SecretHeader)), h.secret) != 1 {
		httpx.WriteError(w, r, httpx.NewError(http.StatusUnauthorized, httpx.CodeUnauthorized,
			"A valid "+SecretHeader+" header is required."))
		return
	}
	var body reportBody
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	report, err := h.validate(body)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), submitTimeout)
	defer cancel()
	outcome, err := h.batcher.Submit(ctx, report)
	if err != nil {
		httpx.Logger(r.Context()).Warn("delivery report not stored", "message_id", report.MessageID, "error", err)
		httpx.WriteError(w, r, httpx.NewError(http.StatusServiceUnavailable, httpx.CodeUnavailable,
			"The report could not be stored; retry later."))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, reportResponse{Outcome: outcome})
}

func (h *Handler) validate(b reportBody) (Report, error) {
	var problems []httpx.FieldError
	id, err := uuid.Parse(b.MessageID)
	if err != nil {
		problems = append(problems, httpx.FieldError{Field: "message_id", Code: "invalid_format", Message: "must be a UUID"})
	}
	if !h.providers[b.Provider] {
		problems = append(problems, httpx.FieldError{Field: "provider", Code: "invalid_format", Message: "must be a configured provider"})
	}
	if b.ProviderRef == "" {
		problems = append(problems, httpx.FieldError{Field: "provider_ref", Code: "required", Message: "is required"})
	}
	if b.Status != "delivered" && b.Status != "undelivered" {
		problems = append(problems, httpx.FieldError{Field: "status", Code: "invalid_format", Message: "must be delivered or undelivered"})
	}
	if _, err := time.Parse(time.RFC3339Nano, b.ReportedAt); err != nil {
		problems = append(problems, httpx.FieldError{Field: "reported_at", Code: "invalid_format", Message: "must be an RFC 3339 timestamp"})
	}
	if len(problems) > 0 {
		return Report{}, &httpx.Error{Status: http.StatusBadRequest, Code: httpx.CodeValidationFailed,
			Message: "The delivery report is invalid.", Details: problems}
	}
	return Report{MessageID: id, Provider: b.Provider, ProviderRef: b.ProviderRef, Status: b.Status}, nil
}
