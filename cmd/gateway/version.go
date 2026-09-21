package main

import (
	"context"
	"fmt"

	"github.com/mmrzaf/sms-gatway/internal/buildinfo"
)

func runVersion(_ context.Context, _ []string, e env) int {
	fmt.Fprintln(e.stdout, buildinfo.String("gateway"))
	return exitOK
}
