package main

import (
	"context"
	"fmt"
	"text/tabwriter"

	"github.com/mmrzaf/sms-gatway/internal/config"
	"github.com/mmrzaf/sms-gatway/internal/store"
	"github.com/mmrzaf/sms-gatway/migrations"
)

func runMigrate(ctx context.Context, args []string, e env) int {
	action := "up"
	switch len(args) {
	case 0:
	case 1:
		action = args[0]
	default:
		fmt.Fprintln(e.stderr, "usage: gateway migrate [up|down|status]")
		return exitUsage
	}
	if action != "up" && action != "down" && action != "status" {
		fmt.Fprintf(e.stderr, "gateway migrate: unknown action %q; usage: gateway migrate [up|down|status]\n", action)
		return exitUsage
	}

	dbCfg, err := config.LoadDatabase(e.lookup)
	if err != nil {
		fmt.Fprintf(e.stderr, "gateway migrate: %v\n", err)
		return exitUsage
	}
	list, err := store.LoadMigrations(migrations.FS)
	if err != nil {
		fmt.Fprintf(e.stderr, "gateway migrate: %v\n", err)
		return exitFailure
	}
	pool, err := store.Open(ctx, dbCfg.URL, 2)
	if err != nil {
		fmt.Fprintf(e.stderr, "gateway migrate: %v\n", err)
		return exitFailure
	}
	defer pool.Close()
	m := store.NewMigrator(pool, list)

	switch action {
	case "up":
		applied, err := m.Up(ctx)
		for _, mig := range applied {
			fmt.Fprintf(e.stdout, "applied %05d_%s\n", mig.Version, mig.Name)
		}
		if err != nil {
			fmt.Fprintf(e.stderr, "gateway migrate: %v\n", err)
			return exitFailure
		}
		if len(applied) == 0 {
			fmt.Fprintln(e.stdout, "schema is up to date")
		}
	case "down":
		reverted, err := m.Down(ctx)
		if err != nil {
			fmt.Fprintf(e.stderr, "gateway migrate: %v\n", err)
			return exitFailure
		}
		if reverted == nil {
			fmt.Fprintln(e.stdout, "no migration to revert")
		} else {
			fmt.Fprintf(e.stdout, "reverted %05d_%s\n", reverted.Version, reverted.Name)
		}
	case "status":
		status, err := m.Status(ctx)
		if err != nil {
			fmt.Fprintf(e.stderr, "gateway migrate: %v\n", err)
			return exitFailure
		}
		tw := tabwriter.NewWriter(e.stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "VERSION\tNAME\tAPPLIED")
		for _, s := range status {
			applied := "pending"
			if s.AppliedAt != nil {
				applied = s.AppliedAt.UTC().Format("2006-01-02 15:04:05Z")
			}
			fmt.Fprintf(tw, "%05d\t%s\t%s\n", s.Version, s.Name, applied)
		}
		tw.Flush()
	}
	return exitOK
}
