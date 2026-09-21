package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mmrzaf/sms-gatway/internal/config"
)

func runCommand(vars map[string]string, args ...string) (int, string, string) {
	var out, errOut bytes.Buffer
	code := run(context.Background(), args, &out, &errOut, config.MapLookup(vars))
	return code, out.String(), errOut.String()
}

func TestCommands(t *testing.T) {
	if code, stdout, _ := runCommand(nil, "version"); code != exitOK || !strings.HasPrefix(stdout, "provider ") {
		t.Errorf("version: %d %q", code, stdout)
	}
	if code, _, stderr := runCommand(nil, "fly"); code != exitUsage || !strings.Contains(stderr, "unknown command") {
		t.Errorf("unknown command: %d %q", code, stderr)
	}
	if code, _, stderr := runCommand(nil); code != exitUsage || !strings.Contains(stderr, "PROVIDER_NAME: is required") {
		t.Errorf("serve without configuration: %d %q", code, stderr)
	}
}

func TestProbe(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer srv.Close()
	if code, _, stderr := runCommand(nil, "probe", srv.URL); code != exitOK {
		t.Errorf("healthy: %d %q", code, stderr)
	}
	if code, _, _ := runCommand(nil, "probe"); code != exitUsage {
		t.Errorf("missing url: %d", code)
	}
}
