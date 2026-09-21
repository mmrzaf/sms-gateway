// Package api implements the customer-facing REST API under /v1.
//
// Handlers decode and validate HTTP input, call the domain packages, and
// encode responses. They contain no SQL and no business rules.
package api

import (
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mmrzaf/sms-gatway/internal/auth"
	"github.com/mmrzaf/sms-gatway/internal/httpx"
	"github.com/mmrzaf/sms-gatway/internal/message"
	"github.com/mmrzaf/sms-gatway/internal/ratelimit"
)

// Page size bounds for list endpoints.
const (
	defaultLimit = 50
	maxLimit     = 200
)

// Server serves the customer API.
type Server struct {
	db       *pgxpool.Pool
	messages *message.Service
	auth     *auth.Authenticator
	limiter  *ratelimit.Limiter
	now      func() time.Time
}

// New returns a Server.
func New(db *pgxpool.Pool, messages *message.Service, authn *auth.Authenticator, limiter *ratelimit.Limiter) *Server {
	return &Server{db: db, messages: messages, auth: authn, limiter: limiter, now: time.Now}
}

// Register adds the API routes and the API reference to mux.
func (s *Server) Register(mux *http.ServeMux) {
	mux.Handle("POST /v1/messages", s.authed(s.sendMessage))
	mux.Handle("POST /v1/messages/batch", s.authed(s.sendBatch))
	mux.Handle("GET /v1/messages/{id}", s.authed(s.getMessage))
	mux.Handle("GET /v1/messages", s.authed(s.listMessages))
	mux.Handle("GET /v1/balance", s.authed(s.getBalance))
	mux.Handle("POST /v1/balance/charges", s.authed(s.charge))
	mux.Handle("GET /v1/balance/transactions", s.authed(s.listTransactions))
	mux.Handle("GET /v1/reports/summary", s.authed(s.summary))

	mux.HandleFunc("GET /openapi.yaml", serveSpec)
	mux.HandleFunc("GET /docs", serveDocs)
}

// handlerFunc is an authenticated handler that returns its error instead of
// writing it, so error responses are produced in one place.
type handlerFunc func(w http.ResponseWriter, r *http.Request, c auth.Customer) error

// authed authenticates the request with its bearer API key and runs h.
func (s *Server) authed(h handlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := auth.BearerToken(r.Header.Get("Authorization"))
		c, err := s.auth.Authenticate(r.Context(), key)
		if err != nil {
			httpx.WriteError(w, r, toHTTPError(err))
			return
		}
		ctx := httpx.WithLogger(r.Context(), httpx.Logger(r.Context()).With("customer_id", c.ID.String()))
		r = r.WithContext(ctx)
		if err := h(w, r, c); err != nil {
			httpx.WriteError(w, r, toHTTPError(err))
		}
	})
}
