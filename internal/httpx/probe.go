package httpx

import (
	"context"
	"fmt"
	"net/http"
	"time"
)

// Probe requests url and returns nil only for HTTP 200. Container health
// checks use it through the binaries' probe commands, because the runtime
// image has no shell tools.
func Probe(ctx context.Context, url string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s returned %s", url, resp.Status)
	}
	return nil
}
