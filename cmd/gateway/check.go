package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/mmrzaf/sms-gatway/internal/config"
	"github.com/mmrzaf/sms-gatway/internal/invariant"
	"github.com/mmrzaf/sms-gatway/internal/store"
)

// runCheck runs the invariant checks and exits 1 if any fails.
func runCheck(ctx context.Context, args []string, e env) int {
	fs := flag.NewFlagSet("check", flag.ContinueOnError)
	fs.SetOutput(e.stderr)
	asJSON := fs.Bool("json", false, "print the report as JSON")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitOK
		}
		return exitUsage
	}
	cfg, err := config.LoadCheck(e.lookup)
	if err != nil {
		fmt.Fprintf(e.stderr, "gateway check: %v\n", err)
		return exitUsage
	}
	pool, err := store.Open(ctx, cfg.Database.URL, 2)
	if err != nil {
		fmt.Fprintf(e.stderr, "gateway check: %v\n", err)
		return exitFailure
	}
	defer pool.Close()

	report, err := invariant.Run(ctx, pool, invariant.Config{
		LeaseDuration: cfg.LeaseDuration,
		SweepInterval: cfg.SweepInterval,
	})
	if err != nil {
		fmt.Fprintf(e.stderr, "gateway check: %v\n", err)
		return exitFailure
	}

	if *asJSON {
		enc := json.NewEncoder(e.stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(report); err != nil {
			return exitFailure
		}
	} else {
		tw := tabwriter.NewWriter(e.stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "RESULT\tCHECK\tVIOLATIONS\tSAMPLE")
		for _, c := range report.Checks {
			result := "PASS"
			if !c.OK {
				result = "FAIL"
			}
			fmt.Fprintf(tw, "%s\t%s\t%d\t%s\n", result, c.Name, c.Violations, strings.Join(c.Sample, " "))
		}
		tw.Flush()
	}
	if !report.OK {
		return exitFailure
	}
	return exitOK
}
