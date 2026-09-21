package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"time"
)

// runProbe requests a URL and succeeds only on HTTP 200. Container health
// checks use it because the runtime image contains no shell tools.
func runProbe(ctx context.Context, args []string, e env) int {
	fs := flag.NewFlagSet("probe", flag.ContinueOnError)
	fs.SetOutput(e.stderr)
	timeout := fs.Duration("timeout", 2*time.Second, "request timeout")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitOK
		}
		return exitUsage
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(e.stderr, "usage: gateway probe [--timeout=2s] <url>")
		return exitUsage
	}
	return probe(ctx, fs.Arg(0), *timeout, e)
}

func probe(ctx context.Context, url string, timeout time.Duration, e env) int {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		fmt.Fprintf(e.stderr, "probe: %v\n", err)
		return exitUsage
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fmt.Fprintf(e.stderr, "probe: %v\n", err)
		return exitFailure
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		fmt.Fprintf(e.stderr, "probe: %s returned %s\n", url, resp.Status)
		return exitFailure
	}
	return exitOK
}
