package admin

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/mmrzaf/sms-gatway/internal/billing"
	"github.com/mmrzaf/sms-gatway/internal/customer"
	"github.com/mmrzaf/sms-gatway/internal/fakeprovider"
	"github.com/mmrzaf/sms-gatway/internal/httpx"
	"github.com/mmrzaf/sms-gatway/internal/invariant"
	"github.com/mmrzaf/sms-gatway/internal/message"
	"github.com/mmrzaf/sms-gatway/internal/store"
)

// Full pages are rendered inside the layout; fragments are rendered alone
// and refreshed in place by the dashboard script.
var (
	pageNames     = []string{"customers", "customer", "messages", "message", "system", "provider", "error"}
	fragmentNames = []string{"system_panel", "provider_panel"}
)

func parsePages() (map[string]*template.Template, error) {
	funcs := template.FuncMap{
		"ts":    formatTime,
		"tsp":   formatTimePtr,
		"deref": deref,
		"sec":   func(v float64) string { return fmt.Sprintf("%.3f s", v) },
		"rate":  func(v float64) string { return fmt.Sprintf("%.1f", v) },
	}
	layout, err := template.New("layout.html").Funcs(funcs).ParseFS(templateFS, "templates/layout.html")
	if err != nil {
		return nil, err
	}
	pages := make(map[string]*template.Template)
	for _, name := range pageNames {
		t, err := template.Must(layout.Clone()).ParseFS(templateFS, "templates/"+name+".html")
		if err != nil {
			return nil, err
		}
		pages[name] = t
	}
	for _, name := range fragmentNames {
		t, err := template.New(name+".html").Funcs(funcs).ParseFS(templateFS, "templates/"+name+".html")
		if err != nil {
			return nil, err
		}
		pages[name] = t
	}
	return pages, nil
}

func formatTime(t time.Time) string { return t.UTC().Format("2006-01-02 15:04:05.000Z") }

func formatTimePtr(t *time.Time) string {
	if t == nil {
		return "—"
	}
	return formatTime(*t)
}

func deref(s any) string {
	switch v := s.(type) {
	case *string:
		if v != nil {
			return *v
		}
	case *message.FailureReason:
		if v != nil {
			return string(*v)
		}
	}
	return "—"
}

func (s *Server) render(w http.ResponseWriter, status int, name string, data any) {
	var buf bytes.Buffer
	t := s.pages[name]
	entry := "layout.html"
	if t.Lookup(entry) == nil {
		entry = name + ".html"
	}
	if err := t.ExecuteTemplate(&buf, entry, data); err != nil {
		s.logger.Error("render dashboard page", "page", name, "error", err)
		http.Error(w, "The page could not be rendered.", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = buf.WriteTo(w)
}

// page is embedded in every full page's data.
type page struct {
	Title string
	Nav   string
}

// pageHandler wraps a dashboard handler; errors render the error page.
func (s *Server) pageHandler(h handlerFunc) http.Handler {
	return s.basicAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := h(w, r); err != nil {
			s.renderError(w, r, err)
		}
	}))
}

func (s *Server) renderError(w http.ResponseWriter, r *http.Request, err error) {
	status, msg := http.StatusInternalServerError, "An unexpected error occurred."
	var e *httpx.Error
	var validation *message.ValidationError
	switch mapped := toHTTPError(err); {
	case errors.As(err, &validation):
		status = http.StatusUnprocessableEntity
		parts := make([]string, len(validation.Fields))
		for i, f := range validation.Fields {
			parts[i] = f.Field + " " + f.Message
		}
		msg = strings.Join(parts, "; ")
	case errors.As(mapped, &e):
		status, msg = e.Status, e.Message
	default:
		httpx.Logger(r.Context()).Error("dashboard request failed", "error", err)
	}
	s.render(w, status, "error", struct {
		page
		Message string
	}{page{Title: "Error"}, msg})
}

func (s *Server) registerDashboard(mux *http.ServeMux) {
	routes := map[string]handlerFunc{
		"GET /dashboard/{$}":                        redirect("/dashboard/customers"),
		"GET /dashboard/customers":                  s.customersPage,
		"POST /dashboard/customers":                 s.createCustomerForm,
		"GET /dashboard/customers/{id}":             s.customerPage,
		"POST /dashboard/customers/{id}/update":     s.updateCustomerForm,
		"POST /dashboard/customers/{id}/credits":    s.creditsForm,
		"POST /dashboard/customers/{id}/rotate-key": s.rotateKeyForm,
		"POST /dashboard/customers/{id}/delete":     s.deleteCustomerForm,
		"GET /dashboard/messages":                   s.messagesPage,
		"GET /dashboard/messages/{id}":              s.messagePage,
		"GET /dashboard/system":                     s.systemPage,
		"GET /dashboard/system/panel":               s.systemPanel,
		"GET /dashboard/providers":                  s.providersIndex,
		"GET /dashboard/providers/{name}":           s.providerPage,
		"GET /dashboard/providers/{name}/panel":     s.providerPanel,
		"POST /dashboard/providers/{name}/config":   s.providerConfigForm,
	}
	for pattern, h := range routes {
		mux.Handle(pattern, s.pageHandler(h))
	}
}

func redirect(to string) handlerFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		http.Redirect(w, r, to, http.StatusSeeOther)
		return nil
	}
}

// formInt reads an integer form field; empty means nil.
func formInt(r *http.Request, name string) (*int64, error) {
	v := strings.TrimSpace(r.PostFormValue(name))
	if v == "" {
		return nil, nil
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return nil, &message.ValidationError{Fields: []message.FieldError{{Field: name, Code: message.CodeInvalidFormat, Message: "must be a whole number"}}}
	}
	return &n, nil
}

func (s *Server) customersPage(w http.ResponseWriter, r *http.Request) error {
	list, _, err := customer.List(r.Context(), s.db, uuid.Nil, 200)
	if err != nil {
		return err
	}
	s.render(w, http.StatusOK, "customers", struct {
		page
		Customers        []customer.Customer
		DefaultRateLimit int
	}{page{"Customers", "customers"}, list, s.cfg.DefaultRateLimitRPS})
	return nil
}

func (s *Server) createCustomerForm(w http.ResponseWriter, r *http.Request) error {
	credits, err := formInt(r, "initial_credits")
	if err != nil {
		return err
	}
	rps, err := formInt(r, "rate_limit_rps")
	if err != nil {
		return err
	}
	name := strings.TrimSpace(r.PostFormValue("name"))
	req := customerRequest{Name: &name, InitialCredits: credits}
	if rps != nil {
		n := int(*rps)
		req.RateLimitRPS = &n
	}
	if err := req.validate(true); err != nil {
		return err
	}
	c, key, err := s.newCustomer(r.Context(), req)
	if err != nil {
		return err
	}
	return s.renderCustomer(w, r, c.ID, key)
}

func (s *Server) customerPage(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, customer.ErrNotFound)
	if err != nil {
		return err
	}
	return s.renderCustomer(w, r, id, "")
}

// renderCustomer shows a customer; newKey, when set, is shown once.
func (s *Server) renderCustomer(w http.ResponseWriter, r *http.Request, id uuid.UUID, newKey string) error {
	detail, err := s.customerDetail(r.Context(), id)
	if err != nil {
		return err
	}
	transactions, _, err := billing.ListTransactions(r.Context(), s.db, id, billing.TransactionFilter{Limit: 20})
	if err != nil {
		return err
	}
	messages, _, err := s.messages.ListAll(r.Context(), message.AdminFilter{CustomerID: id, Filter: message.Filter{Limit: 20}}, s.now())
	if err != nil {
		return err
	}
	s.render(w, http.StatusOK, "customer", struct {
		page
		Customer     customerJSON
		NewKey       string
		Statuses     []message.Status
		Transactions []billing.Transaction
		Messages     []message.Inspected
	}{page{detail.Name, "customers"}, detail, newKey, message.Statuses, transactions, messages})
	return nil
}

func (s *Server) updateCustomerForm(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, customer.ErrNotFound)
	if err != nil {
		return err
	}
	rps, err := formInt(r, "rate_limit_rps")
	if err != nil {
		return err
	}
	var req customerRequest
	if name := strings.TrimSpace(r.PostFormValue("name")); name != "" {
		req.Name = &name
	}
	if rps != nil {
		n := int(*rps)
		req.RateLimitRPS = &n
	}
	if err := req.validate(false); err != nil {
		return err
	}
	if _, err := customer.Update(r.Context(), s.db, id, req.Name, req.RateLimitRPS); err != nil {
		return err
	}
	http.Redirect(w, r, "/dashboard/customers/"+id.String(), http.StatusSeeOther)
	return nil
}

func (s *Server) creditsForm(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, customer.ErrNotFound)
	if err != nil {
		return err
	}
	amount, err := formInt(r, "amount")
	if err != nil {
		return err
	}
	if amount == nil {
		amount = new(int64)
	}
	if _, err := s.addCredit(r.Context(), id, *amount); err != nil {
		return err
	}
	http.Redirect(w, r, "/dashboard/customers/"+id.String(), http.StatusSeeOther)
	return nil
}

func (s *Server) rotateKeyForm(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, customer.ErrNotFound)
	if err != nil {
		return err
	}
	_, key, err := customer.RotateKey(r.Context(), s.db, id)
	if err != nil {
		return err
	}
	return s.renderCustomer(w, r, id, key)
}

func (s *Server) deleteCustomerForm(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, customer.ErrNotFound)
	if err != nil {
		return err
	}
	if err := customer.Delete(r.Context(), s.db, id); err != nil {
		return err
	}
	http.Redirect(w, r, "/dashboard/customers", http.StatusSeeOther)
	return nil
}

func (s *Server) messagesPage(w http.ResponseWriter, r *http.Request) error {
	q := httpx.NewQuery(r.URL.Query())
	f := messageFilter(q)
	if err := q.Err(); err != nil {
		return err
	}
	list, more, err := s.messages.ListAll(r.Context(), f, s.now())
	if err != nil {
		return err
	}
	var next string
	if more {
		values := r.URL.Query()
		values.Set("cursor", store.EncodeCursor(list[len(list)-1].ID))
		next = "/dashboard/messages?" + values.Encode()
	}
	customers, _, err := customer.List(r.Context(), s.db, uuid.Nil, 200)
	if err != nil {
		return err
	}
	s.render(w, http.StatusOK, "messages", struct {
		page
		Messages  []message.Inspected
		Customers []customer.Customer
		Statuses  []message.Status
		Filter    message.AdminFilter
		Next      string
	}{page{"Messages", "messages"}, list, customers, message.Statuses, f, next})
	return nil
}

func (s *Server) messagePage(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, message.ErrNotFound)
	if err != nil {
		return err
	}
	in, err := s.messages.Inspect(r.Context(), id)
	if err != nil {
		return err
	}
	s.render(w, http.StatusOK, "message", struct {
		page
		M message.Inspected
	}{page{"Message", "messages"}, in})
	return nil
}

func (s *Server) systemPage(w http.ResponseWriter, r *http.Request) error {
	o, err := s.overview(r.Context())
	if err != nil {
		return err
	}
	var report *invariant.Report
	if r.URL.Query().Get("check") != "" {
		rep, err := invariant.Run(r.Context(), s.db, s.cfg.Invariants)
		if err != nil {
			return err
		}
		report = &rep
	}
	s.render(w, http.StatusOK, "system", struct {
		page
		Overview   systemView
		Invariants *invariant.Report
	}{page{"System", "system"}, s.systemView(o), report})
	return nil
}

func (s *Server) systemPanel(w http.ResponseWriter, r *http.Request) error {
	o, err := s.overview(r.Context())
	if err != nil {
		return err
	}
	s.render(w, http.StatusOK, "system_panel", s.systemView(o))
	return nil
}

// systemView is the overview with worker stats decoded for display.
type systemView struct {
	Overview
	WorkerStats []workerView
}

type workerView struct {
	ID       string
	LastSeen time.Time
	Stale    bool
	Stats    struct {
		Pools    map[string]struct{ Concurrency, InFlight int64 } `json:"pools"`
		Circuits map[string]string                                `json:"circuits"`
		Rates    map[string]float64                               `json:"rates_per_s"`
	}
}

func (s *Server) systemView(o Overview) systemView {
	v := systemView{Overview: o}
	for _, w := range o.Workers {
		wv := workerView{ID: w.ID, LastSeen: w.LastSeen, Stale: w.Stale}
		var raw struct {
			Pools map[string]struct {
				Concurrency int64 `json:"concurrency"`
				InFlight    int64 `json:"in_flight"`
			} `json:"pools"`
			Circuits map[string]string  `json:"circuits"`
			Rates    map[string]float64 `json:"rates_per_s"`
		}
		_ = json.Unmarshal(w.Stats, &raw)
		wv.Stats.Pools = make(map[string]struct{ Concurrency, InFlight int64 })
		for name, p := range raw.Pools {
			wv.Stats.Pools[name] = struct{ Concurrency, InFlight int64 }{p.Concurrency, p.InFlight}
		}
		wv.Stats.Circuits, wv.Stats.Rates = raw.Circuits, raw.Rates
		v.WorkerStats = append(v.WorkerStats, wv)
	}
	return v
}

func (s *Server) providersIndex(w http.ResponseWriter, r *http.Request) error {
	if len(s.cfg.Providers) == 0 {
		return errProviderUnknown
	}
	http.Redirect(w, r, "/dashboard/providers/"+s.cfg.Providers[0].Name, http.StatusSeeOther)
	return nil
}

type providerView struct {
	page
	Name      string
	Providers []string
	Settings  *fakeprovider.Settings
	Error     string
}

func (s *Server) providerPage(w http.ResponseWriter, r *http.Request) error {
	p, ok := s.provider(r.PathValue("name"))
	if !ok {
		return errProviderUnknown
	}
	v := providerView{page: page{"Provider " + p.Name, "providers"}, Name: p.Name}
	for _, pr := range s.cfg.Providers {
		v.Providers = append(v.Providers, pr.Name)
	}
	var settings fakeprovider.Settings
	if err := s.providerCall(r.Context(), p, http.MethodGet, "/admin/config", nil, &settings); err != nil {
		v.Error = "Provider " + p.Name + " is unreachable."
	} else {
		v.Settings = &settings
	}
	s.render(w, http.StatusOK, "provider", v)
	return nil
}

func (s *Server) providerPanel(w http.ResponseWriter, r *http.Request) error {
	p, ok := s.provider(r.PathValue("name"))
	if !ok {
		return errProviderUnknown
	}
	var data struct {
		Stats    fakeprovider.Stats
		Messages struct {
			Data []map[string]any `json:"data"`
		}
		Error string
	}
	err1 := s.providerCall(r.Context(), p, http.MethodGet, "/admin/stats", nil, &data.Stats)
	err2 := s.providerCall(r.Context(), p, http.MethodGet, "/admin/messages?limit=50", nil, &data.Messages)
	if err1 != nil || err2 != nil {
		data.Error = "Provider " + p.Name + " is unreachable."
	}
	s.render(w, http.StatusOK, "provider_panel", data)
	return nil
}

func (s *Server) providerConfigForm(w http.ResponseWriter, r *http.Request) error {
	p, ok := s.provider(r.PathValue("name"))
	if !ok {
		return errProviderUnknown
	}
	if err := r.ParseForm(); err != nil {
		return err
	}
	var u fakeprovider.SettingsUpdate
	ints := map[string]**int{"latency_ms": &u.LatencyMS, "jitter_ms": &u.JitterMS, "dlr_delay_ms": &u.DLRDelayMS, "dlr_jitter_ms": &u.DLRJitterMS}
	for name, dst := range ints {
		if v := strings.TrimSpace(r.PostFormValue(name)); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil {
				return &message.ValidationError{Fields: []message.FieldError{{Field: name, Code: message.CodeInvalidFormat, Message: "must be a whole number"}}}
			}
			*dst = &n
		}
	}
	floats := map[string]**float64{"failure_rate": &u.FailureRate, "timeout_rate": &u.TimeoutRate, "reject_rate": &u.RejectRate, "delivery_ratio": &u.DeliveryRatio}
	for name, dst := range floats {
		if v := strings.TrimSpace(r.PostFormValue(name)); v != "" {
			f, err := strconv.ParseFloat(v, 64)
			if err != nil {
				return &message.ValidationError{Fields: []message.FieldError{{Field: name, Code: message.CodeInvalidFormat, Message: "must be a number"}}}
			}
			*dst = &f
		}
	}
	outage := r.PostFormValue("outage") == "on"
	u.Outage = &outage

	var settings fakeprovider.Settings
	if err := s.providerCall(r.Context(), p, http.MethodPut, "/admin/config", u, &settings); err != nil {
		return err
	}
	http.Redirect(w, r, "/dashboard/providers/"+p.Name, http.StatusSeeOther)
	return nil
}
