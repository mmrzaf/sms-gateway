package auth_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/mmrzaf/sms-gatway/internal/auth"
	"github.com/mmrzaf/sms-gatway/internal/customer"
	"github.com/mmrzaf/sms-gatway/internal/testutil"
)

func TestGenerateKey(t *testing.T) {
	seen := make(map[string]bool)
	for range 200 {
		k := auth.GenerateKey()
		if !auth.ValidKeyFormat(k) {
			t.Fatalf("generated key %q has an invalid format", k)
		}
		if seen[k] {
			t.Fatal("duplicate key")
		}
		seen[k] = true
	}
	k := auth.GenerateKey()
	if p := auth.DisplayPrefix(k); len(p) != auth.DisplayPrefixLength || p != k[:12] {
		t.Errorf("display prefix %q", p)
	}
	if len(auth.HashKey(k)) != 32 {
		t.Error("hash is not SHA-256")
	}
}

func TestBearerToken(t *testing.T) {
	tests := map[string]string{
		"Bearer sk_abc":   "sk_abc",
		"bearer sk_abc":   "sk_abc",
		"Basic abc":       "",
		"sk_abc":          "",
		"":                "",
		"Bearer  sk_abc ": "sk_abc",
	}
	for header, want := range tests {
		if got := auth.BearerToken(header); got != want {
			t.Errorf("BearerToken(%q) = %q, want %q", header, got, want)
		}
	}
}

func TestAuthenticate(t *testing.T) {
	pool := testutil.DB(t)
	c, key := testutil.Customer(t, pool, 0)
	a := auth.NewAuthenticator(pool, 100*time.Millisecond)
	ctx := context.Background()

	got, err := a.Authenticate(ctx, key)
	if err != nil || got.ID != c.ID || got.RateLimitRPS != c.RateLimitRPS {
		t.Fatalf("valid key: %+v, %v", got, err)
	}
	for _, bad := range []string{"", "sk_short", auth.GenerateKey()} {
		if _, err := a.Authenticate(ctx, bad); !errors.Is(err, auth.ErrUnauthorized) {
			t.Errorf("key %q: %v", bad, err)
		}
	}

	if _, _, err := customer.RotateKey(ctx, pool, c.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Authenticate(ctx, key); err != nil {
		t.Errorf("old key should still work from the cache: %v", err)
	}
	time.Sleep(150 * time.Millisecond)
	if _, err := a.Authenticate(ctx, key); !errors.Is(err, auth.ErrUnauthorized) {
		t.Errorf("old key after the cache lifetime: %v", err)
	}
}
