// Command provider runs a simulated SMS operator for the gateway to send to.
//
// Usage:
//
//	provider [serve]
//	provider probe [--timeout=2s] <url>
//	provider version
//
// Configuration is read from environment variables; see
// docs/090-operations/020-configuration.md.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/mmrzaf/sms-gatway/internal/buildinfo"
	"github.com/mmrzaf/sms-gatway/internal/config"
	"github.com/mmrzaf/sms-gatway/internal/fakeprovider"
	"github.com/mmrzaf/sms-gatway/internal/httpx"
	"github.com/mmrzaf/sms-gatway/internal/logging"
)

const (
	exitOK      = 0
	exitFailure = 1
	exitUsage   = 2

	shutdownTimeout = 10 * time.Second
)

func main() {
	os.Exit(run(context.Background(), os.Args[1:], os.Stdout, os.Stderr, os.LookupEnv))
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer, lookup config.LookupFunc) int {
	cmd := "serve"
	if len(args) > 0 {
		cmd, args = args[0], args[1:]
	}
	switch cmd {
	case "serve":
		return serve(ctx, stdout, stderr, lookup)
	case "probe":
		return probe(ctx, args, stderr)
	case "version":
		fmt.Fprintln(stdout, buildinfo.String("provider"))
		return exitOK
	case "help", "-h", "--help":
		fmt.Fprintln(stdout, "Usage: provider [serve] | provider probe [--timeout=2s] <url> | provider version")
		return exitOK
	default:
		fmt.Fprintf(stderr, "provider: unknown command %q\n", cmd)
		return exitUsage
	}
}

func serve(ctx context.Context, stdout, stderr io.Writer, lookup config.LookupFunc) int {
	cfg, err := config.LoadFakeProvider(lookup)
	if err != nil {
		fmt.Fprintf(stderr, "provider: %v\n", err)
		return exitUsage
	}
	logger := logging.New(stdout, cfg.LogLevel).With("provider", cfg.Name)
	ctx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	sim := cfg.Simulation
	p := fakeprovider.New(fakeprovider.Config{
		Name:          cfg.Name,
		GatewayDLRURL: cfg.GatewayDLRURL,
		Secret:        cfg.Secret,
		Settings: fakeprovider.Settings{
			LatencyMS:     sim.LatencyMS,
			JitterMS:      sim.JitterMS,
			FailureRate:   sim.FailureRate,
			TimeoutRate:   sim.TimeoutRate,
			RejectRate:    sim.RejectRate,
			Outage:        sim.Outage,
			DeliveryRatio: sim.DeliveryRatio,
			DLRDelayMS:    sim.DLRDelayMS,
			DLRJitterMS:   sim.DLRJitterMS,
		},
	}, logger)
	defer p.Close()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", httpx.Healthz)
	mux.Handle("/", p.Handler())
	handler := httpx.Chain(mux, httpx.WithRequestID(logger), httpx.WithAccessLog(), httpx.WithRecovery())

	logger.Info("starting", "version", buildinfo.String("provider"), "addr", cfg.Addr, "settings", p.Settings())
	if err := httpx.Serve(ctx, httpx.NewServer(cfg.Addr, handler), shutdownTimeout); err != nil {
		logger.Error("stopped with error", "error", err)
		return exitFailure
	}
	logger.Info("stopped")
	return exitOK
}

func probe(ctx context.Context, args []string, stderr io.Writer) int {
	fs := flag.NewFlagSet("probe", flag.ContinueOnError)
	fs.SetOutput(stderr)
	timeout := fs.Duration("timeout", 2*time.Second, "request timeout")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitOK
		}
		return exitUsage
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(stderr, "usage: provider probe [--timeout=2s] <url>")
		return exitUsage
	}
	if err := httpx.Probe(ctx, fs.Arg(0), *timeout); err != nil {
		fmt.Fprintf(stderr, "probe: %v\n", err)
		return exitFailure
	}
	return exitOK
}
