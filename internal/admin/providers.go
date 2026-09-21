package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"

	"github.com/mmrzaf/sms-gatway/internal/config"
	"github.com/mmrzaf/sms-gatway/internal/httpx"
)

func (s *Server) provider(name string) (config.Provider, bool) {
	for _, p := range s.cfg.Providers {
		if p.Name == name {
			return p, true
		}
	}
	return config.Provider{}, false
}

var errProviderUnknown = httpx.NewError(http.StatusNotFound, httpx.CodeNotFound, "The provider is not configured.")

func errProviderUnreachable() error {
	return httpx.NewError(http.StatusBadGateway, "provider_unreachable", "The provider did not answer.")
}

// proxyProvider forwards a request to the provider's own admin API and
// returns its response unchanged.
func (s *Server) proxyProvider(path string) handlerFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		p, ok := s.provider(r.PathValue("name"))
		if !ok {
			return errProviderUnknown
		}
		url := p.URL + path
		if r.URL.RawQuery != "" {
			url += "?" + r.URL.RawQuery
		}
		req, err := http.NewRequestWithContext(r.Context(), r.Method, url, r.Body)
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", r.Header.Get("Content-Type"))
		resp, err := s.client.Do(req)
		if err != nil {
			return errProviderUnreachable()
		}
		defer resp.Body.Close()
		w.Header().Set("Content-Type", resp.Header.Get("Content-Type"))
		w.WriteHeader(resp.StatusCode)
		_, _ = io.Copy(w, resp.Body)
		return nil
	}
}

// providerCall sends a request to a provider's admin API and decodes the
// JSON answer into out. The dashboard uses it.
func (s *Server) providerCall(ctx context.Context, p config.Provider, method, path string, body, out any) error {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, p.URL+path, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.client.Do(req)
	if err != nil {
		return errProviderUnreachable()
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return httpx.NewError(http.StatusBadGateway, "provider_error", "The provider answered "+resp.Status+".")
	}
	return json.NewDecoder(resp.Body).Decode(out)
}
