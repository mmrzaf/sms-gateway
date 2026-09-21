package httpx

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"
)

// NewServer returns an HTTP server with conservative timeouts.
func NewServer(addr string, h http.Handler) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           h,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
}

// Serve listens on srv.Addr and serves until ctx is cancelled, then stops
// accepting connections and waits up to shutdownTimeout for in-flight
// requests to finish.
func Serve(ctx context.Context, srv *http.Server, shutdownTimeout time.Duration) error {
	ln, err := net.Listen("tcp", srv.Addr)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", srv.Addr, err)
	}
	return ServeListener(ctx, srv, ln, shutdownTimeout)
}

// ServeListener is Serve on an existing listener.
func ServeListener(ctx context.Context, srv *http.Server, ln net.Listener, shutdownTimeout time.Duration) error {
	errCh := make(chan error, 1)
	go func() { errCh <- srv.Serve(ln) }()

	select {
	case err := <-errCh:
		return fmt.Errorf("serve %s: %w", srv.Addr, err)
	case <-ctx.Done():
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shut down %s: %w", srv.Addr, err)
	}
	if err := <-errCh; err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("serve %s: %w", srv.Addr, err)
	}
	return nil
}
