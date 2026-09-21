package main

import (
	"context"
	"errors"
	"fmt"

	"github.com/mmrzaf/sms-gatway/internal/config"
	"github.com/mmrzaf/sms-gatway/internal/customer"
	"github.com/mmrzaf/sms-gatway/internal/store"
)

// demoCustomers are the customers used by the demo walkthrough and the load
// scenarios.
var demoCustomers = []struct {
	name    string
	keyVar  string
	credits int64
	rps     int
}{
	{"acme", "ACME_KEY", 10_000, 100},
	{"bulkco", "BULK_KEY", 1_000_000, 2_000},
	{"quickpay", "QUICK_KEY", 50_000, 200},
}

// runSeed creates the demo customers, or rotates the keys of those that
// exist, and prints NAME_KEY=sk_... lines to stdout so that
// eval "$(gateway seed)" exports them. Progress goes to stderr.
func runSeed(ctx context.Context, args []string, e env) int {
	if len(args) > 0 {
		fmt.Fprintln(e.stderr, "usage: gateway seed")
		return exitUsage
	}
	dbCfg, err := config.LoadDatabase(e.lookup)
	if err != nil {
		fmt.Fprintf(e.stderr, "gateway seed: %v\n", err)
		return exitUsage
	}
	pool, err := store.Open(ctx, dbCfg.URL, 2)
	if err != nil {
		fmt.Fprintf(e.stderr, "gateway seed: %v\n", err)
		return exitFailure
	}
	defer pool.Close()

	for _, d := range demoCustomers {
		c, err := customer.FindByName(ctx, pool, d.name)
		var key string
		switch {
		case errors.Is(err, customer.ErrNotFound):
			c, key, err = customer.Create(ctx, pool, customer.New{
				Name: d.name, InitialCredits: d.credits, RateLimitRPS: d.rps,
			})
			if err == nil {
				fmt.Fprintf(e.stderr, "created %s with %d credits\n", c.Name, c.Balance)
			}
		case err == nil:
			_, key, err = customer.RotateKey(ctx, pool, c.ID)
			if err == nil {
				fmt.Fprintf(e.stderr, "rotated the key of %s\n", c.Name)
			}
		}
		if err != nil {
			fmt.Fprintf(e.stderr, "gateway seed: %s: %v\n", d.name, err)
			return exitFailure
		}
		fmt.Fprintf(e.stdout, "%s=%s\n", d.keyVar, key)
	}
	return exitOK
}
