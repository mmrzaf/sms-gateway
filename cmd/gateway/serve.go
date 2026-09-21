package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os/signal"
	"syscall"

	"github.com/mmrzaf/sms-gatway/internal/app"
	"github.com/mmrzaf/sms-gatway/internal/buildinfo"
	"github.com/mmrzaf/sms-gatway/internal/config"
	"github.com/mmrzaf/sms-gatway/internal/logging"
)

func runServe(ctx context.Context, args []string, e env) int {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.SetOutput(e.stderr)
	roleName := fs.String("role", string(app.RoleAll), "process role: api, worker, or all")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitOK
		}
		return exitUsage
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(e.stderr, "gateway serve: unexpected arguments %v\n", fs.Args())
		return exitUsage
	}
	role, err := app.ParseRole(*roleName)
	if err != nil {
		fmt.Fprintf(e.stderr, "gateway serve: %v\n", err)
		return exitUsage
	}
	cfg, err := config.LoadGateway(e.lookup)
	if err != nil {
		fmt.Fprintf(e.stderr, "gateway serve: %v\n", err)
		return exitUsage
	}

	logger := logging.New(e.stdout, cfg.LogLevel).With("role", string(role))
	ctx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	logger.Info("starting", "version", buildinfo.String("gateway"), "config", cfg.String())
	if err := app.Run(ctx, cfg, role, logger); err != nil {
		logger.Error("stopped with error", "error", err)
		return exitFailure
	}
	logger.Info("stopped")
	return exitOK
}
