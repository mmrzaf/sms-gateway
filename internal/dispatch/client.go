package dispatch

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/google/uuid"
)

// client sends messages to one provider.
type client struct {
	name string
	url  string
	http *http.Client
}

func newClient(name, baseURL string, maxConns int) *client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.MaxIdleConnsPerHost = maxConns
	transport.MaxIdleConns = maxConns
	return &client{name: name, url: baseURL + "/send", http: &http.Client{Transport: transport}}
}

type sendBody struct {
	ID   string `json:"id"`
	To   string `json:"to"`
	Text string `json:"text"`
}

type acceptedBody struct {
	ProviderRef string `json:"provider_ref"`
}

// send submits a message and returns the provider's reference. The message ID
// is the provider's idempotency key, so repeating a send whose outcome is
// unknown is safe. Failures are returned as *SendError.
func (c *client) send(ctx context.Context, timeout time.Duration, id uuid.UUID, to, text string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	body, err := json.Marshal(sendBody{ID: id.String(), To: to, Text: text})
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return "", classifyTransportError(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, resp.Body)
		return "", classifyStatus(resp.StatusCode)
	}
	var accepted acceptedBody
	if err := json.NewDecoder(resp.Body).Decode(&accepted); err != nil || accepted.ProviderRef == "" {
		return "", &SendError{Status: resp.StatusCode, ProviderFault: true,
			Err: fmt.Errorf("unreadable acceptance: %v", err)}
	}
	return accepted.ProviderRef, nil
}
