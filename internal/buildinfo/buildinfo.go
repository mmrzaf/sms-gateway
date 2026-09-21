// Package buildinfo reports the version and source revision of a binary.
package buildinfo

import (
	"fmt"
	"runtime"
	"runtime/debug"
)

// Version and Commit are set at build time with
//
//	-ldflags "-X github.com/mmrzaf/sms-gatway/internal/buildinfo.Version=... -X .../buildinfo.Commit=..."
//
// When Commit is empty, the revision recorded by the Go toolchain is used.
var (
	Version = "dev"
	Commit  = ""
)

// Revision returns the source revision the binary was built from, or
// "unknown" when it was not recorded.
func Revision() string {
	if Commit != "" {
		return Commit
	}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "unknown"
	}
	var rev string
	var modified bool
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
		case "vcs.modified":
			modified = s.Value == "true"
		}
	}
	if rev == "" {
		return "unknown"
	}
	if len(rev) > 12 {
		rev = rev[:12]
	}
	if modified {
		rev += "-dirty"
	}
	return rev
}

// String describes the named binary, for example "gateway 1.0.0 (a1b2c3d4e5f6, go1.26.8)".
func String(name string) string {
	return fmt.Sprintf("%s %s (%s, %s)", name, Version, Revision(), runtime.Version())
}
