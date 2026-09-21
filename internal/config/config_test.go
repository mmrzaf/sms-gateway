package config

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func minimalEnv() map[string]string {
	return map[string]string{
		"DATABASE_URL":    "postgres://gateway:gateway@localhost:5432/gateway",
		"ADMIN_TOKEN":     "admin",
		"PROVIDER_SECRET": "secret",
		"PROVIDERS":       "A=http://provider-a:9001,B=http://provider-b:9002/",
	}
}

func TestLoadGatewayDefaults(t *testing.T) {
	c, err := LoadGateway(MapLookup(minimalEnv()))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	checks := []struct {
		name      string
		got, want any
	}{
		{"max conns", c.Database.MaxConns, 20},
		{"http addr", c.HTTPAddr, ":8080"},
		{"admin addr", c.AdminAddr, ":8081"},
		{"worker addr", c.WorkerAddr, ":8082"},
		{"price normal", c.API.PriceNormal, int64(1)},
		{"price express", c.API.PriceExpress, int64(3)},
		{"max segments", c.API.MaxSegments, 10},
		{"normal lanes", c.Dispatch.NormalLanes, 16},
		{"lease", c.Dispatch.LeaseDuration, 30 * time.Second},
		{"normal attempts", c.Normal.MaxAttempts, 8},
		{"normal ttl", c.Normal.TTL, 24 * time.Hour},
		{"express attempts", c.Express.MaxAttempts, 5},
		{"express timeout", c.Express.Timeout, 2 * time.Second},
		{"express sla", c.Express.SLA, 30 * time.Second},
		{"reserved ratio", c.Providers.ExpressReservedRatio, 0.2},
		{"dlr batch", c.DLR.BatchSize, 500},
	}
	for _, tc := range checks {
		if tc.got != tc.want {
			t.Errorf("%s: got %v, want %v", tc.name, tc.got, tc.want)
		}
	}
}

func TestLoadGatewayProviders(t *testing.T) {
	c, err := LoadGateway(MapLookup(minimalEnv()))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []Provider{{"A", "http://provider-a:9001"}, {"B", "http://provider-b:9002"}}
	if len(c.Providers.List) != len(want) {
		t.Fatalf("got %d providers, want %d", len(c.Providers.List), len(want))
	}
	for i := range want {
		if c.Providers.List[i] != want[i] {
			t.Errorf("provider %d: got %+v, want %+v", i, c.Providers.List[i], want[i])
		}
	}
	if p, ok := c.Providers.Provider("B"); !ok || p.URL != "http://provider-b:9002" {
		t.Errorf("Provider(B) = %+v, %v", p, ok)
	}
	if _, ok := c.Providers.Provider("C"); ok {
		t.Error("Provider(C) should not exist")
	}
}

func TestLoadGatewayReportsEveryProblem(t *testing.T) {
	env := minimalEnv()
	delete(env, "ADMIN_TOKEN")
	env["DB_MAX_CONNS"] = "zero"
	env["NORMAL_LANES"] = "5000"
	env["POLL_INTERVAL"] = "-1s"
	env["EXPRESS_RESERVED_RATIO"] = "1.5"
	env["LOG_LEVEL"] = "verbose"

	_, err := LoadGateway(MapLookup(env))
	var cfgErr *Error
	if !errors.As(err, &cfgErr) {
		t.Fatalf("expected *Error, got %v", err)
	}
	for _, key := range []string{"ADMIN_TOKEN", "DB_MAX_CONNS", "NORMAL_LANES", "POLL_INTERVAL", "EXPRESS_RESERVED_RATIO", "LOG_LEVEL"} {
		if !strings.Contains(err.Error(), key+":") {
			t.Errorf("error does not mention %s:\n%v", key, err)
		}
	}
	if len(cfgErr.Problems) != 6 {
		t.Errorf("got %d problems, want 6:\n%v", len(cfgErr.Problems), err)
	}
}

func TestLoadGatewayCrossFieldRules(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
		key  string
	}{
		{"lease shorter than 3 timeouts", map[string]string{"LEASE_DURATION": "10s"}, "LEASE_DURATION"},
		{"normal backoff base above max", map[string]string{"NORMAL_BACKOFF_BASE": "20m"}, "NORMAL_BACKOFF_BASE"},
		{"express backoff base above max", map[string]string{"EXPRESS_BACKOFF_BASE": "1m"}, "EXPRESS_BACKOFF_BASE"},
		{"express timeout above sla", map[string]string{"EXPRESS_SLA": "1s"}, "EXPRESS_TIMEOUT"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			env := minimalEnv()
			for k, v := range tc.env {
				env[k] = v
			}
			_, err := LoadGateway(MapLookup(env))
			if err == nil || !strings.Contains(err.Error(), tc.key+":") {
				t.Fatalf("expected a problem for %s, got %v", tc.key, err)
			}
		})
	}
}

func TestParseProvidersRejectsInvalidEntries(t *testing.T) {
	tests := map[string]string{
		"missing separator":  "A",
		"bad name":           "A B=http://x:1",
		"duplicate name":     "A=http://x:1,A=http://y:2",
		"relative url":       "A=/send",
		"unsupported scheme": "A=ftp://x:1",
	}
	for name, value := range tests {
		t.Run(name, func(t *testing.T) {
			env := minimalEnv()
			env["PROVIDERS"] = value
			_, err := LoadGateway(MapLookup(env))
			if err == nil || !strings.Contains(err.Error(), "PROVIDERS:") {
				t.Fatalf("expected a PROVIDERS problem, got %v", err)
			}
		})
	}
}

func TestLoadDatabaseNeedsOnlyDatabaseVariables(t *testing.T) {
	db, err := LoadDatabase(MapLookup(map[string]string{
		"DATABASE_URL": "postgres://localhost/gateway",
		"DB_MAX_CONNS": "5",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if db.MaxConns != 5 {
		t.Errorf("MaxConns = %d, want 5", db.MaxConns)
	}
	if _, err := LoadDatabase(MapLookup(nil)); err == nil {
		t.Error("expected an error when DATABASE_URL is missing")
	}
}

func TestStringOmitsSecrets(t *testing.T) {
	c, err := LoadGateway(MapLookup(minimalEnv()))
	if err != nil {
		t.Fatal(err)
	}
	s := c.String()
	for _, secret := range []string{"admin", "secret", "gateway:gateway"} {
		if strings.Contains(s, secret) {
			t.Errorf("String() leaks %q: %s", secret, s)
		}
	}
}
