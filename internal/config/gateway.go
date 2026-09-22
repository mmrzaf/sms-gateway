package config

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// Gateway is the configuration of the gateway binary in every role.
type Gateway struct {
	Database        Database
	LogLevel        string
	ShutdownTimeout time.Duration

	HTTPAddr   string
	AdminAddr  string
	WorkerAddr string
	AdminToken string

	API       API
	Providers Providers
	Dispatch  Dispatch
	Normal    ClassPolicy
	Express   ExpressPolicy
	DLR       DLR
}

// Database configures the PostgreSQL connection pool.
type Database struct {
	URL      string
	MaxConns int
}

// API configures the customer-facing API.
type API struct {
	Instances           int
	DefaultRateLimitRPS int
	KeyCacheTTL         time.Duration
	PriceNormal         int64
	PriceExpress        int64
	MaxSegments         int
}

// Provider is one SMS provider endpoint.
type Provider struct {
	Name string
	URL  string
}

// Providers configures provider access and protection.
type Providers struct {
	// List is in priority order.
	List                    []Provider
	Secret                  string
	RateLimit               int
	ExpressReservedRatio    float64
	CircuitFailureThreshold int
	CircuitOpenDuration     time.Duration
}

// Dispatch configures worker pools, claiming, and background loops.
type Dispatch struct {
	NormalLanes            int
	NormalConcurrency      int
	ExpressConcurrency     int
	ClaimBatchSize         int
	LeaseDuration          time.Duration
	PollInterval           time.Duration
	CompleterBatchSize     int
	CompleterFlushInterval time.Duration
	HeartbeatInterval      time.Duration
	SweepInterval          time.Duration
}

// ClassPolicy holds the retry settings of one service class.
type ClassPolicy struct {
	MaxAttempts int
	BackoffBase time.Duration
	BackoffMax  time.Duration
	TTL         time.Duration
	Timeout     time.Duration
}

// ExpressPolicy is the Express class policy plus its SLA.
type ExpressPolicy struct {
	ClassPolicy
	SLA time.Duration
}

// DLR configures delivery report batching.
type DLR struct {
	BatchSize     int
	FlushInterval time.Duration
}

var providerNamePattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,32}$`)

// LoadDatabase reads only the variables needed to reach the database.
// Commands such as migrate use it so they do not require unrelated settings.
func LoadDatabase(lookup LookupFunc) (Database, error) {
	r := newReader(lookup)
	db := readDatabase(r)
	return db, r.err()
}

func readDatabase(r *reader) Database {
	return Database{
		URL:      r.required("DATABASE_URL"),
		MaxConns: r.int("DB_MAX_CONNS", 20, 1, 1000),
	}
}

// LoadGateway reads and validates the complete gateway configuration.
func LoadGateway(lookup LookupFunc) (Gateway, error) {
	r := newReader(lookup)
	c := Gateway{
		Database:        readDatabase(r),
		LogLevel:        r.oneOf("LOG_LEVEL", "info", "debug", "info", "warn", "error"),
		ShutdownTimeout: r.duration("SHUTDOWN_TIMEOUT", 10*time.Second),

		HTTPAddr:   r.string("HTTP_ADDR", ":8080"),
		AdminAddr:  r.string("ADMIN_ADDR", ":8081"),
		WorkerAddr: r.string("WORKER_ADDR", ":8082"),
		AdminToken: r.required("ADMIN_TOKEN"),

		API: API{
			Instances:           r.int("API_INSTANCES", 1, 1, 10000),
			DefaultRateLimitRPS: r.int("DEFAULT_RATE_LIMIT_RPS", 100, 1, 100000),
			KeyCacheTTL:         r.duration("KEY_CACHE_TTL", 30*time.Second),
			PriceNormal:         int64(r.int("PRICE_NORMAL", 1, 1, 1000000)),
			PriceExpress:        int64(r.int("PRICE_EXPRESS", 3, 1, 1000000)),
			MaxSegments:         r.int("MAX_SEGMENTS", 10, 1, 10),
		},

		Providers: Providers{
			List:                    parseProviders(r, "PROVIDERS"),
			Secret:                  r.required("PROVIDER_SECRET"),
			RateLimit:               r.int("PROVIDER_RATE_LIMIT", 500, 1, 1000000),
			ExpressReservedRatio:    r.float("EXPRESS_RESERVED_RATIO", 0.2, 0, 1),
			CircuitFailureThreshold: r.int("CIRCUIT_FAILURE_THRESHOLD", 20, 1, 100000),
			CircuitOpenDuration:     r.duration("CIRCUIT_OPEN_DURATION", 10*time.Second),
		},

		Dispatch: Dispatch{
			NormalLanes:            r.int("NORMAL_LANES", 16, 1, 1024),
			NormalConcurrency:      r.int("NORMAL_CONCURRENCY", 256, 1, 100000),
			ExpressConcurrency:     r.int("EXPRESS_CONCURRENCY", 64, 1, 100000),
			ClaimBatchSize:         r.int("CLAIM_BATCH_SIZE", 200, 1, 10000),
			LeaseDuration:          r.duration("LEASE_DURATION", 30*time.Second),
			PollInterval:           r.duration("POLL_INTERVAL", 100*time.Millisecond),
			CompleterBatchSize:     r.int("COMPLETER_BATCH_SIZE", 200, 1, 10000),
			CompleterFlushInterval: r.duration("COMPLETER_FLUSH_INTERVAL", 50*time.Millisecond),
			HeartbeatInterval:      r.duration("HEARTBEAT_INTERVAL", 5*time.Second),
			SweepInterval:          r.duration("SWEEP_INTERVAL", 10*time.Second),
		},

		Normal: ClassPolicy{
			MaxAttempts: r.int("NORMAL_MAX_ATTEMPTS", 8, 1, 100),
			BackoffBase: r.duration("NORMAL_BACKOFF_BASE", 5*time.Second),
			BackoffMax:  r.duration("NORMAL_BACKOFF_MAX", 10*time.Minute),
			TTL:         r.duration("NORMAL_TTL", 24*time.Hour),
			Timeout:     r.duration("NORMAL_TIMEOUT", 5*time.Second),
		},

		Express: ExpressPolicy{
			ClassPolicy: ClassPolicy{
				MaxAttempts: r.int("EXPRESS_MAX_ATTEMPTS", 5, 1, 100),
				BackoffBase: r.duration("EXPRESS_BACKOFF_BASE", 1*time.Second),
				BackoffMax:  r.duration("EXPRESS_BACKOFF_MAX", 10*time.Second),
				TTL:         r.duration("EXPRESS_TTL", 5*time.Minute),
				Timeout:     r.duration("EXPRESS_TIMEOUT", 2*time.Second),
			},
			SLA: r.duration("EXPRESS_SLA", 30*time.Second),
		},

		DLR: DLR{
			BatchSize:     r.int("DLR_BATCH_SIZE", 500, 1, 10000),
			FlushInterval: r.duration("DLR_FLUSH_INTERVAL", 50*time.Millisecond),
		},
	}

	validateGateway(r, c)
	return c, r.err()
}

// validateGateway checks rules that relate several variables.
func validateGateway(r *reader, c Gateway) {
	maxTimeout := max(c.Normal.Timeout, c.Express.Timeout)
	r.check(c.Dispatch.LeaseDuration >= 3*maxTimeout, "LEASE_DURATION",
		"must be at least 3 × the largest provider timeout (%s), got %s", 3*maxTimeout, c.Dispatch.LeaseDuration)
	r.check(c.Normal.BackoffBase <= c.Normal.BackoffMax, "NORMAL_BACKOFF_BASE",
		"must not exceed NORMAL_BACKOFF_MAX")
	r.check(c.Express.BackoffBase <= c.Express.BackoffMax, "EXPRESS_BACKOFF_BASE",
		"must not exceed EXPRESS_BACKOFF_MAX")
	r.check(c.Express.Timeout <= c.Express.SLA, "EXPRESS_TIMEOUT",
		"must not exceed EXPRESS_SLA")
}

// parseProviders parses "A=http://a:9001,B=http://b:9002" into an ordered list.
func parseProviders(r *reader, key string) []Provider {
	raw := r.required(key)
	if raw == "" {
		return nil
	}
	var list []Provider
	seen := make(map[string]bool)
	for _, entry := range strings.Split(raw, ",") {
		name, rawURL, ok := strings.Cut(strings.TrimSpace(entry), "=")
		if !ok {
			r.fail(key, "entry %q must have the form NAME=URL", entry)
			continue
		}
		if !providerNamePattern.MatchString(name) {
			r.fail(key, "provider name %q must match %s", name, providerNamePattern)
			continue
		}
		if seen[name] {
			r.fail(key, "provider name %q is listed twice", name)
			continue
		}
		u, err := url.Parse(rawURL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			r.fail(key, "provider %s: %q is not an absolute http or https URL", name, rawURL)
			continue
		}
		seen[name] = true
		list = append(list, Provider{Name: name, URL: strings.TrimSuffix(rawURL, "/")})
	}
	return list
}

// String renders a summary that is safe to log: secrets are omitted.
func (c Gateway) String() string {
	names := make([]string, len(c.Providers.List))
	for i, p := range c.Providers.List {
		names[i] = p.Name
	}
	return fmt.Sprintf("providers=%s lanes=%d normal_concurrency=%d express_concurrency=%d lease=%s",
		strings.Join(names, ","), c.Dispatch.NormalLanes, c.Dispatch.NormalConcurrency,
		c.Dispatch.ExpressConcurrency, c.Dispatch.LeaseDuration)
}
