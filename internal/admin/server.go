// Package admin serves the operator's dashboard and the admin API behind it.
// Both are for running and demonstrating the gateway; they are served on the
// admin port, which is not exposed publicly, and protected by basic auth.
package admin

import (
	"crypto/subtle"
	"embed"
	"errors"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mmrzaf/sms-gatway/internal/billing"
	"github.com/mmrzaf/sms-gatway/internal/config"
	"github.com/mmrzaf/sms-gatway/internal/customer"
	"github.com/mmrzaf/sms-gatway/internal/httpx"
	"github.com/mmrzaf/sms-gatway/internal/invariant"
	"github.com/mmrzaf/sms-gatway/internal/message"
)

// User is the basic-auth user name; the password is the admin token.
const User = "admin"

//go:embed templates/*.html
var templateFS embed.FS

//go:embed static
var staticFS embed.FS

// Config configures the admin server.
type Config struct {
	Token               string
	Providers           []config.Provider
	Lanes               []string
	DefaultRateLimitRPS int
	Invariants          invariant.Config
}

// Server serves /admin/api/* and /dashboard/*.
type Server struct {
	db       *pgxpool.Pool
	messages *message.Service
	cfg      Config
	logger   *slog.Logger
	client   *http.Client
	pages    map[string]*template.Template
	now      func() time.Time
}

// New returns a Server.
func New(db *pgxpool.Pool, messages *message.Service, cfg Config, logger *slog.Logger) (*Server, error) {
	pages, err := parsePages()
	if err != nil {
		return nil, err
	}
	return &Server{
		db:       db,
		messages: messages,
		cfg:      cfg,
		logger:   logger,
		client:   &http.Client{Timeout: 5 * time.Second},
		pages:    pages,
		now:      time.Now,
	}, nil
}

// Register adds the admin API and dashboard routes to mux, behind basic auth.
func (s *Server) Register(mux *http.ServeMux) {
	api := map[string]handlerFunc{
		"GET /admin/api/customers":                  s.listCustomers,
		"POST /admin/api/customers":                 s.createCustomer,
		"GET /admin/api/customers/{id}":             s.getCustomer,
		"PATCH /admin/api/customers/{id}":           s.updateCustomer,
		"DELETE /admin/api/customers/{id}":          s.deleteCustomer,
		"POST /admin/api/customers/{id}/rotate-key": s.rotateKey,
		"POST /admin/api/customers/{id}/credits":    s.addCredits,
		"GET /admin/api/messages":                   s.listMessages,
		"GET /admin/api/messages/{id}":              s.getMessage,
		"GET /admin/api/system":                     s.system,
		"GET /admin/api/invariants":                 s.invariants,
		"GET /admin/api/providers/{name}/config":    s.proxyProvider("/admin/config"),
		"PUT /admin/api/providers/{name}/config":    s.proxyProvider("/admin/config"),
		"GET /admin/api/providers/{name}/messages":  s.proxyProvider("/admin/messages"),
		"GET /admin/api/providers/{name}/stats":     s.proxyProvider("/admin/stats"),
	}
	for pattern, h := range api {
		mux.Handle(pattern, s.basicAuth(s.jsonHandler(h)))
	}
	s.registerDashboard(mux)
	static, _ := fs.Sub(staticFS, "static")
	mux.Handle("GET /dashboard/static/", s.basicAuth(http.StripPrefix("/dashboard/static/", http.FileServerFS(static))))
}

// basicAuth requires user "admin" with the admin token as password.
func (s *Server) basicAuth(next http.Handler) http.Handler {
	token := []byte(s.cfg.Token)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		if !ok || user != User || subtle.ConstantTimeCompare([]byte(pass), token) != 1 {
			w.Header().Set("WWW-Authenticate", `Basic realm="sms-gateway-admin"`)
			httpx.WriteError(w, r, httpx.NewError(http.StatusUnauthorized, httpx.CodeUnauthorized,
				"Admin credentials are required."))
			return
		}
		next.ServeHTTP(w, r)
	})
}

// handlerFunc returns its error so error responses are produced in one place.
type handlerFunc func(w http.ResponseWriter, r *http.Request) error

func (s *Server) jsonHandler(h handlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := h(w, r); err != nil {
			httpx.WriteError(w, r, toHTTPError(err))
		}
	})
}

// toHTTPError maps domain errors to their HTTP representation.
func toHTTPError(err error) error {
	var validation *message.ValidationError
	switch {
	case errors.As(err, &validation):
		details := make([]httpx.FieldError, len(validation.Fields))
		for i, f := range validation.Fields {
			details[i] = httpx.FieldError{Field: f.Field, Code: f.Code, Message: f.Message}
		}
		return httpx.ValidationError(details...)
	case errors.Is(err, customer.ErrNotFound), errors.Is(err, billing.ErrCustomerNotFound):
		return httpx.NewError(http.StatusNotFound, httpx.CodeNotFound, "The customer does not exist.")
	case errors.Is(err, message.ErrNotFound):
		return httpx.NewError(http.StatusNotFound, httpx.CodeNotFound, "The message does not exist.")
	}
	return err
}
