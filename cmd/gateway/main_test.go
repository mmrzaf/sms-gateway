package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mmrzaf/sms-gatway/internal/config"
	"github.com/mmrzaf/sms-gatway/internal/testutil"
)

func runCommand(t *testing.T, vars map[string]string, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code = run(context.Background(), args, env{stdout: &out, stderr: &errOut, lookup: config.MapLookup(vars)})
	return code, out.String(), errOut.String()
}

func TestUsage(t *testing.T) {
	if code, _, stderr := runCommand(t, nil); code != exitUsage || !strings.Contains(stderr, "Commands:") {
		t.Errorf("no arguments: code %d, stderr %q", code, stderr)
	}
	if code, stdout, _ := runCommand(t, nil, "help"); code != exitOK || !strings.Contains(stdout, "migrate") {
		t.Errorf("help: code %d, stdout %q", code, stdout)
	}
	if code, _, stderr := runCommand(t, nil, "launch"); code != exitUsage || !strings.Contains(stderr, `unknown command "launch"`) {
		t.Errorf("unknown command: code %d, stderr %q", code, stderr)
	}
}

func TestVersion(t *testing.T) {
	code, stdout, _ := runCommand(t, nil, "version")
	if code != exitOK || !strings.HasPrefix(stdout, "gateway ") {
		t.Errorf("code %d, stdout %q", code, stdout)
	}
}

func TestServeRejectsBadInput(t *testing.T) {
	if code, _, stderr := runCommand(t, nil, "serve", "--role=gateway"); code != exitUsage || !strings.Contains(stderr, "unknown role") {
		t.Errorf("bad role: code %d, stderr %q", code, stderr)
	}
	if code, _, stderr := runCommand(t, nil, "serve"); code != exitUsage || !strings.Contains(stderr, "DATABASE_URL: is required") {
		t.Errorf("missing config: code %d, stderr %q", code, stderr)
	}
}

func TestMigrateRejectsBadInput(t *testing.T) {
	if code, _, _ := runCommand(t, nil, "migrate", "sideways"); code != exitUsage {
		t.Errorf("bad action: code %d", code)
	}
	if code, _, stderr := runCommand(t, nil, "migrate"); code != exitUsage || !strings.Contains(stderr, "DATABASE_URL") {
		t.Errorf("missing config: code %d, stderr %q", code, stderr)
	}
}

func TestProbe(t *testing.T) {
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer ok.Close()
	failing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer failing.Close()

	if code, _, stderr := runCommand(t, nil, "probe", ok.URL); code != exitOK {
		t.Errorf("healthy target: code %d, stderr %q", code, stderr)
	}
	if code, _, stderr := runCommand(t, nil, "probe", failing.URL); code != exitFailure || !strings.Contains(stderr, "503") {
		t.Errorf("unhealthy target: code %d, stderr %q", code, stderr)
	}
	if code, _, _ := runCommand(t, nil, "probe"); code != exitUsage {
		t.Errorf("missing url: code %d", code)
	}
}

func TestMigrateCommand(t *testing.T) {
	vars := map[string]string{"DATABASE_URL": testutil.SchemaURL(t)}

	code, stdout, stderr := runCommand(t, vars, "migrate", "status")
	if code != exitOK || !strings.Contains(stdout, "pending") {
		t.Fatalf("status before up: code %d, stdout %q, stderr %q", code, stdout, stderr)
	}
	code, stdout, stderr = runCommand(t, vars, "migrate")
	if code != exitOK || !strings.Contains(stdout, "applied 00001_init") {
		t.Fatalf("up: code %d, stdout %q, stderr %q", code, stdout, stderr)
	}
	code, stdout, _ = runCommand(t, vars, "migrate", "up")
	if code != exitOK || !strings.Contains(stdout, "up to date") {
		t.Fatalf("second up: code %d, stdout %q", code, stdout)
	}
	code, stdout, _ = runCommand(t, vars, "migrate", "down")
	if code != exitOK || !strings.Contains(stdout, "reverted 00001_init") {
		t.Fatalf("down: code %d, stdout %q", code, stdout)
	}
}
