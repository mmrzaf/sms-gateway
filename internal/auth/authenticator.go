package auth

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mmrzaf/sms-gatway/internal/store"
)

// ErrUnauthorized means the key is missing, malformed, or unknown.
var ErrUnauthorized = errors.New("unauthorized")

// Customer is the authenticated identity of a request.
type Customer struct {
	ID           uuid.UUID
	Name         string
	RateLimitRPS int
}

// Authenticator resolves API keys to customers, caching successful lookups
// for a fixed time. A deleted customer or a rotated key therefore stops
// working within one cache lifetime. Failed lookups are not cached, so
// invalid keys cannot fill the cache.
type Authenticator struct {
	db  store.Querier
	ttl time.Duration
	now func() time.Time

	mu        sync.Mutex
	entries   map[[sha256.Size]byte]cacheEntry
	lastPurge time.Time
}

type cacheEntry struct {
	customer Customer
	expires  time.Time
}

// NewAuthenticator returns an Authenticator that caches lookups for ttl.
func NewAuthenticator(db store.Querier, ttl time.Duration) *Authenticator {
	return &Authenticator{
		db:      db,
		ttl:     ttl,
		now:     time.Now,
		entries: make(map[[sha256.Size]byte]cacheEntry),
	}
}

// Authenticate returns the customer owning key, or ErrUnauthorized.
func (a *Authenticator) Authenticate(ctx context.Context, key string) (Customer, error) {
	if !ValidKeyFormat(key) {
		return Customer{}, ErrUnauthorized
	}
	hash := sha256.Sum256([]byte(key))
	now := a.now()

	a.mu.Lock()
	e, ok := a.entries[hash]
	a.mu.Unlock()
	if ok && now.Before(e.expires) {
		return e.customer, nil
	}

	var c Customer
	err := a.db.QueryRow(ctx,
		`SELECT id, name, rate_limit_rps FROM customers WHERE api_key_hash = $1`, hash[:]).
		Scan(&c.ID, &c.Name, &c.RateLimitRPS)
	if errors.Is(err, pgx.ErrNoRows) {
		return Customer{}, ErrUnauthorized
	}
	if err != nil {
		return Customer{}, fmt.Errorf("look up api key: %w", err)
	}

	a.mu.Lock()
	a.purgeExpiredLocked(now)
	a.entries[hash] = cacheEntry{customer: c, expires: now.Add(a.ttl)}
	a.mu.Unlock()
	return c, nil
}

// purgeExpiredLocked drops expired entries, at most once per cache lifetime.
func (a *Authenticator) purgeExpiredLocked(now time.Time) {
	if now.Sub(a.lastPurge) < a.ttl {
		return
	}
	for k, e := range a.entries {
		if !now.Before(e.expires) {
			delete(a.entries, k)
		}
	}
	a.lastPurge = now
}

// BearerToken extracts the token from an "Authorization: Bearer <token>"
// header value. It returns "" if the header has another form.
func BearerToken(header string) string {
	scheme, token, ok := strings.Cut(header, " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") {
		return ""
	}
	return strings.TrimSpace(token)
}
