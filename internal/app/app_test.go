package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mmrzaf/sms-gatway/internal/config"
	"github.com/mmrzaf/sms-gatway/internal/customer"
	"github.com/mmrzaf/sms-gatway/internal/fakeprovider"
	"github.com/mmrzaf/sms-gatway/internal/testutil"
)

func freeAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().String()
}

// TestAllRolesEndToEnd runs the whole gateway in one process against a fake
// provider: a message submitted through the public API is dispatched,
// reported delivered through the admin port, and visible in metrics and the
// dashboard.
func TestAllRolesEndToEnd(t *testing.T) {
	dbURL := testutil.SchemaURL(t)
	publicAddr, adminAddr, workerAddr := freeAddr(t), freeAddr(t), freeAddr(t)

	logger := slog.New(slog.DiscardHandler)
	fp := fakeprovider.New(fakeprovider.Config{Name: "A", GatewayDLRURL: "http://" + adminAddr + "/internal/dlr",
		Secret: "secret", Settings: fakeprovider.Settings{DeliveryRatio: 1}}, logger)
	t.Cleanup(fp.Close)
	provider := httptest.NewServer(fp.Handler())
	t.Cleanup(provider.Close)

	cfg, err := config.LoadGateway(config.MapLookup(map[string]string{
		"DATABASE_URL":    dbURL,
		"ADMIN_TOKEN":     "admin-token",
		"PROVIDER_SECRET": "secret",
		"PROVIDERS":       "A=" + provider.URL,
		"HTTP_ADDR":       publicAddr,
		"ADMIN_ADDR":      adminAddr,
		"WORKER_ADDR":     workerAddr,
		"POLL_INTERVAL":   "20ms",
		"SWEEP_INTERVAL":  "100ms",
	}))
	if err != nil {
		t.Fatal(err)
	}

	// Migrate the schema the gateway will use, and create a customer in it.
	migrated := testutil.MigrateURL(t, dbURL)
	c, key, err := customer.Create(context.Background(), migrated, customer.New{Name: "acme", InitialCredits: 10, RateLimitRPS: 100})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Run(ctx, cfg, RoleAll, logger) }()
	defer func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("Run returned %v", err)
		}
	}()
	waitHTTP(t, "http://"+publicAddr+"/readyz")

	body, _ := json.Marshal(map[string]string{"to": "+989121234567", "text": "hello"})
	req, _ := http.NewRequest("POST", "http://"+publicAddr+"/v1/messages", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var accepted map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&accepted)
	resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("send: %d %v", resp.StatusCode, accepted)
	}

	deadline := time.Now().Add(10 * time.Second)
	for {
		req, _ := http.NewRequest("GET", fmt.Sprintf("http://%s/v1/messages/%s", publicAddr, accepted["id"]), nil)
		req.Header.Set("Authorization", "Bearer "+key)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		var m map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&m)
		resp.Body.Close()
		if m["status"] == "delivered" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("message not delivered: %v", m)
		}
		time.Sleep(50 * time.Millisecond)
	}

	for addr, want := range map[string]string{
		adminAddr:  `sms_messages_accepted_total{type="normal"}`,
		workerAddr: `sms_messages_completed_total{type="normal",status="sent"}`,
	} {
		if body := get(t, "http://"+addr+"/metrics", ""); !strings.Contains(body, want) {
			t.Errorf("metrics on %s lack %s", addr, want)
		}
	}
	if body := get(t, "http://"+adminAddr+"/dashboard/customers/"+c.ID.String(), "admin-token"); !strings.Contains(body, "acme") {
		t.Error("dashboard does not show the customer")
	}
}

func waitHTTP(t *testing.T, url string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		resp, err := http.Get(url)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s did not become ready", url)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func get(t *testing.T, url, adminToken string) string {
	t.Helper()
	req, _ := http.NewRequest("GET", url, nil)
	if adminToken != "" {
		req.SetBasicAuth("admin", adminToken)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return string(body)
}
