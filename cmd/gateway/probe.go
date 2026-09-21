package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"time"

	"github.com/mmrzaf/sms-gatway/internal/httpx"
)

// runProbe requests a URL and succeeds only on HTTP 200.
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
	if err := httpx.Probe(ctx, fs.Arg(0), *timeout); err != nil {
		fmt.Fprintf(e.stderr, "probe: %v\n", err)
		return exitFailure
	}
	return exitOK
}
