package httpx

import (
	"context"
	"log/slog"
	"net/http"
	"regexp"
	"runtime/debug"
	"time"

	"github.com/google/uuid"
)

// RequestIDHeader carries the request ID in both directions.
const RequestIDHeader = "X-Request-Id"

var requestIDPattern = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,128}$`)

type ctxKey int

const (
	requestIDKey ctxKey = iota
	loggerKey
)

// Middleware wraps a handler.
type Middleware func(http.Handler) http.Handler

// Chain applies middleware so that the first one listed runs first.
func Chain(h http.Handler, mws ...Middleware) http.Handler {
	for i := len(mws) - 1; i >= 0; i-- {
		h = mws[i](h)
	}
	return h
}

// RequestID returns the request ID stored in ctx, or "" if there is none.
func RequestID(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey).(string)
	return id
}

// Logger returns the request-scoped logger stored in ctx, or the default logger.
func Logger(ctx context.Context) *slog.Logger {
	if l, ok := ctx.Value(loggerKey).(*slog.Logger); ok {
		return l
	}
	return slog.Default()
}

// WithLogger returns a copy of ctx carrying l as the request logger.
func WithLogger(ctx context.Context, l *slog.Logger) context.Context {
	return context.WithValue(ctx, loggerKey, l)
}

// WithRequestID accepts a well-formed X-Request-Id from the client or
// generates one, echoes it in the response, and stores it in the context
// together with a logger that includes it.
func WithRequestID(base *slog.Logger) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id := r.Header.Get(RequestIDHeader)
			if !requestIDPattern.MatchString(id) {
				id = uuid.Must(uuid.NewV7()).String()
			}
			w.Header().Set(RequestIDHeader, id)
			ctx := context.WithValue(r.Context(), requestIDKey, id)
			ctx = WithLogger(ctx, base.With("request_id", id))
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// WithAccessLog logs every request after it completes: at debug level
// normally, and at error level for 5xx responses.
func WithAccessLog() Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(rec, r)

			level := slog.LevelDebug
			if rec.status >= 500 {
				level = slog.LevelError
			}
			Logger(r.Context()).Log(r.Context(), level, "http request",
				"method", r.Method,
				"path", r.URL.Path,
				"status", rec.status,
				"duration_ms", time.Since(start).Milliseconds(),
			)
		})
	}
}

// WithRecovery turns a panic in a handler into a logged 500 response.
func WithRecovery() Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if v := recover(); v != nil {
					if v == http.ErrAbortHandler {
						panic(v)
					}
					Logger(r.Context()).Error("handler panic", "panic", v, "stack", string(debug.Stack()))
					WriteError(w, r, NewError(http.StatusInternalServerError, CodeInternal, "An unexpected error occurred."))
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}

// statusRecorder captures the response status for logging.
type statusRecorder struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (s *statusRecorder) WriteHeader(code int) {
	if !s.wroteHeader {
		s.status = code
		s.wroteHeader = true
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	s.wroteHeader = true
	return s.ResponseWriter.Write(b)
}

// Unwrap lets http.ResponseController reach the underlying writer.
func (s *statusRecorder) Unwrap() http.ResponseWriter {
	return s.ResponseWriter
}
