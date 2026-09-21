package httpx

import (
	"context"
	"net/http"
)

type healthBody struct {
	Status string `json:"status"`
}

// Healthz reports that the process is running.
func Healthz(w http.ResponseWriter, _ *http.Request) {
	WriteJSON(w, http.StatusOK, healthBody{Status: "ok"})
}

// Readyz reports whether the process can serve traffic, according to check.
func Readyz(check func(context.Context) error) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := check(r.Context()); err != nil {
			Logger(r.Context()).Warn("readiness check failed", "error", err)
			WriteJSON(w, http.StatusServiceUnavailable, healthBody{Status: "unavailable"})
			return
		}
		WriteJSON(w, http.StatusOK, healthBody{Status: "ready"})
	}
}
