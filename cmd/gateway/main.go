// Command gateway runs the SMS gateway and its operational tasks.
//
// Usage:
//
//	gateway <command> [flags] [arguments]
//
// Run "gateway help" for the list of commands. Configuration is read from
// environment variables; flags only select what to run.
package main

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/mmrzaf/sms-gatway/internal/config"
)

// Exit codes.
const (
	exitOK      = 0
	exitFailure = 1
	exitUsage   = 2
)

// env is the process environment a command runs in.
type env struct {
	stdout io.Writer
	stderr io.Writer
	lookup config.LookupFunc
}

type command struct {
	name    string
	summary string
	run     func(ctx context.Context, args []string, e env) int
}

// commands lists the subcommands in the order "help" shows them. It is
// populated in init because the help command itself reads it.
var commands []command

func init() {
	commands = []command{
		{"serve", "Run the gateway in a role (api, worker, or all)", runServe},
		{"migrate", "Apply, revert, or list database migrations", runMigrate},
		{"seed", "Create the demo customers, or rotate their keys, and print the keys", runSeed},
		{"probe", "Request a health URL and exit 0 on HTTP 200", runProbe},
		{"version", "Print version information", runVersion},
		{"help", "Show this help", runHelp},
	}
}

func main() {
	os.Exit(run(context.Background(), os.Args[1:], env{
		stdout: os.Stdout,
		stderr: os.Stderr,
		lookup: os.LookupEnv,
	}))
}

func run(ctx context.Context, args []string, e env) int {
	if len(args) == 0 {
		printUsage(e.stderr)
		return exitUsage
	}
	name := args[0]
	if name == "-h" || name == "--help" {
		name = "help"
	}
	for _, c := range commands {
		if c.name == name {
			return c.run(ctx, args[1:], e)
		}
	}
	fmt.Fprintf(e.stderr, "gateway: unknown command %q\n\n", name)
	printUsage(e.stderr)
	return exitUsage
}

func runHelp(_ context.Context, _ []string, e env) int {
	printUsage(e.stdout)
	return exitOK
}

func printUsage(w io.Writer) {
	fmt.Fprintln(w, "Usage: gateway <command> [flags] [arguments]")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Commands:")
	for _, c := range commands {
		fmt.Fprintf(w, "  %-9s %s\n", c.name, c.summary)
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Configuration is read from environment variables; see docs/090-operations/020-configuration.md.")
}
