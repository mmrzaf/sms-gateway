package httpx

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type sample struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

func decodeRequest(t *testing.T, contentType, body string) (sample, error) {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	if contentType != "" {
		r.Header.Set("Content-Type", contentType)
	}
	var s sample
	err := DecodeJSON(httptest.NewRecorder(), r, &s)
	return s, err
}

func TestDecodeJSON(t *testing.T) {
	s, err := decodeRequest(t, "application/json; charset=utf-8", `{"name":"a","count":2}`)
	if err != nil || s.Name != "a" || s.Count != 2 {
		t.Fatalf("got %+v, %v", s, err)
	}
}

func TestDecodeJSONErrors(t *testing.T) {
	tests := []struct {
		name        string
		contentType string
		body        string
		status      int
		code        string
	}{
		{"missing content type", "", `{}`, 415, CodeUnsupportedMediaType},
		{"wrong content type", "text/plain", `{}`, 415, CodeUnsupportedMediaType},
		{"empty body", "application/json", ``, 400, CodeInvalidJSON},
		{"syntax error", "application/json", `{"name":`, 400, CodeInvalidJSON},
		{"wrong type", "application/json", `{"count":"x"}`, 400, CodeInvalidJSON},
		{"unknown field", "application/json", `{"nmae":"a"}`, 400, CodeInvalidJSON},
		{"trailing data", "application/json", `{"name":"a"}{}`, 400, CodeInvalidJSON},
		{"too large", "application/json", `{"name":"` + strings.Repeat("a", MaxBodyBytes) + `"}`, 413, CodePayloadTooLarge},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := decodeRequest(t, tc.contentType, tc.body)
			var e *Error
			if !errors.As(err, &e) {
				t.Fatalf("expected *Error, got %v", err)
			}
			if e.Status != tc.status || e.Code != tc.code {
				t.Errorf("got %d %s, want %d %s", e.Status, e.Code, tc.status, tc.code)
			}
		})
	}
}

func TestWriteErrorEnvelope(t *testing.T) {
	h := Chain(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		WriteError(w, r, ValidationError(FieldError{Field: "to", Code: "invalid_format", Message: "bad"}))
	}), WithRequestID(slog.New(slog.DiscardHandler)))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set(RequestIDHeader, "req-123")
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status %d", rec.Code)
	}
	var body struct {
		Error struct {
			Code      string       `json:"code"`
			Message   string       `json:"message"`
			Details   []FieldError `json:"details"`
			RequestID string       `json:"request_id"`
		} `json:"error"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Error.Code != CodeValidationFailed || body.Error.RequestID != "req-123" ||
		len(body.Error.Details) != 1 || body.Error.Details[0].Field != "to" {
		t.Errorf("unexpected envelope: %+v", body.Error)
	}
}

func TestWriteErrorHidesInternalErrors(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req = req.WithContext(WithLogger(req.Context(), slog.New(slog.DiscardHandler)))
	WriteError(rec, req, errors.New("connection refused to 10.0.0.5"))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status %d", rec.Code)
	}
	body, _ := io.ReadAll(rec.Body)
	if strings.Contains(string(body), "10.0.0.5") {
		t.Errorf("internal detail leaked: %s", body)
	}
	if !strings.Contains(string(body), `"details":null`) {
		t.Errorf("details should be null: %s", body)
	}
}

func TestRequestIDGeneratedWhenInvalid(t *testing.T) {
	var seen string
	h := WithRequestID(slog.New(slog.DiscardHandler))(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = RequestID(r.Context())
	}))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set(RequestIDHeader, "has spaces and\nnewlines")
	h.ServeHTTP(rec, req)

	if seen == "" || strings.ContainsAny(seen, " \n") {
		t.Fatalf("request id not regenerated: %q", seen)
	}
	if rec.Header().Get(RequestIDHeader) != seen {
		t.Errorf("response header %q, context %q", rec.Header().Get(RequestIDHeader), seen)
	}
}

func TestRecoveryReturns500(t *testing.T) {
	h := Chain(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("boom")
	}), WithRequestID(slog.New(slog.DiscardHandler)), WithRecovery())

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusInternalServerError || !strings.Contains(rec.Body.String(), CodeInternal) {
		t.Errorf("got %d %s", rec.Code, rec.Body.String())
	}
}

func TestReadyz(t *testing.T) {
	ok := Readyz(func(context.Context) error { return nil })
	rec := httptest.NewRecorder()
	ok(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("ready: status %d", rec.Code)
	}

	down := Readyz(func(context.Context) error { return errors.New("db down") })
	rec = httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	req = req.WithContext(WithLogger(req.Context(), slog.New(slog.DiscardHandler)))
	down(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("unavailable: status %d", rec.Code)
	}
}

func TestServeShutsDownOnCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	srv := NewServer("127.0.0.1:0", http.HandlerFunc(Healthz))
	done := make(chan error, 1)
	go func() { done <- Serve(ctx, srv, time.Second) }()
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Serve returned %v", err)
	}
}
